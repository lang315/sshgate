# Tunnel half-close and slice 3a/3b minors Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tunnels propagate half-close; fix five deferred minors from slices 3a and 3b.

**Architecture:** Small, independent fixes: `internal/tunnel` (pipe, dial timeout, EMFILE), `internal/sshx/sshtest` (test server pipe and a stall knob), `internal/hub/tunnels.go` (one `client.Wait` waiter per client), and two renderer fixes (`App.tsx` tunnels list vs events, `FilesView.tsx` relist after a job).

**Tech Stack:** Go 1.27 (`golang.org/x/crypto/ssh` v0.54.0), React + vitest in `desktop/`.

**Spec:** `docs/superpowers/specs/2026-09-27-slice3b-port-forwarding-design.md` (tunnels) and `docs/superpowers/specs/2026-09-26-slice3a-sftp-design.md` (files). The design for this plan was approved in chat on 2026-09-28; it is the text of this plan.

## Global Constraints

- No new dependencies (Go or npm).
- Match surrounding style: terse comments, gofmt, no new abstractions beyond what a task names.
- Git author is "Lãng"; every commit message ends with the two trailer lines:
  `Co-Authored-By: <your model name> <noreply@anthropic.com>` and
  `Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2`
- Go checks: `go vet ./...` and `go test -race` on the touched packages must pass. Desktop checks: `cd desktop && npm run typecheck && npm test`.
- The renderer must never poll hub methods (idle-lock rule in CLAUDE.md); fixes here add no new hub calls except the ones already made.
- Tunnels bind loopback only; nothing here changes what a tunnel may bind or reach.

---

### Task 1: Half-close through tunnels

**Files:**
- Modify: `internal/tunnel/tunnel.go` (`pipe`)
- Modify: `internal/sshx/sshtest/forward.go` (`pipe`)
- Test: `internal/tunnel/tunnel_test.go`
- Modify: `docs/superpowers/ROADMAP.md` (the "Slice 3b → later (2026-09-27, final review)" half-close line), `docs/superpowers/specs/2026-09-27-slice3b-port-forwarding-design.md` (wherever it says both halves close when either side ends)

Today `pipe` closes both ends as soon as either direction ends, so a client that half-closes (`nc -N`, HTTP/1.0-style) never gets the reply.

- [ ] **Step 1: Write failing tests** in `tunnel_test.go`:
  - A helper target `replyAfterEOF(t)`: TCP listener on 127.0.0.1:0; per connection it reads until EOF, then writes `fmt.Sprintf("got %d", n)` and closes.
  - `TestHalfCloseLocal`, `TestHalfCloseRemote`, `TestHalfCloseDynamic`: through the tunnel, write `"ping"`, call `CloseWrite()` on the client's `*net.TCPConn`, then `io.ReadAll` with a 5 s deadline must return exactly `"got 4"`. Dynamic uses the existing SOCKS5 test helper in this file (or a small one: greeting `05 01 00`, CONNECT by IPv4 or domain, read the 10-byte reply).
  - `TestCloseEndsHalfClosedConn`: target reads to EOF and then holds the connection open without replying; client writes and half-closes; `f.Close()`; the client's read must return (EOF or error) within 2 s and `f.Conns()` must reach 0.
- [ ] **Step 2: Run** `go test -race ./internal/tunnel -run 'HalfClose|CloseEndsHalfClosed' -v` — the three HalfClose tests FAIL (reply lost).
- [ ] **Step 3: Implement** `pipe` in `internal/tunnel/tunnel.go`:

```go
// pipe copies both ways. A direction that ends at a clean EOF half-closes its
// destination, so the other direction can still carry a reply; an error, or
// a destination with no CloseWrite, closes both ends. Forward.Close closes
// the local end, whose read then fails, so nothing outlives the tunnel.
func pipe(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	half := func(dst, src io.ReadWriteCloser) {
		defer func() { done <- struct{}{} }()
		if _, err := io.Copy(dst, src); err == nil {
			if cw, ok := dst.(interface{ CloseWrite() error }); ok && cw.CloseWrite() == nil {
				return
			}
		}
		a.Close()
		b.Close()
	}
	go half(a, b)
	go half(b, a)
	<-done
	<-done
	a.Close()
	b.Close()
}
```

  `c.Dial`/`c.Listen` connections embed `ssh.Channel` (has `CloseWrite`); `*net.TCPConn` has `CloseWrite`.
- [ ] **Step 4:** Give `internal/sshx/sshtest/forward.go`'s `pipe` the same body (and comment); the test server must propagate half-close too or the reply never comes back. It is a separate package, so duplicate it rather than export from `tunnel`.
- [ ] **Step 5: Run** `go test -race ./internal/tunnel ./internal/sshx/... ./internal/hub -run 'Tunnel|HalfClose|Local|Remote|Dynamic|Forward' -v` then `go test -race ./internal/tunnel ./internal/hub` — all PASS.
- [ ] **Step 6: Docs.** ROADMAP: replace the half-close "later" line with one saying it was fixed on 2026-09-28 (tunnels now half-close; a direction's error still closes both ends). Spec 3b: update the sentence about closing both halves to the new rule. Update the `Updated:` line of ROADMAP to add "tunnel half-close fixed".
- [ ] **Step 7: Commit** `fix(tunnel): propagate half-close through forwards`.

### Task 2: Dial timeout and EMFILE in `internal/tunnel`

**Files:**
- Modify: `internal/tunnel/tunnel.go`, `internal/tunnel/socks.go` (only if its dial callback signature must change)
- Modify: `internal/sshx/sshtest/forward.go`, `internal/sshx/sshtest/sshtest.go` (a stall knob)
- Test: `internal/tunnel/tunnel_test.go`

Two problems: (1) `Local` and `Dynamic` use `c.Dial`, which has no timeout, and `Forward.Close` cannot interrupt a dial in flight, so a handler goroutine and its tracked connection outlive `Close` while the server sits on the channel open. (2) Any `Accept` error ends the tunnel, including `EMFILE`/`ENFILE` (out of file descriptors), which is transient.

- [ ] **Step 1: sshtest knob.** Add `func (s *Server) StallDirect()`: after it, `direct-tcpip` channel opens are never answered (the handler blocks until the server connection closes, then returns). Mirror `RefuseForward`'s style (a bool under `s.mu`, read where `directTCPIP` is dispatched in `sshtest.go`).
- [ ] **Step 2: Write failing tests:**
  - `TestCloseInterruptsDial`: server with `StallDirect()`; `Local` tunnel; connect a client; wait `f.Conns() == 1`; `f.Close()`; `f.Conns()` must reach 0 within 1 s (the handler returned).
  - `TestDialTimeout`: same stalled server; set the package var `dialTimeout` to 100 ms for the test (restore in `t.Cleanup`); connect a client; its read must return EOF/error within 2 s, without calling `Close`.
  - `TestAcceptRetriesEMFILE`: a fake `net.Listener` wrapping a real one whose first `Accept` returns `&net.OpError{Op: "accept", Net: "tcp", Err: os.NewSyscallError("accept", syscall.EMFILE)}`; `serve` over it must keep running and carry a later connection (use the echo target and `ping`); `f.Err()` is nil.
- [ ] **Step 3: Run** `go test -race ./internal/tunnel -run 'InterruptsDial|DialTimeout|EMFILE' -v` — FAIL.
- [ ] **Step 4: Implement:**
  - `var dialTimeout = 10 * time.Second` (package level; `Remote` already uses 10 s — use the var there too).
  - `Forward` gets a `ctx context.Context` and `cancel context.CancelFunc`, created in `serve`; `Close` calls `cancel()`. The `handle` callback becomes `func(ctx context.Context, in net.Conn)`.
  - `Local`: `dctx, cancel := context.WithTimeout(ctx, dialTimeout); out, err := c.DialContext(dctx, "tcp", target); cancel()`.
  - `Dynamic`: the dial callback passed to `socks` does the same with `DialContext`.
  - `Remote`: `(&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", target)`.
  - Accept loop: if `errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE)` and the forward is not closed, sleep a backoff (start 5 ms, double, cap 1 s, reset after a successful Accept) and continue; otherwise keep today's behaviour. Sleep with a `select` on `ctx.Done()` and a timer so `Close` is not delayed by the backoff.
- [ ] **Step 5: Run** `go test -race ./internal/tunnel ./internal/hub ./internal/sshx/...` — PASS. `go vet ./...` clean. Cross-compile check: `GOOS=windows go build ./...`.
- [ ] **Step 6: Commit** `fix(tunnel): bound and cancel dials; survive EMFILE on accept`.

### Task 3: One `client.Wait` waiter per client in the hub

**Files:**
- Modify: `internal/hub/tunnels.go` (`tunnelSet`, end of `StartTunnel`)
- Test: `internal/hub/tunnels_test.go`

Today every successful `StartTunnel` spawns `go func() { client.Wait(); h.endTunnel(lt, "connection lost") }()`, which lives until the SSH client dies, so each start/stop cycle leaks one goroutine for the life of the connection.

- [ ] **Step 1: Write a failing test** `TestTunnelOneWaiterPerClient`: save one local tunnel, then start and stop it 20 times on the same connection; assert `len(h.tunnels.waiters) == 1` (white-box, same package). Keep `TestTunnelEndedByServerChangeAndConnectionLoss` passing unchanged — it proves connection loss still ends a tunnel.
- [ ] **Step 2: Run** `go test -race ./internal/hub -run 'OneWaiter' -v` — FAIL (field missing).
- [ ] **Step 3: Implement:**
  - `tunnelSet` gains `waiters map[*ssh.Client]chan struct{}` (guarded by `mu`; init in `newTunnelSet`).
  - `func (ts *tunnelSet) clientDone(c *ssh.Client) <-chan struct{}`: under `mu`, return the existing channel, or create one and start `go func() { c.Wait(); ts.mu.Lock(); delete(ts.waiters, c); ts.mu.Unlock(); close(ch) }()`.
  - Replace the two goroutines at the end of `StartTunnel` with one:

```go
	gone := h.tunnels.clientDone(client)
	go func() {
		select {
		case <-gone:
			h.endTunnel(lt, "connection lost")
		case <-fwd.Done():
			if fwd.Err() != nil {
				h.endTunnel(lt, "connection lost")
			}
		}
	}()
```

  - Call `clientDone` where `mu` is not held (it takes `mu`). Keep the lock-order comment accurate if you touch it.
- [ ] **Step 4: Run** `go test -race ./internal/hub` — PASS (run it twice; tunnel tests are timing-sensitive).
- [ ] **Step 5: Commit** `fix(hub): one client.Wait waiter per client for tunnels`.

### Task 4: Renderer — tunnels list vs events, relist after a job

**Files:**
- Modify: `desktop/src/renderer/tunnels.ts`, `desktop/src/renderer/App.tsx`
- Modify: `desktop/src/renderer/files.ts`, `desktop/src/renderer/FilesView.tsx`
- Test: `desktop/test/tunnels.test.ts`, `desktop/test/files.test.ts`

(1) `App.tsx` `reloadTunnels` sets the `tunnels.list` reply as-is; a `tunnels.state` event that arrived while the request was in flight is overwritten by the older snapshot, so a row can show a stale status until the next event. Events and replies share one ordered stream, so replaying, in order, every event seen since the request was sent onto the reply yields the newest state per tunnel.

(2) `FilesView.tsx` `finishJob` relists only when no load is in flight (`relistAfterJob`). If a job ends while a load of that same folder is in flight, that load may predate the job's last change and nothing relists after it, so the listing stays stale.

- [ ] **Step 1: Write failing vitest tests:**
  - `tunnels.test.ts`: `replayStates(snapshot, events)` applies events in order with `applyState` — a snapshot row `stopped` plus events `[running(conns 0), running(conns 2)]` ends `running` with conns 2; an event for an id not in the snapshot is ignored; an empty event list returns the snapshot unchanged.
  - `files.test.ts`: `relistAfterJob(jobFolder, shown, requested, loading)` now returns `'now' | 'after' | 'no'`: `'now'` when `!loading && jobFolder === shown && requested === shown`; `'after'` when `loading && requested === jobFolder`; `'no'` otherwise (a load of a different folder in flight, or the author has moved on). Update the existing `relistAfterJob` tests to the new return values.
- [ ] **Step 2: Run** `cd desktop && npx vitest run test/tunnels.test.ts test/files.test.ts` — FAIL.
- [ ] **Step 3: Implement:**
  - `tunnels.ts`: `export const replayStates = (list: TunnelView[], events: TunnelState[]) => events.reduce(applyState, list)`.
  - `App.tsx`: a ref holding the set of in-flight reload buffers (`useRef(new Set<TunnelState[]>())`). `reloadTunnels` creates a buffer, adds it, calls `hub.tunnelsList()`, and on reply `setTunnels(replayStates(reply, buf))`; it removes the buffer in `finally`. The existing `tunnels.state` handler also pushes each event into every active buffer. No new hub calls.
  - `files.ts`: `relistAfterJob` returns the three-way value above (update its comment).
  - `FilesView.tsx`: a `relistPending` ref (`string | undefined`). In `finishJob`: `'now'` → `void load(shown.current)` as today; `'after'` → `relistPending.current = j.folder`. In `load`'s `finally`, when this is the latest load (`gen === loadGen.current`): if `relistPending.current !== undefined`, take and clear it, and if it equals both `shown.current` and `requested.current`, `void load(shown.current)`. Any new `load()` of another folder simply leaves the pending value unmatched and it is dropped.
- [ ] **Step 4: Run** `cd desktop && npm run typecheck && npm test` — PASS.
- [ ] **Step 5: Run e2e for the touched views:** `cd desktop && npm run e2e -- e2e/tunnels.spec.ts e2e/files.spec.ts` — PASS (needs a display; macOS is fine).
- [ ] **Step 6: Commit** `fix(desktop): tunnels list keeps newer states; relist after a job that ends mid-load`.
