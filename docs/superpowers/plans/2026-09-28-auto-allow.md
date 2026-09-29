# Per-host Auto-allow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the author put one host on auto-allow (timed 15 min–4 h, or forever with a Resume after each unlock) so plain AI `exec` runs there without a click, with every other gate kept, visible, audited, and stoppable.

**Architecture:** The hub owns grants (`map[server]*grant` under `h.mu`) and checks them in `Hub.Exec` right before `broker.Submit`, in one locked section that also resolves the server and compares it with the snapshot taken at enable time. Forever is a MAC-covered `config.Server.AutoAllow` flag that only `servers.setAutoAllow` writes; nothing arms a grant but that call. The desktop app gets a host-card button and dialog, a chip, a paused banner, a tab dot, an AI-button segment, and an Auto-allowed feed in the AI column.

**Tech Stack:** Go 1.27 (hub, `internal/config`, `internal/broker`), Electron + React + vitest + Playwright (`desktop/`).

**Spec:** `docs/superpowers/specs/2026-09-28-auto-allow-design.md` (approved 2026-09-28). Read it; it is the authority. Background: `CLAUDE.md` (architecture and conventions).

## Global Constraints

- No new dependencies (Go or npm).
- Plain `exec` only; `sudoExec` is never auto-allowed.
- Refused hosts: login user `root`, a stored su password (`EncSuPassword`), a stored sudo password (`EncSudoPassword`); also not AI-visible, no pinned host key, locked, no vault. Hidden and nonexistent servers give byte-identical errors (`serverNotFound`).
- Modes: `off`, `15m`, `30m`, `60m`, `2h`, `4h`, `forever`.
- At most 2 auto runs in flight per host (`maxAutoInflight = 2`); more go to approval.
- Auto runs never increment `h.running` and never count as UI activity.
- A timed grant holds off the idle lock until its deadline; forever does not; manual Lock ends every grant.
- A reload never arms a grant; only `servers.setAutoAllow` does.
- Every `servers.save`, `servers.delete`, `servers.forgetHostKey`, and `vault.create` turns auto-allow off (grant and flag).
- The MCP door never reads or changes auto-allow; `listServers` does not show it.
- Audit: `approval: "auto"` on every auto exec record; `waitMs` on human-decided records; config actions `autoAllowOn`, `autoAllowResume`, `autoAllowOff`, `autoAllowCheck`.
- Protocol version 6 (`ProtocolVersion` in `internal/hub/idle.go`, `PROTOCOL_VERSION` in `desktop/src/shared/protocol.ts`, and the desktop test fixture).
- The renderer never calls the hub in response to `autoAllow.ran`, `autoAllow.off`, or a timer (idle-lock rule: every call but `status` counts as UI activity).
- New colour token `--auto` (not amber, never pulsing); components use tokens only.
- Commit trailer (use your own model name):
  `Co-Authored-By: <model> <noreply@anthropic.com>`
  `Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2`
- Checks: `go vet ./...`, `go test -race ./internal/... ./cmd/...` (touched packages at least); desktop: `cd desktop && npm run typecheck && npm test`.

---

### Task 1: Audit and config fields; `waitMs`

**Files:**
- Modify: `internal/broker/audit.go` (`AuditRecord`, `ConfigRecord`)
- Modify: `internal/config/store.go` (`Server`)
- Modify: `internal/hub/hosts.go` (`dialChanged`, `CreateVault`, `changes`)
- Modify: `internal/hub/hub.go` (`Exec`: set `WaitMs`)
- Test: `internal/broker/audit_test.go`, `internal/config/server_input_test.go`, `internal/config/store_test.go`, `internal/hub/hosts_test.go`, `internal/hub/hub_test.go`

**Interfaces:**
- Produces: `broker.AuditRecord.Approval string` (`json:"approval,omitempty"`), `broker.AuditRecord.WaitMs int64` (`json:"waitMs,omitempty"`); `broker.ConfigRecord.Until string` (`json:"until,omitempty"`), `Forever bool` (`json:"forever,omitempty"`), `Reason string` (`json:"reason,omitempty"`); `config.Server.AutoAllow bool` (`json:"autoAllow,omitempty"`).

- [ ] **Step 1: Write failing tests**
  - `internal/config/server_input_test.go`: `TestApplyServerDropsAutoAllow`: a file with server `a` having `AutoAllow: true`; `ApplyServer(f, "a", ServerInput{Name:"a", Host:same, Port:same, User:same, Auth:"agent", AIVisible:true}, key)`; `after.AutoAllow` must be `false`.
  - `internal/config/store_test.go`: `TestAutoAllowCoveredByMAC`: save a file with a KDF and one server (`AutoAllow: false`) with `config.Save`; edit the JSON on disk to add `"autoAllow":true` to that server; `config.Load` then `f.VerifyMAC(key)` must fail. (Follow the existing MAC tamper test in that file for the exact helpers.)
  - `internal/hub/hosts_test.go`: `TestDialChangedIgnoresAutoAllow`: `dialChanged(a, b)` is false when only `AutoAllow` differs. `TestCreateVaultClearsAutoAllow`: a KDF-less store whose server has `AutoAllow: true` → `CreateVault("password1")` → reloaded server has `AutoAllow == false` (use the existing CreateVault test setup in that file).
  - `internal/hub/hub_test.go`: `TestExecRecordsWaitMs`: `newHub`, run `h.Exec` on `vis` in a goroutine, `time.Sleep(20 * time.Millisecond)` after `waitPending`, `allowFirst`; read the last line of the audit file (`filepath.Join(filepath.Dir(path), "audit.jsonl")`) and assert `waitMs >= 20` and `approval` absent. Do the same with a deny: `waitMs` set.
  - `internal/broker/audit_test.go`: `TestAuditRecordOmitsEmptyAutoFields`: marshal an `AuditRecord{}` and a `ConfigRecord{}`; the JSON has no `approval`, `waitMs`, `until`, `forever`, `reason` keys.
- [ ] **Step 2: Run** `go test ./internal/broker ./internal/config ./internal/hub -run 'AutoAllow|WaitMs|OmitsEmptyAuto' -v` — FAIL (fields missing).
- [ ] **Step 3: Implement**
  - `AuditRecord`: add after `StderrBytes`:
    ```go
    	Approval    string    `json:"approval,omitempty"` // "auto" when a grant allowed it (spec 2026-09-28)
    	WaitMs      int64     `json:"waitMs,omitempty"`   // submit to the human's decision
    ```
  - `ConfigRecord`: add after `Changed`:
    ```go
    	Until   string `json:"until,omitempty"`   // autoAllowOn: RFC 3339 deadline of a timed grant
    	Forever bool   `json:"forever,omitempty"` // autoAllowOn: a forever grant
    	Reason  string `json:"reason,omitempty"`  // autoAllowOff: why it ended; autoAllowCheck: the result
    ```
    and extend the `Action` comment with `autoAllowOn, autoAllowResume, autoAllowOff, autoAllowCheck`.
  - `config.Server`: add after `AIVisible`:
    ```go
    	AutoAllow        bool     `json:"autoAllow,omitempty"` // forever auto-allow; see hub/autoallow.go
    ```
    `ApplyServer` already builds `after` without it, so a save drops it; add to its doc comment: "AutoAllow is never carried over: every save turns auto-allow off."
  - `dialChanged`: ignore `AutoAllow` like `AIVisible`:
    ```go
    // dialChanged: every field but AIVisible, AutoAllow and Tunnels feeds the
    // dial config or the name the connection is registered under.
    func dialChanged(a, b config.Server) bool {
    	a.AIVisible, a.AutoAllow, a.Tunnels = b.AIVisible, b.AutoAllow, nil
    	b.Tunnels = nil
    	return !reflect.DeepEqual(a, b)
    }
    ```
  - `CreateVault`: in the loop, next to `s.AIVisible = false`, add `s.AutoAllow = false`, and extend the comment ("Kept servers lose aiVisible, autoAllow and their pins").
  - `changes`: add `{"autoAllow", a.AutoAllow, b.AutoAllow}` to the plain-field list.
  - `Hub.Exec`: record the wait:
    ```go
    	submitted := time.Now()
    	d, err := h.broker.Submit(ctx, req)
    	if err != nil {
    		return ExecResponse{}, err // ErrTooManyPending, or ctx.Err() when withdrawn
    	}
    	base.WaitMs = time.Since(submitted).Milliseconds()
    ```
- [ ] **Step 4: Run** `go test -race ./internal/broker ./internal/config ./internal/hub` — PASS. `go vet ./...` clean.
- [ ] **Step 5: Commit** `feat(hub): audit fields and vault flag for auto-allow; record approval wait`

### Task 2: Hub grants, `servers.setAutoAllow`, turning off, protocol 6

**Files:**
- Create: `internal/hub/autoallow.go`
- Modify: `internal/hub/hub.go` (`Hub` fields, `New`, `lockWithReason`, `setAutoSink`)
- Modify: `internal/hub/idle.go` (`ProtocolVersion = 6`, `lockIfIdle` ends grants)
- Modify: `internal/hub/hosts.go` (`SaveServer`, `DeleteServer`, `ForgetHostKey`)
- Modify: `internal/hub/uidoor.go` (`uiServer`, `serversForUI`, handler, sink)
- Modify: `desktop/src/shared/protocol.ts` (`PROTOCOL_VERSION = 6` only; the rest is Task 5), `desktop/test/fixtures/fakeHub.mjs` (protocol 6)
- Test: `internal/hub/autoallow_test.go`, `internal/hub/uidoor_test.go`

**Interfaces:**
- Consumes: Task 1's fields.
- Produces (used by Tasks 3–4):
  - `type grant struct { until time.Time; snap grantSnap; inflight map[uint64]context.CancelFunc }`, `type grantSnap struct{ target, hostKey, hostKeyAlgo, auth string }`, `func snapOf(dc sshx.DialConfig) grantSnap`
  - `Hub.grants map[string]*grant`, `Hub.grantSeq uint64` (both under `h.mu`)
  - `var autoModes = map[string]time.Duration{...}`, `const maxAutoInflight = 2`
  - `func autoRefusal(s config.Server) string`
  - `func (h *Hub) SetAutoAllow(name, mode string) error`
  - `func (h *Hub) endGrantLocked(name string) bool` (h.mu held; cancels in-flight runs)
  - `func (h *Hub) endAllGrantsLocked() []string`
  - `func (h *Hub) grantEnded(name, reason string)` (h.mu NOT held; audits `autoAllowOff`, then notifies `autoAllow.off`)
  - `func (h *Hub) notifyAuto(method string, params any)`; `func (h *Hub) setAutoSink(f func(method string, params any)) (release func())`
  - UI door: `servers.setAutoAllow {server, mode}` → `{}`

- [ ] **Step 1: Write failing tests** in `internal/hub/autoallow_test.go` (use `newHub` from `hub_test.go`; its vault has `vis` AI-visible and pinned, `nokey` unpinned, `hid` hidden; add servers to the file with a small helper that rewrites the store via `config.Update(path, testMK, ...)` then `h.Reload()` when a test needs `root`, su or sudo passwords — for the password cases set `EncSuPassword`/`EncSudoPassword` to any non-empty string, since only presence is checked):
  - `TestSetAutoAllowTimed`: `h.SetAutoAllow("vis", "15m")` → `h.grants["vis"]` exists with `until` ≈ now+15m (±5 s), zero-length `inflight`; audit has `{"kind":"config","action":"autoAllowOn","server":"vis","until":...}`; store flag stays false.
  - `TestSetAutoAllowForeverAndResume`: `SetAutoAllow("vis","forever")` → reloaded `File.FindServer("vis").AutoAllow == true`, grant with zero `until`, audit `autoAllowOn` with `forever:true`. Then `h.Lock(); unlockForTest(h)` → no grant (paused), flag still true. Then `SetAutoAllow("vis","forever")` again → grant armed, audit `autoAllowResume`, and the store's `Revision` did not change (no write).
  - `TestSetAutoAllowTimedClearsForeverFlag`: forever then `30m` → flag false, grant timed.
  - `TestSetAutoAllowRefusals`: table: locked (`h.Lock()` first) → `ErrLocked`; `nokey` → `ErrNoHostKey`; `hid` and `ghost` → errors with identical `Error()` text; `root` user, su password, sudo password → error containing `auto-allow refused: root login` / `has an su password` / `has a sudo password`; no grant created in every case.
  - `TestAutoAllowOff`: timed grant, then `SetAutoAllow("vis","off")` → no grant, audit `autoAllowOff` with `reason:"turned off"`; forever then off → flag false too. Off on a server with nothing on → nil error, no audit line.
  - `TestAutoAllowEndsOnLock`: grants on `vis` (timed) → `h.Lock()` → no grants, audit `autoAllowOff` `reason:"locked"`. Idle lock too: `h.lockIfIdle(0)`... use a hub whose `lastActivity` is old: set `h.lastActivity = time.Now().Add(-time.Hour)` under `h.mu`, grant forever, call `h.lockIfIdle(time.Minute)` → locked and grants empty (forever does not hold it off; the timed hold-off is Task 3).
  - `TestAutoAllowEndsOnServerWrites`: for each of `SaveServer("vis", <same fields, label unchanged>)`, `DeleteServer("vis")`, `ForgetHostKey("vis")`: a forever grant beforehand → afterwards no grant, flag false (or server gone), audit `autoAllowOff` with reasons `saved`, `deleted`, `server changed`.
  - `TestAutoAllowSink`: install `h.setAutoSink` capturing `(method, params)`; turning off sends `autoAllow.off` with `{"server":"vis","reason":"turned off"}` after the audit line is written (read the audit file inside the sink and assert the `autoAllowOff` line is already there).
  - `TestServersForUIAutoAllow`: timed → `uiServer.AutoAllow.Until` non-empty RFC 3339; forever armed → `Forever: true, Paused: false`; forever after lock+unlock → `Paused: true`; `hid` → `AutoAllowRefused == "not visible to AI"`; `nokey` → `"no pinned host key"`.
  - In `internal/hub/uidoor_test.go` (follow its existing in-process door helpers): `servers.setAutoAllow` with `{"server":"vis","mode":"15m"}` → `{}`; with an extra key, a duplicate key, or `"mode":"1h"` → error code -32602; and the `hello` result is `{"protocol":6}`.
- [ ] **Step 2: Run** `go test ./internal/hub -run 'AutoAllow|ServersForUIAutoAllow|Hello' -v` — FAIL.
- [ ] **Step 3: Implement `internal/hub/autoallow.go`:**

```go
package hub

import (
	"context"
	"fmt"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshx"
)

// Auto-allow (spec 2026-09-28-auto-allow-design.md): per-host grants that let
// plain AI exec skip the approval broker. Grants live in h.grants under h.mu;
// forever is also the vault flag config.Server.AutoAllow. Only SetAutoAllow
// arms a grant: a reload or an unlock never does.

// maxAutoInflight caps auto runs per host; more go to approval. Skipping the
// broker skips its cap of 5 pending, and one shared ssh.Client would run out
// of sshd MaxSessions and lock the human out of a terminal.
const maxAutoInflight = 2

var autoModes = map[string]time.Duration{
	"15m": 15 * time.Minute, "30m": 30 * time.Minute, "60m": time.Hour, "2h": 2 * time.Hour, "4h": 4 * time.Hour,
}

type grant struct {
	until    time.Time // zero: forever
	snap     grantSnap // the server as it was when the human enabled it
	inflight map[uint64]context.CancelFunc
}

// grantSnap is what an auto run must still match; any difference ends the grant.
type grantSnap struct{ target, hostKey, hostKeyAlgo, auth string }

func snapOf(dc sshx.DialConfig) grantSnap {
	return grantSnap{target: target(dc), hostKey: dc.HostKey, hostKeyAlgo: dc.HostKeyAlgo, auth: dc.Auth}
}

// autoRefusal says why s can never be on auto-allow, or "". A root login, or a
// stored su or sudo password, is root access: a command planted during a
// grant could capture a sudo password the next time the human approves a
// sudo-exec.
func autoRefusal(s config.Server) string {
	switch {
	case !s.AIVisible:
		return "not visible to AI"
	case s.HostKey == "":
		return "no pinned host key"
	case s.User == "root":
		return "root login"
	case s.EncSuPassword != "":
		return "has an su password"
	case s.EncSudoPassword != "":
		return "has a sudo password"
	}
	return ""
}

// SetAutoAllow turns name's auto-allow on (a timed mode, or forever) or off.
// forever on a server whose vault flag is already set only arms it (Resume).
func (h *Hub) SetAutoAllow(name, mode string) error {
	if mode == "off" {
		return h.autoAllowOff(name, "turned off")
	}
	d, timed := autoModes[mode]
	h.mu.Lock()
	if err := h.checkLocked(name); err != nil {
		h.mu.Unlock()
		return err
	}
	s, _ := h.deps.File.FindServer(name)
	if why := autoRefusal(s); why != "" {
		h.mu.Unlock()
		return fmt.Errorf("auto-allow refused: %s", why)
	}
	dc, err := h.resolveLocked(name)
	h.mu.Unlock()
	if err != nil {
		return ErrConnFailed
	}
	resume := !timed && s.AutoAllow
	if timed == s.AutoAllow { // timed over forever clears the flag; a new forever sets it
		if err := h.writeAutoAllow(name, !timed); err != nil {
			return err
		}
	}
	g := &grant{snap: snapOf(dc), inflight: map[uint64]context.CancelFunc{}}
	if timed {
		g.until = time.Now().Add(d)
	}
	h.mu.Lock()
	if old := h.grants[name]; old != nil {
		g.inflight = old.inflight // runs already going stay counted and cancellable
	}
	h.grants[name] = g
	h.mu.Unlock()
	r := broker.ConfigRecord{Action: "autoAllowOn", Server: name}
	switch {
	case resume:
		r.Action = "autoAllowResume"
	case timed:
		r.Until = g.until.UTC().Format(time.RFC3339)
	default:
		r.Forever = true
	}
	h.auditConfig(r)
	return nil
}

// writeAutoAllow sets name's vault flag and reloads.
func (h *Hub) writeAutoAllow(name string, on bool) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == name {
				f.Servers[i].AutoAllow = on
				return nil
			}
		}
		return serverNotFound(name)
	})
	if err != nil {
		return err
	}
	return h.Reload()
}

// autoAllowOff ends name's grant and clears its flag. The grant ends in
// memory even if the write fails; the error is returned.
func (h *Hub) autoAllowOff(name, reason string) error {
	h.mu.Lock()
	had := h.endGrantLocked(name)
	flag := false
	if h.deps.File != nil {
		s, ok := h.deps.File.FindServer(name)
		flag = ok && s.AutoAllow
	}
	h.mu.Unlock()
	var err error
	if flag {
		err = h.writeAutoAllow(name, false)
	}
	if had || flag {
		h.grantEnded(name, reason)
	}
	return err
}

// endGrantLocked drops name's grant and cancels its runs; h.mu is held. The
// caller calls grantEnded after unlocking when it returns true.
func (h *Hub) endGrantLocked(name string) bool {
	g := h.grants[name]
	if g == nil {
		return false
	}
	for _, cancel := range g.inflight {
		cancel()
	}
	delete(h.grants, name)
	return true
}

// endAllGrantsLocked drops every grant (a lock); h.mu is held.
func (h *Hub) endAllGrantsLocked() []string {
	var names []string
	for name := range h.grants {
		h.endGrantLocked(name)
		names = append(names, name)
	}
	return names
}

// grantEnded audits, then tells the app; h.mu is not held.
func (h *Hub) grantEnded(name, reason string) {
	h.auditConfig(broker.ConfigRecord{Action: "autoAllowOff", Server: name, Reason: reason})
	h.notifyAuto("autoAllow.off", map[string]string{"server": name, "reason": reason})
}

// dropGrant ends name's grant after a vault write that already cleared its
// flag (save, delete, forget); hadFlag says the flag was set before it.
func (h *Hub) dropGrant(name, reason string, hadFlag bool) {
	h.mu.Lock()
	had := h.endGrantLocked(name)
	h.mu.Unlock()
	if had || hadFlag {
		h.grantEnded(name, reason)
	}
}

func (h *Hub) notifyAuto(method string, params any) {
	h.mu.Lock()
	sink := h.autoSink
	h.mu.Unlock()
	if sink != nil {
		sink(method, params)
	}
}

// setAutoSink installs f for auto-allow notifications; release works like
// setEventSink's.
func (h *Hub) setAutoSink(f func(method string, params any)) (release func()) {
	h.mu.Lock()
	h.autoSink = f
	h.autoSinkGen++
	gen := h.autoSinkGen
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		if h.autoSinkGen == gen {
			h.autoSink = nil
		}
		h.mu.Unlock()
	}
}

// uiAutoAllow is a server's auto-allow state for the app.
type uiAutoAllow struct {
	Until   string `json:"until,omitempty"`
	Forever bool   `json:"forever,omitempty"`
	Paused  bool   `json:"paused,omitempty"`
}

// autoStateLocked is name's state for serversForUI; h.mu is held.
func (h *Hub) autoStateLocked(s config.Server, now time.Time) *uiAutoAllow {
	g := h.grants[s.Name]
	switch {
	case g != nil && !g.until.IsZero() && now.Before(g.until):
		return &uiAutoAllow{Until: g.until.UTC().Format(time.RFC3339)}
	case s.AutoAllow:
		return &uiAutoAllow{Forever: true, Paused: g == nil}
	}
	return nil
}
```

- [ ] **Step 4: Wire it in:**
  - `Hub` struct (`hub.go`): add after `running`:
    ```go
    	grants      map[string]*grant // auto-allow; see autoallow.go
    	grantSeq    uint64            // ids for grant.inflight
    	autoSink    func(method string, params any)
    	autoSinkGen uint64
    ```
    and extend the `mu` comment: `..., lastActivity, running, grants, grantSeq and autoSink`. In `New`: `grants: map[string]*grant{}`.
  - `lockWithReason`:
    ```go
    	h.mu.Lock()
    	sink := h.zeroKeyLocked()
    	ended := h.endAllGrantsLocked()
    	h.mu.Unlock()
    	if sink != nil {
    		sink(reason)
    	}
    	for _, n := range ended {
    		h.grantEnded(n, "locked")
    	}
    ```
    Same in `lockIfIdle` (idle.go): after `sink := h.zeroKeyLocked()` add `ended := h.endAllGrantsLocked()`, and after the sink call the same loop. Set `ProtocolVersion = 6`.
  - `SaveServer`: capture `hadFlag := before.AutoAllow` (after `config.Update`), then after the existing `denyPending(name)` add `h.dropGrant(name, "saved", hadFlag)` (`name` is `original` for an update, which is the rename case).
  - `DeleteServer`: in the Update fn, before removing, record `hadFlag = s.AutoAllow`; after the existing cleanup add `h.dropGrant(name, "deleted", hadFlag)`.
  - `ForgetHostKey`: in the Update fn also `hadFlag = f.Servers[i].AutoAllow; f.Servers[i].AutoAllow = false`; after the existing cleanup add `h.dropGrant(name, "server changed", hadFlag)`.
  - `uiServer` (uidoor.go): add
    ```go
    	AutoAllow        *uiAutoAllow `json:"autoAllow,omitempty"`
    	AutoAllowRefused string       `json:"autoAllowRefused,omitempty"`
    ```
    and in `serversForUI` set `AutoAllow: h.autoStateLocked(s, now), AutoAllowRefused: autoRefusal(s)` with `now := time.Now()` taken once before the loop.
  - `ServeUIDoor`: next to the lock sink:
    ```go
    	releaseAuto := h.setAutoSink(func(method string, params any) { s.Notify(method, params) })
    	defer releaseAuto()
    ```
    and register:
    ```go
    	req("servers.setAutoAllow", func(_ context.Context, raw json.RawMessage) (any, error) {
    		var p struct {
    			Server string `json:"server"`
    			Mode   string `json:"mode"`
    		}
    		if err := strictParams(raw, &p, "server", "mode"); err != nil {
    			return nil, err
    		}
    		if _, ok := autoModes[p.Mode]; !ok && p.Mode != "off" && p.Mode != "forever" {
    			return nil, &rpc.Error{Code: -32602, Message: "mode must be off, 15m, 30m, 60m, 2h, 4h, or forever"}
    		}
    		return empty, h.SetAutoAllow(p.Server, p.Mode)
    	})
    ```
  - Desktop: `PROTOCOL_VERSION = 6` in `desktop/src/shared/protocol.ts` and the protocol number in `desktop/test/fixtures/fakeHub.mjs` (grep for `5` next to `protocol`), so `hello` still matches.
- [ ] **Step 5: Run** `go test -race ./internal/hub` (twice) and `cd desktop && npm run typecheck && npm test` — PASS. `go vet ./...` clean.
- [ ] **Step 6: Commit** `feat(hub): per-host auto-allow grants and servers.setAutoAllow (protocol 6)`

### Task 3: The auto path in `Hub.Exec`, idle hold-off, expiry sweep

**Files:**
- Modify: `internal/hub/hub.go` (`Exec`: auto branch; extract `run`)
- Modify: `internal/hub/autoallow.go` (`autoStart`, `autoExec`, `sweepGrants`, `timedGrantLocked`, `cutBytes`)
- Modify: `internal/hub/idle.go` (`idleLoop`, `lockIfIdle`)
- Modify: `internal/hub/hub.go` `New` (idle loop always runs)
- Test: `internal/hub/autoallow_test.go`, `internal/hub/mcpdoor_test.go`, `cmd/sshgate/e2e_test.go`

**Interfaces:**
- Consumes: Task 2's `grant`, `snapOf`, `autoRefusal`, `endGrantLocked`, `grantEnded`, `notifyAuto`, `maxAutoInflight`.
- Produces: `func (h *Hub) run(ctx context.Context, name string, dc sshx.DialConfig, cmd string, sudo bool, timeout int, base broker.AuditRecord, red *config.Redactor) (ExecResponse, error)`; `func (h *Hub) sweepGrants(now time.Time)`; notification `autoAllow.ran {server, command, truncated?, description, exitCode?, error?, time}`.

- [ ] **Step 1: Write failing tests** (`internal/hub/autoallow_test.go`; `fakeExec` from `hub_test.go` records calls; add a `blockExec` type whose `Exec` blocks until its ctx is done and then returns `sshx.ErrCancelled`, or until a release channel closes):
  - `TestAutoExecRunsWithoutApproval`: `SetAutoAllow("vis","15m")`; `h.Exec(ctx, ExecRequest{Server:"vis", Command:"echo hi", Description:"d", Client:"t"})` returns without any pending request ever appearing (assert `len(h.broker.Pending()) == 0` throughout by running Exec synchronously) and `fe.calls == ["echo hi"]`; the last audit line has `"outcome":"allowed","approval":"auto"` and no `waitMs`; a sink installed with `setAutoSink` got `autoAllow.ran` with `server:"vis"`, `command:"echo hi"`, `exitCode:0`, after the audit line was written.
  - `TestAutoExecSudoStillAsks`: grant on `vis`; `Exec` with `Sudo:true` in a goroutine → `waitPending(t, h.broker, 1)`; deny it.
  - `TestAutoExecExpired`: grant, then under `h.mu` set `h.grants["vis"].until = time.Now().Add(-time.Second)`; `Exec` goes pending (goroutine + `waitPending`); the grant is gone; audit has `autoAllowOff` `reason:"expired"`.
  - `TestAutoExecSnapshotMismatch`: grant on `vis`; rewrite the store with `config.Update` changing `vis`'s `Port` to 23 (the pin stays: write `HostKey` explicitly too), `h.Reload()`; `Exec` goes pending; grant gone; `autoAllowOff reason:"server changed"`. Repeat with delete + re-add of `vis` with identical fields and a different `Auth` (`"password"`): mismatch. Repeat with only `EncSuPassword` set: mismatch.
  - `TestAutoExecForeverFlagClearedOutside`: forever grant; clear the flag with a direct `config.Update` + `Reload`; `Exec` goes pending; grant gone.
  - `TestAutoExecCap`: `blockExec`; grant; start two `Exec`s in goroutines; wait until `len(h.grants["vis"].inflight) == 2` (under `h.mu`); a third `Exec` goes pending; release.
  - `TestAutoExecCancelledOnLock`: `blockExec`; grant; `Exec` in a goroutine; wait for 1 in flight; `h.Lock()`; the Exec returns `ErrCancelledRunning`; its audit line has `"outcome":"cancelled_running","approval":"auto"`.
  - `TestAutoExecErrorKeepsApproval`: `fakeExec{err: errors.New("boom")}`; grant; `Exec` errors; audit `"outcome":"error","approval":"auto"`; the `autoAllow.ran` notification has `error` equal to the error text the AI got.
  - `TestAutoRunsDoNotHoldIdleLock`: `blockExec`; forever grant; `Exec` in a goroutine; wait in flight; set `lastActivity` an hour back; `h.lockIfIdle(time.Minute)` → locked (auto runs are not in `running`), and the run was cancelled.
  - `TestTimedGrantHoldsIdleLock`: timed grant (15m); `lastActivity` an hour back; `lockIfIdle(time.Minute)` → still unlocked; set `until` to the past; `lockIfIdle` → locked.
  - `TestSweepGrantsCancelsAtDeadline`: `blockExec`; timed grant; `Exec` in flight; set `until` past; `h.sweepGrants(time.Now())` → run cancelled, grant gone, `autoAllowOff expired`.
  - `TestAutoCommandCut`: grant; a 5000-byte command (ASCII `echo ` + `a`×4995) → `autoAllow.ran` `command` has at most 1000 bytes and `truncated` = the rest; a command whose byte 1000 falls inside a multi-byte rune is cut before that rune (valid UTF-8).
  - `TestCLIModeNeverArms`: a vault with `vis` flagged `AutoAllow: true` (write it in the store before `New`); `unlockForTest`; `Exec` goes pending (the hub never arms from the flag).
  - `internal/hub/mcpdoor_test.go` `TestListServersHidesAutoAllow`: with a forever grant on `vis`, the raw JSON of the MCP door's `listServers` result contains no `autoAllow` / `AutoAllow` substring.
  - `cmd/sshgate/e2e_test.go` `TestEndToEndAutoAllow`: reuse `TestEndToEndApprovalFlow`'s setup (bridge → hub → in-process sshtest); call `h.SetAutoAllow(<server>, "15m")` on the hub before the bridge call; the tool call returns its output with no decision made (no goroutine approves).
- [ ] **Step 2: Run** `go test ./internal/hub ./cmd/sshgate -run 'Auto|Sweep|IdleLock|CLIModeNeverArms|HidesAutoAllow' -v` — FAIL.
- [ ] **Step 3: Extract `run` from `Exec`** (no behaviour change for the approval path): move the tail of `Exec` from `ex := h.executor(r.Server, dc2)` to the end into

```go
// run executes cmd on name with dc, audits base (Outcome, exit code, sizes,
// reason), and returns what the AI sees. It is the tail of both the approved
// and the auto-allowed path.
func (h *Hub) run(ctx context.Context, name string, dc sshx.DialConfig, cmd string, sudo bool, timeout int, base broker.AuditRecord, red *config.Redactor) (ExecResponse, error) {
	ex := h.executor(name, dc)
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	// ... the existing body unchanged, using name for r.Server and sudo for r.Sudo ...
}
```

  and end `Exec`'s approved branch with `return h.run(ctx, r.Server, dc2, cmd, r.Sudo, timeout, base, red)`.
- [ ] **Step 4: Add the auto branch** in `Exec`, right before `submitted := time.Now()`:

```go
	if !r.Sudo {
		if ar := h.autoStart(ctx, r.Server); ar != nil {
			defer ar.done()
			return h.autoExec(ar, r, cmd, timeout, base)
		}
	}
```

  and in `autoallow.go`:

```go
type autoRun struct {
	dc   sshx.DialConfig
	ctx  context.Context // cancelled when the grant ends
	done func()
}

// autoStart decides, in one h.mu section, whether name's exec runs under its
// grant, and registers the run. It resolves the server itself and compares
// it with the grant's snapshot, so the run uses exactly what it checked; any
// mismatch ends the grant and the request goes to approval. nil: approval.
func (h *Hub) autoStart(ctx context.Context, name string) *autoRun {
	h.mu.Lock()
	g := h.grants[name]
	if g == nil || h.checkLocked(name) != nil {
		h.mu.Unlock()
		return nil
	}
	reason := ""
	s, _ := h.deps.File.FindServer(name)
	dc, err := h.resolveLocked(name)
	switch {
	case !g.until.IsZero() && !time.Now().Before(g.until):
		reason = "expired"
	case err != nil, autoRefusal(s) != "", snapOf(dc) != g.snap, g.until.IsZero() && !s.AutoAllow:
		reason = "server changed"
	case len(g.inflight) >= maxAutoInflight:
		h.mu.Unlock()
		return nil
	}
	if reason != "" {
		h.endGrantLocked(name)
		h.mu.Unlock()
		h.grantEnded(name, reason)
		return nil
	}
	h.grantSeq++
	id := h.grantSeq
	rctx, cancel := context.WithCancel(ctx)
	g.inflight[id] = cancel
	h.mu.Unlock()
	return &autoRun{dc: dc, ctx: rctx, done: func() {
		h.mu.Lock()
		delete(g.inflight, id)
		h.mu.Unlock()
		cancel()
	}}
}

// autoCmdCap bounds the command text sent to the app's feed.
const autoCmdCap = 1000

// autoExec runs an auto-allowed exec: not counted in h.running (so it never
// holds off the idle lock), audited with approval "auto", then reported to
// the app.
func (h *Hub) autoExec(ar *autoRun, r ExecRequest, cmd string, timeout int, base broker.AuditRecord) (ExecResponse, error) {
	red := redactorFor(ar.dc)
	base.Approval = "auto"
	base.Command, base.Description = red.Redact(cmd), red.Redact(r.Description)
	resp, err := h.run(ar.ctx, r.Server, ar.dc, cmd, false, timeout, base, red)
	shown, cut := cutBytes(base.Command, autoCmdCap)
	ran := map[string]any{"server": r.Server, "command": shown, "description": base.Description,
		"time": time.Now().UTC().Format(time.RFC3339)}
	if cut > 0 {
		ran["truncated"] = cut
	}
	if err != nil {
		ran["error"] = err.Error()
	} else {
		ran["exitCode"] = resp.ExitCode
	}
	h.notifyAuto("autoAllow.ran", ran)
	return resp, err
}

// cutBytes cuts s to at most n bytes on a rune boundary; cut is how many
// bytes were dropped.
func cutBytes(s string, n int) (string, int) {
	if len(s) <= n {
		return s, 0
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], len(s) - n
}

// sweepGrants ends timed grants past their deadline, cancelling their runs.
// The idle loop calls it every tick; Exec also checks the deadline exactly.
func (h *Hub) sweepGrants(now time.Time) {
	h.mu.Lock()
	var ended []string
	for name, g := range h.grants {
		if !g.until.IsZero() && !now.Before(g.until) {
			h.endGrantLocked(name)
			ended = append(ended, name)
		}
	}
	h.mu.Unlock()
	for _, n := range ended {
		h.grantEnded(n, "expired")
	}
}

// timedGrantLocked reports a timed grant still before its deadline; h.mu is
// held. Such a grant holds off the idle lock: its deadline was fixed by the
// human, and nothing the AI does moves it.
func (h *Hub) timedGrantLocked(now time.Time) bool {
	for _, g := range h.grants {
		if !g.until.IsZero() && now.Before(g.until) {
			return true
		}
	}
	return false
}
```

  Add `"unicode/utf8"` to the imports.
- [ ] **Step 5: Idle loop:**
  - `lockIfIdle`: the condition becomes `if time.Since(h.lastActivity) < idle || h.running > 0 || h.timedGrantLocked(time.Now()) {` and update its comment ("…, no timed auto-allow grant is before its deadline (auto runs themselves never count), …").
  - `idleLoop`: call `h.sweepGrants(time.Now())` on every tick before `lockIfIdle`; when `idle <= 0` (auto-lock disabled), tick every minute and only sweep:
    ```go
    func (h *Hub) idleLoop(idle time.Duration) {
    	tick := time.Minute
    	if idle > 0 {
    		tick = max(idle/15, time.Second)
    		tick = min(tick, idle)
    	}
    	t := time.NewTicker(tick)
    	defer t.Stop()
    	for {
    		select {
    		case <-h.done:
    			return
    		case <-t.C:
    			h.sweepGrants(time.Now())
    			if idle > 0 {
    				h.lockIfIdle(idle)
    			}
    		}
    	}
    }
    ```
  - `New`: start `go h.idleLoop(idle)` unconditionally (remove the `if idle > 0`), keeping `idle = defaultIdleLock` when 0 and passing a negative value through.
- [ ] **Step 6: Run** `go test -race ./internal/hub ./cmd/sshgate` (twice) — PASS. `go vet ./...` clean.
- [ ] **Step 7: Commit** `feat(hub): run plain exec under an auto-allow grant; grants hold the idle lock only until their deadline`

### Task 4: `servers.autoAllowCheck`

**Files:**
- Modify: `internal/hub/autoallow.go` (`AutoAllowCheck`)
- Modify: `internal/hub/uidoor.go` (handler)
- Test: `internal/hub/autoallow_test.go`, `internal/hub/uidoor_test.go`

**Interfaces:**
- Consumes: `autoRefusal`, `checkLocked`, `resolveLocked`, `executor`.
- Produces: `func (h *Hub) AutoAllowCheck(ctx context.Context, name string) (AutoCheck, error)`, `type AutoCheck struct { UID int `json:"uid"`; PasswordlessSudo bool `json:"passwordlessSudo"` }`; UI door `servers.autoAllowCheck {server}` → `AutoCheck`.

- [ ] **Step 1: Write failing tests:**
  - `TestAutoAllowCheck`: `fakeExec{res: sshx.ExecResult{Stdout: "1000\nnopasswd\n"}}` → `{UID:1000, PasswordlessSudo:true}` and `fe.calls[0] == autoProbe`; `"0\n"` → `{0,false}`; `"garbage"` → error `could not check this host`; `fakeExec{err: ...}` → same error; audit line `{"action":"autoAllowCheck","server":"vis","reason":"uid 1000, passwordless sudo true"}` for the success case.
  - Refusals run no command: `hid`/`ghost` identical errors, `nokey` `ErrNoHostKey`, locked `ErrLocked`, root/su/sudo-password hosts → `auto-allow refused: ...`; `fe.calls` stays empty.
  - `uidoor_test.go`: `servers.autoAllowCheck` with `{"server":"vis"}` returns `{"uid":...,"passwordlessSudo":...}`; an extra key → -32602.
- [ ] **Step 2: Run** `go test ./internal/hub -run 'AutoAllowCheck' -v` — FAIL.
- [ ] **Step 3: Implement:**

```go
// autoProbe is the fixed command autoAllowCheck runs: the account's uid, and
// "nopasswd" if sudo works without a password. Advice for the human only.
const autoProbe = "id -u; sudo -n true 2>/dev/null && echo nopasswd"

type AutoCheck struct {
	UID              int  `json:"uid"`
	PasswordlessSudo bool `json:"passwordlessSudo"`
}

var errAutoCheck = errors.New("could not check this host")

// AutoAllowCheck runs autoProbe on name so the app can warn that auto-allow
// there is root access. It checks what SetAutoAllow checks first.
func (h *Hub) AutoAllowCheck(ctx context.Context, name string) (AutoCheck, error) {
	h.mu.Lock()
	if err := h.checkLocked(name); err != nil {
		h.mu.Unlock()
		return AutoCheck{}, err
	}
	s, _ := h.deps.File.FindServer(name)
	if why := autoRefusal(s); why != "" {
		h.mu.Unlock()
		return AutoCheck{}, fmt.Errorf("auto-allow refused: %s", why)
	}
	dc, err := h.resolveLocked(name)
	h.mu.Unlock()
	if err != nil {
		return AutoCheck{}, errAutoCheck
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := h.executor(name, dc).Exec(cctx, autoProbe)
	lines := strings.Fields(res.Stdout)
	if err != nil || len(lines) == 0 {
		if err != nil {
			fmt.Fprintf(os.Stderr, "hub: autoAllowCheck %q: %v\n", name, err)
		}
		return AutoCheck{}, errAutoCheck
	}
	uid, err := strconv.Atoi(lines[0])
	if err != nil {
		return AutoCheck{}, errAutoCheck
	}
	c := AutoCheck{UID: uid, PasswordlessSudo: slices.Contains(lines[1:], "nopasswd")}
	h.auditConfig(broker.ConfigRecord{Action: "autoAllowCheck", Server: name,
		Reason: fmt.Sprintf("uid %d, passwordless sudo %v", c.UID, c.PasswordlessSudo)})
	return c, nil
}
```

  Handler in `ServeUIDoor`:

```go
	req("servers.autoAllowCheck", func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Server string `json:"server"`
		}
		if err := strictParams(raw, &p, "server"); err != nil {
			return nil, err
		}
		return h.AutoAllowCheck(ctx, p.Server)
	})
```
- [ ] **Step 4: Run** `go test -race ./internal/hub` — PASS. `go vet ./...` clean.
- [ ] **Step 5: Commit** `feat(hub): servers.autoAllowCheck probes for root access`

### Task 5: Desktop protocol, transport, and auto-allow logic

**Files:**
- Modify: `desktop/src/shared/protocol.ts`, `desktop/src/renderer/transport.ts`
- Create: `desktop/src/renderer/autoallow.ts`
- Test: `desktop/test/autoallow.test.ts`, `desktop/test/transport.test.ts` (method list), `desktop/test/ipc.test.ts` if it pins `REQUEST_METHODS`

**Interfaces:**
- Produces:
  - `protocol.ts`: `type AutoAllowMode = 'off' | '15m' | '30m' | '60m' | '2h' | '4h' | 'forever'`; `interface AutoAllowState { until?: string; forever?: boolean; paused?: boolean }`; `ServerInfo.autoAllow?: AutoAllowState`, `ServerInfo.autoAllowRefused?: string`; `interface AutoAllowRan { server: string; command: string; truncated?: number; description: string; exitCode?: number; error?: string; time: string }`; `interface AutoAllowCheck { uid: number; passwordlessSudo: boolean }`; `HubEvent` gains `{ method: 'autoAllow.ran'; params: AutoAllowRan }` and `{ method: 'autoAllow.off'; params: { server: string; reason: string } }`; `REQUEST_METHODS` gains `'servers.setAutoAllow', 'servers.autoAllowCheck'`.
  - `transport.ts` `hub.setAutoAllow(server: string, mode: AutoAllowMode): Promise<void>`, `hub.autoAllowCheck(server: string): Promise<AutoAllowCheck>`.
  - `autoallow.ts`: `AUTO_MODES`, `chipLabel`, `isActive`, `autoHosts`, `pausedHosts`, `dropOnLock`, `applyOff`, `FEED_CAP`, `pushFeed`, `commandLabel`, `enableAllowed` (signatures below).

- [ ] **Step 1: Write failing tests** `desktop/test/autoallow.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import type { AutoAllowRan, ServerInfo } from '../src/shared/protocol'
import { applyOff, autoHosts, chipLabel, commandLabel, dropOnLock, enableAllowed, FEED_CAP, isActive, pausedHosts, pushFeed } from '../src/renderer/autoallow'

const now = Date.parse('2026-09-28T10:00:00Z')
const at = (min: number) => new Date(now + min * 60_000).toISOString()
const srv = (name: string, autoAllow?: ServerInfo['autoAllow']) => ({ name, autoAllow }) as ServerInfo

describe('chipLabel', () => {
  it('counts down a timed grant in whole minutes, rounding up', () => {
    expect(chipLabel({ until: at(14.2) }, now)).toBe('Auto 15m')
    expect(chipLabel({ until: at(0.1) }, now)).toBe('Auto 1m')
    expect(chipLabel({ until: at(125) }, now)).toBe('Auto 2h 5m')
    expect(chipLabel({ until: at(120) }, now)).toBe('Auto 2h')
  })
  it('is gone once the deadline passes', () => expect(chipLabel({ until: at(-1) }, now)).toBeUndefined())
  it('shows forever and paused', () => {
    expect(chipLabel({ forever: true }, now)).toBe('Auto ∞')
    expect(chipLabel({ forever: true, paused: true }, now)).toBe('Auto paused')
    expect(chipLabel(undefined, now)).toBeUndefined()
  })
})

describe('host sets', () => {
  const list = [srv('a', { until: at(5) }), srv('b', { forever: true }), srv('c', { forever: true, paused: true }), srv('d', { until: at(-1) }), srv('e')]
  it('isActive: timed before its deadline or armed forever', () => {
    expect(list.map((s) => isActive(s.autoAllow, now))).toEqual([true, true, false, false, false])
  })
  it('autoHosts has active and paused hosts', () => expect([...autoHosts(list, now)].sort()).toEqual(['a', 'b', 'c']))
  it('pausedHosts', () => expect(pausedHosts(list)).toEqual(['c']))
  it('dropOnLock drops timed grants and pauses forever', () => {
    expect(dropOnLock(list).map((s) => s.autoAllow)).toEqual([undefined, { forever: true, paused: true }, { forever: true, paused: true }, undefined, undefined])
  })
  it('applyOff clears one host', () => expect(applyOff(list, 'b').find((s) => s.name === 'b')!.autoAllow).toBeUndefined())
})

describe('feed', () => {
  const ran = (i: number): AutoAllowRan => ({ server: 'a', command: `c${i}`, description: '', exitCode: 0, time: at(i) })
  it('is newest first and capped', () => {
    let feed: AutoAllowRan[] = []
    for (let i = 0; i < FEED_CAP + 5; i++) feed = pushFeed(feed, ran(i))
    expect(feed).toHaveLength(FEED_CAP)
    expect(feed[0].command).toBe(`c${FEED_CAP + 4}`)
  })
  it('labels a cut command', () => {
    expect(commandLabel({ ...ran(1), truncated: 42 })).toBe('c1 … (truncated 42 bytes)')
    expect(commandLabel(ran(1))).toBe('c1')
  })
})

describe('enableAllowed', () => {
  const base = { mode: '15m' as const, typed: '', host: 'box', refused: undefined, openedAt: now, changedAt: now, now: now + 600 }
  it('waits 500 ms after opening or any change', () => {
    expect(enableAllowed(base)).toBe(true)
    expect(enableAllowed({ ...base, now: now + 499 })).toBe(false)
    expect(enableAllowed({ ...base, changedAt: now + 200 })).toBe(false)
  })
  it('forever needs the host name typed exactly', () => {
    expect(enableAllowed({ ...base, mode: 'forever' })).toBe(false)
    expect(enableAllowed({ ...base, mode: 'forever', typed: 'box' })).toBe(true)
    expect(enableAllowed({ ...base, mode: 'forever', typed: 'Box' })).toBe(false)
  })
  it('never for a refused host', () => expect(enableAllowed({ ...base, refused: 'root login' })).toBe(false))
})
```

  Also add the two new methods to whatever `REQUEST_METHODS` expectation `transport.test.ts`/`ipc.test.ts` pin (grep for `'tunnels.start'` in `desktop/test`).
- [ ] **Step 2: Run** `cd desktop && npx vitest run test/autoallow.test.ts` — FAIL (module missing).
- [ ] **Step 3: Implement** the protocol additions listed under Interfaces, the two transport methods next to `tunnelsStart`:

```ts
  setAutoAllow: async (server: string, mode: AutoAllowMode) => { await call('servers.setAutoAllow', { server, mode }) },
  autoAllowCheck: (server: string) => call<AutoAllowCheck>('servers.autoAllowCheck', { server }),
```

  and `desktop/src/renderer/autoallow.ts`:

```ts
import type { AutoAllowMode, AutoAllowRan, AutoAllowState, ServerInfo } from '../shared/protocol'
import { ALLOW_DELAY_MS } from './approvals'

// Per-host auto-allow (spec 2026-09-28-auto-allow-design.md). Everything here
// is pure: the renderer never asks the hub on a timer or on an auto-allow event.

export const AUTO_MODES: { mode: Exclude<AutoAllowMode, 'off'>; label: string }[] = [
  { mode: '15m', label: '15 min' }, { mode: '30m', label: '30 min' }, { mode: '60m', label: '60 min' },
  { mode: '2h', label: '2 h' }, { mode: '4h', label: '4 h' }, { mode: 'forever', label: 'Until turned off' },
]

const left = (a: AutoAllowState | undefined, now: number) => (a?.until ? Date.parse(a.until) - now : 0)

export function chipLabel(a: AutoAllowState | undefined, now: number): string | undefined {
  if (a?.forever) return a.paused ? 'Auto paused' : 'Auto ∞'
  const ms = left(a, now)
  if (ms <= 0) return undefined
  const min = Math.ceil(ms / 60_000)
  if (min < 60) return `Auto ${min}m`
  const h = Math.floor(min / 60), m = min % 60
  return m ? `Auto ${h}h ${m}m` : `Auto ${h}h`
}

export const isActive = (a: AutoAllowState | undefined, now: number) => !!(a?.forever ? !a.paused : left(a, now) > 0)

// Hosts whose auto-allow is on or paused: they get the tab dot and count in "auto N".
export const autoHosts = (servers: ServerInfo[], now: number) =>
  new Set(servers.filter((s) => isActive(s.autoAllow, now) || s.autoAllow?.paused).map((s) => s.name))

export const pausedHosts = (servers: ServerInfo[]) => servers.filter((s) => s.autoAllow?.paused).map((s) => s.name)

// On the locked notification: timed grants are gone; forever ones wait for Resume.
export const dropOnLock = (servers: ServerInfo[]): ServerInfo[] =>
  servers.map((s) => ({ ...s, autoAllow: s.autoAllow?.forever ? { forever: true, paused: true } : undefined }))

export const applyOff = (servers: ServerInfo[], server: string): ServerInfo[] =>
  servers.map((s) => (s.name === server ? { ...s, autoAllow: undefined } : s))

export const FEED_CAP = 50
export const pushFeed = (feed: AutoAllowRan[], r: AutoAllowRan) => [r, ...feed].slice(0, FEED_CAP)

export const commandLabel = (r: AutoAllowRan) => (r.truncated ? `${r.command} … (truncated ${r.truncated} bytes)` : r.command)

// Enable is mouse-only and waits ALLOW_DELAY_MS after the dialog opens or its
// content changes; forever also needs the host name typed exactly.
export function enableAllowed(o: { mode: Exclude<AutoAllowMode, 'off'>; typed: string; host: string; refused?: string; openedAt: number; changedAt: number; now: number }): boolean {
  if (o.refused) return false
  if (o.mode === 'forever' && o.typed !== o.host) return false
  return o.now - Math.max(o.openedAt, o.changedAt) >= ALLOW_DELAY_MS
}
```
- [ ] **Step 4: Run** `cd desktop && npm run typecheck && npm test` — PASS.
- [ ] **Step 5: Commit** `feat(desktop): auto-allow protocol, transport, and pure logic`

### Task 6: Desktop UI

**Files:**
- Create: `desktop/src/renderer/AutoAllowDialog.tsx`, `desktop/src/renderer/AutoAllowPaused.tsx`
- Modify: `desktop/src/renderer/HostList.tsx`, `desktop/src/renderer/App.tsx`, `desktop/src/renderer/ApprovalPanel.tsx`, `desktop/src/renderer/TerminalTabs.tsx`, `desktop/src/renderer/icons.tsx`, `desktop/src/renderer/styles.css`
- Test: `desktop/test/AutoAllowDialog.test.ts` (render with `renderToStaticMarkup`, as `ApprovalPanel.test.ts` does)

**Interfaces:**
- Consumes: Task 5's `autoallow.ts` and transport methods; `blockKeyboardActivation`, `highlightNonAscii` from `approvals.ts`; `summary` from `tunnels.ts`.
- Produces: `AutoAllowDialog({ server, remoteTunnels, check, onEnable, onCancel })`, `AutoAllowPaused({ hosts, onResume, onStop })`.

- [ ] **Step 1: Write failing tests** `desktop/test/AutoAllowDialog.test.ts` with `renderToStaticMarkup`:
  - A refused host (`autoAllowRefused: 'root login'`) renders the text `Auto-allow is not available here: root login` and the Enable button has `disabled`.
  - A normal host renders the six duration radios with the labels from `AUTO_MODES`, the prompt-injection sentence ("including commands planted by what it reads"), "sudo-exec still asks", and Enable with `tabindex="-1"` and `disabled` (the 500 ms delay starts at mount).
  - Remote tunnels passed in render "Remote tunnels running on this host reach your machine" and each summary.
- [ ] **Step 2: Run** `cd desktop && npx vitest run test/AutoAllowDialog.test.ts` — FAIL.
- [ ] **Step 3: `AutoAllowDialog.tsx`** (follow `HostKeyDialog.tsx` for the modal markup, `role="dialog"`, `aria-modal`, Cancel autofocus):

```tsx
import { useEffect, useState } from 'react'
import type { AutoAllowCheck, AutoAllowMode, ServerInfo } from '../shared/protocol'
import { blockKeyboardActivation } from './approvals'
import { AUTO_MODES, enableAllowed } from './autoallow'

type Mode = Exclude<AutoAllowMode, 'off'>

export function AutoAllowDialog({ server, remoteTunnels, check, onEnable, onCancel }: {
  server: ServerInfo; remoteTunnels: string[]
  check: () => Promise<AutoAllowCheck>
  onEnable: (mode: Mode) => Promise<void>; onCancel: () => void
}) {
  const [mode, setMode] = useState<Mode>('15m')
  const [typed, setTyped] = useState('')
  const [checked, setChecked] = useState<AutoAllowCheck | 'error'>()
  const [openedAt] = useState(Date.now())
  const [changedAt, setChangedAt] = useState(openedAt)
  const [now, setNow] = useState(openedAt)
  const [error, setError] = useState<string>()
  const refused = server.autoAllowRefused
  useEffect(() => { if (!refused) check().then(setChecked, () => setChecked('error')) }, [])
  // Content that moves under the cursor restarts the Enable delay.
  useEffect(() => { setChangedAt(Date.now()) }, [mode, checked])
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 100); return () => clearInterval(t) }, [])
  const root = checked !== undefined && checked !== 'error' && (checked.uid === 0 || checked.passwordlessSudo)
  const ok = enableAllowed({ mode, typed, host: server.name, refused, openedAt, changedAt, now })
  const enable = () => { if (ok) onEnable(mode).catch((e) => setError((e as Error).message)) }
  return (
    <div className="modal-backdrop">
      <div className="modal autoallow" role="dialog" aria-modal="true" aria-labelledby="aa-title">
        <h3 id="aa-title">{`Auto-allow AI commands on ${server.name}`}</h3>
        {refused ? (
          <p className="error">{`Auto-allow is not available here: ${refused}`}</p>
        ) : (
          <>
            <p>{`The AI runs any command this account can run on ${server.name} without asking, including commands planted by what it reads (prompt injection), and can leave things that run later (cron jobs, SSH keys, shell startup files). sudo-exec still asks.`}</p>
            {root && <p className="error">This is root access: the AI can do anything on this host.</p>}
            {checked === 'error' && <p className="muted">Could not check this host's sudo access.</p>}
            {remoteTunnels.length > 0 && (
              <div className="warn"><p>Remote tunnels running on this host reach your machine:</p>
                <ul>{remoteTunnels.map((t) => <li key={t} className="mono">{t}</li>)}</ul></div>
            )}
            <fieldset>
              <legend>For how long</legend>
              {AUTO_MODES.map((m) => (
                <label key={m.mode}><input type="radio" name="aa-mode" checked={mode === m.mode} onChange={() => setMode(m.mode)} />{m.label}</label>
              ))}
            </fieldset>
            <p className="muted">{mode === 'forever'
              ? 'Stays set after restart. After each unlock it waits for you to click Resume.'
              : 'Ends at its time, when you stop it, or when you lock the vault. The vault will not auto-lock before then.'}</p>
            {mode === 'forever' && (
              <label>{`Type ${server.name} to confirm`}<input value={typed} onChange={(e) => setTyped(e.target.value)} /></label>
            )}
          </>
        )}
        {error && <p className="error">{error}</p>}
        <div className="modal-actions">
          <button type="button" className="btn" autoFocus onClick={onCancel}>Cancel</button>
          <button type="button" className="btn danger" tabIndex={-1} disabled={!ok} onKeyDown={blockKeyboardActivation} onClick={enable}>Enable</button>
        </div>
      </div>
    </div>
  )
}
```

  Use the modal/backdrop/button class names that `HostKeyDialog.tsx` and `FileDialogs.tsx` already use (read them; if they differ from the placeholders above, follow theirs).
- [ ] **Step 4: `AutoAllowPaused.tsx`:**

```tsx
import { useEffect, useState } from 'react'
import { ALLOW_DELAY_MS, blockKeyboardActivation } from './approvals'

// Shown after an unlock while forever hosts wait for Resume (spec: Desktop).
export function AutoAllowPaused({ hosts, onResume, onStop }: { hosts: string[]; onResume: () => void; onStop: () => void }) {
  const key = hosts.join('\n')
  const [since, setSince] = useState(Date.now())
  const [now, setNow] = useState(Date.now())
  useEffect(() => { setSince(Date.now()) }, [key])
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 100); return () => clearInterval(t) }, [])
  if (hosts.length === 0) return null
  return (
    <div className="banner auto" role="status">
      <span>{`Auto-allow is paused on ${hosts.join(', ')}.`}</span>
      <button type="button" className="btn" tabIndex={-1} disabled={now - since < ALLOW_DELAY_MS} onKeyDown={blockKeyboardActivation} onClick={onResume}>Resume</button>
      <button type="button" className="btn" onClick={onStop}>Stop</button>
    </div>
  )
}
```
- [ ] **Step 5: Host card (`HostList.tsx`):** new props `now: number`, `onAutoAllow: (name: string) => void`, `onStopAutoAllow: (name: string) => void`, `paused: string[]`, `onResume`, `onStopPaused`. Inside `.hostcard-name` add `{chipLabel(s.autoAllow, now) && <span className="chip auto">{chipLabel(s.autoAllow, now)}</span>}`. In `.hostcard-actions`, before Edit:

```tsx
{isActive(s.autoAllow, now) || s.autoAllow?.paused
  ? <button type="button" className="icon" aria-label={`Stop auto-allow ${s.name}`} title="Stop auto-allow" onClick={() => onStopAutoAllow(s.name)}><StopIcon /></button>
  : <button type="button" className="icon" aria-label={`Auto-allow ${s.name}`} title="Auto-allow" onClick={() => onAutoAllow(s.name)}><BoltIcon /></button>}
```

  Render `<AutoAllowPaused hosts={paused} onResume={onResume} onStop={onStopPaused} />` above the host grid. Add `BoltIcon` and `StopIcon` to `icons.tsx` in the same style as the existing icons (16×16 `currentColor` SVG paths, `aria-hidden`).
- [ ] **Step 6: App wiring (`App.tsx`):**
  - State: `const [autoFeed, setAutoFeed] = useState<AutoAllowRan[]>([])`, `const [autoDialog, setAutoDialog] = useState<string>()`, `const [now, setNow] = useState(Date.now())`.
  - A 15 s interval that only calls `setNow(Date.now())` while any server has `autoAllow?.until` (a renderer clock, no hub call).
  - In the existing `hub.onEvent` handler that already reacts to `locked`: also `setServers(dropOnLock)`. Add a handler: `autoAllow.off` → `setServers((cur) => applyOff(cur, e.params.server))`; `autoAllow.ran` → `setAutoFeed((f) => pushFeed(f, e.params))`. Neither calls the hub.
  - `useEffect(() => { if (hubState.kind !== 'running') setAutoFeed([]) }, [hubState.kind])`.
  - Handlers (user actions, so hub calls are fine): `enableAuto(name, mode)` = `await hub.setAutoAllow(name, mode); setAutoDialog(undefined); await reloadServers()`; `stopAuto(name)` = `await hub.setAutoAllow(name, 'off'); await reloadServers()`; `resumeAll()` = for each `pausedHosts(servers)` `await hub.setAutoAllow(n, 'forever')`, then `reloadServers()`; `stopAll()` = for each of `autoHosts(servers, now)` `await hub.setAutoAllow(n, 'off')`, then `reloadServers()`.
  - Render `<AutoAllowDialog>` when `autoDialog` is set, with `server={servers.find(...)}`, `remoteTunnels={tunnels.filter((t) => t.server === autoDialog && t.kind === 'remote' && t.status === 'running').map(summary)}`, `check={() => hub.autoAllowCheck(autoDialog)}`.
  - AI button: show while `aiOpen || items.length > 0 || autoN > 0` where `autoN = autoHosts(servers, now).size`; inside it, after the existing count, `{autoN > 0 && <span className="count auto">{`auto ${autoN}`}</span>}`. The `waiting` class logic stays tied to `items.length` only.
  - Pass to `HostList`: `now`, `onAutoAllow={setAutoDialog}`, `onStopAutoAllow={stopAuto}`, `paused={pausedHosts(servers)}`, `onResume={resumeAll}`, `onStopPaused={stopAll}`. Pass to `Terminals`/`TerminalTabs`: `autoHosts={autoHosts(servers, now)}`. Pass to `ApprovalPanel`: `autoFeed`, `autoN`, `paused={pausedHosts(servers)}`, `onStopAll={stopAll}`, `onResume={resumeAll}`.
- [ ] **Step 7: `TerminalTabs.tsx`:** accept `autoHosts: Set<string>`; in `.tabname`, after `{t.server}`, add `{autoHosts.has(t.server) && <span className="autodot" role="img" aria-label="auto-allow on" />}`.
- [ ] **Step 8: `ApprovalPanel.tsx`:** new props `autoFeed: AutoAllowRan[]`, `autoN: number`, `paused: string[]`, `onStopAll: () => void`, `onResume: () => void`. Below the pending list (outside the observed `listRef` element, so feed rows never restart the Allow delay), add:

```tsx
<section className="autofeed" aria-label="Auto-allowed">
  <div className="approvals-head">
    <h3>Auto-allowed</h3>
    {/* Always rendered so nothing shifts, like Deny all. */}
    <button type="button" className="btn danger-outline" onClick={onStopAll} disabled={autoN === 0}>Stop all auto-allow</button>
  </div>
  <AutoAllowPaused hosts={paused} onResume={onResume} onStop={onStopAll} />
  {autoFeed.length === 0 ? <p className="muted empty">Nothing ran on auto-allow.</p> : (
    <ul>{autoFeed.map((r, i) => (
      <li key={`${r.time}-${i}`} className="autorun">
        <span className="mono muted">{new Date(r.time).toLocaleTimeString()}</span> <strong>{r.server}</strong>
        <code className="cmd">{highlightNonAscii(commandLabel(r)).map((seg, j) => seg.mark ? <mark key={j}>{seg.text}</mark> : <span key={j}>{seg.text}</span>)}</code>
        {r.description && <span className="muted">{r.description}</span>}
        <span className={r.error ? 'error' : 'muted'}>{r.error ?? `exit ${r.exitCode}`}</span>
      </li>))}</ul>
  )}
</section>
```

  Check `highlightNonAscii`'s `Segment` shape in `approvals.ts` and render it exactly as the approval card does (reuse that JSX if it is a helper).
- [ ] **Step 9: `styles.css`:** add `--auto` and `--auto-tint` to both theme blocks (dark: `--auto: #8FA7F0; --auto-tint: #1F2744;`, light: `--auto: #3B53A8; --auto-tint: #e3e8fb;`), and:

```css
.chip.auto { background: var(--auto-tint); color: var(--auto); }
.count.auto { background: var(--auto-tint); color: var(--auto); margin-left: 4px; }
.autodot { display: inline-block; width: 6px; height: 6px; border-radius: 50%; background: var(--auto); margin-left: 6px; }
.banner.auto { display: flex; gap: 8px; align-items: center; padding: 8px 12px; border-radius: 6px; background: var(--auto-tint); color: var(--auto); }
.autofeed ul { list-style: none; margin: 0; padding: 0; }
.autorun { display: grid; gap: 2px; padding: 6px 0; border-top: 1px solid var(--line); }
```

  (Use the file's existing border token if it is not `--line`.) No animation on any `auto` class.
- [ ] **Step 10: Run** `cd desktop && npm run typecheck && npm test` — PASS. Then `npm run e2e -- e2e/smoke.spec.ts e2e/ui.spec.ts e2e/tunnels.spec.ts` — PASS (nothing existing broke). If `ui.spec.ts` screenshots change because the host card gained a button, update the expectations the spec keeps (it writes screenshots; it does not diff them unless it says so — check).
- [ ] **Step 11: Commit** `feat(desktop): auto-allow dialog, chip, paused banner, tab dot, and feed`

### Task 7: End-to-end test and docs

**Files:**
- Create: `desktop/e2e/autoallow.spec.ts`
- Modify: `docs/superpowers/ROADMAP.md`, `PRODUCT.md`, `README.md`, `CLAUDE.md`, `docs/superpowers/specs/2026-09-24-desktop-app-design.md`, `docs/superpowers/specs/2026-09-28-auto-allow-design.md` (Status line)

- [ ] **Step 1: Write `desktop/e2e/autoallow.spec.ts`** (model it on `smoke.spec.ts`: `launch()`, `unlock(win)`, `doorCall(l.socket, 'exec', {...})` — the `box` host from `sshtestd` is AI-visible, pinned, user `test`, password auth):

```ts
import { test, expect } from '@playwright/test'
import { launch, unlock, type Launched } from './launch'
import { doorCall } from './doorClient'

let l: Launched
test.beforeAll(async () => { l = await launch() })
test.afterAll(async () => { await l?.close() })

const exec = (id: string, command: string) =>
  doorCall(l.socket, 'exec', { requestId: id, client: 'e2e', server: 'box', command, description: 'auto e2e' })

test('timed auto-allow runs exec without a click, and Stop and Lock end it', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  await win.getByRole('button', { name: 'Auto-allow box' }).click()
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await d.getByRole('radio', { name: '15 min' }).check()
  await win.waitForTimeout(600)
  await d.getByRole('button', { name: 'Enable' }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveText(/Auto 1[45]m/)

  const out = await exec('a1', 'echo auto-one') // no decision is made anywhere
  expect(JSON.stringify(out)).toContain('echo auto-one')
  await win.getByRole('button', { name: 'AI requests' }).click()
  await expect(win.getByRole('region', { name: 'Auto-allowed' })).toContainText('echo auto-one')

  await win.getByRole('button', { name: 'Stop auto-allow box' }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveCount(0)
  const waiting = exec('a2', 'echo must-ask')
  await expect(win.locator('.approval')).toContainText('echo must-ask')
  await win.locator('.approval input').press('Enter') // deny
  await expect(waiting).rejects.toThrow(/Denied/)

  await win.getByRole('button', { name: 'Auto-allow box' }).click()
  await win.waitForTimeout(600)
  await win.getByRole('dialog').getByRole('button', { name: 'Enable' }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveCount(1)
  await win.getByRole('button', { name: 'Lock' }).click()
  await unlock(win)
  await expect(win.locator('.hostcard .chip.auto')).toHaveCount(0)
})

test('forever auto-allow pauses after unlock until Resume', async () => {
  const win = await l.app.firstWindow()
  await win.locator('.tabbar .hometab').click()
  await win.getByRole('button', { name: 'Auto-allow box' }).click()
  const d = win.getByRole('dialog')
  await d.getByRole('radio', { name: 'Until turned off' }).check()
  await d.getByLabel('Type box to confirm').fill('box')
  await win.waitForTimeout(600)
  await d.getByRole('button', { name: 'Enable' }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveText('Auto ∞')

  await win.getByRole('button', { name: 'Lock' }).click()
  await unlock(win)
  await expect(win.getByText('Auto-allow is paused on box.').first()).toBeVisible()
  const waiting = exec('f1', 'echo paused-asks')
  await expect(win.locator('.approval')).toContainText('echo paused-asks')
  await win.locator('.approval input').press('Enter')
  await expect(waiting).rejects.toThrow(/Denied/)

  await win.waitForTimeout(600)
  await win.getByRole('button', { name: 'Resume' }).first().click()
  const out = await exec('f2', 'echo resumed')
  expect(JSON.stringify(out)).toContain('echo resumed')
  await win.getByRole('button', { name: 'Stop auto-allow box' }).click()
})
```

  Adjust selectors to the real markup (`.approval`, the Lock button's accessible name, the AI button) by reading `smoke.spec.ts` and `ui.spec.ts`; keep the assertions.
- [ ] **Step 2: Run** `cd desktop && npm run e2e -- e2e/autoallow.spec.ts` — PASS; then the whole `npm run e2e` — PASS (the opt-in live specs skip).
- [ ] **Step 3: Docs** (write in the repo's plain, exact style):
  - `docs/superpowers/ROADMAP.md`: replace the standing decision "Every AI command is approved by a human. No auto-approval rules of any kind until a spec argues otherwise." with the sentence from the spec's "ROADMAP, PRODUCT, README, CLAUDE.md changes" section, citing `specs/2026-09-28-auto-allow-design.md`. Add a "Findings carried forward" line: "Auto-allow → 4 (2026-09-28): a host on auto-allow sends its command output to the AI unreviewed; putting a host with secrets on auto-allow meets slice 4's entry gate." Add to the `Updated:` line "per-host auto-allow".
  - `PRODUCT.md`: Purpose ("a human decision on every single command" → per-command by default, auto-allow per host as the spec states), Positioning, and the principles section, with the honest limit.
  - `README.md`: line 9 and the "no allow-list, no 'always allow', and no auto-approval of any kind" bullet → the rule and the honest limit (a grant is not a privilege boundary; persistence outlives it); a short "Auto-allow" subsection under "The desktop app" describing the button, durations, Resume, and Stop all.
  - `docs/superpowers/specs/2026-09-24-desktop-app-design.md` Threat Model: one paragraph noting that on a host with a grant a same-uid process reaching the MCP door gets remote exec without a human, and approval is no longer the only egress control there; point to the auto-allow spec.
  - `CLAUDE.md`: in "Hub and bridge", change "Every MCP exec goes through `broker.Submit` and blocks for a human decision" to say "unless its host has an auto-allow grant (see Auto-allow below)"; add `servers.setAutoAllow`, `servers.autoAllowCheck` to the UI-door method list and protocol 6; add an "Auto-allow (`autoallow.go`)" bullet summarising the rules (grants under `h.mu`, snapshot check, cap 2, not in `running`, timed grants hold the idle lock until their deadline, forever flag + Resume, every save/delete/forget/vault.create turns it off, reload never arms, audit `approval: "auto"`, MCP door blind to it); extend the `Hub.Exec` order with the auto step; in Desktop add the host-card button/dialog, chip, paused banner, tab dot, AI-button segment, and feed; add `autoallow` to the e2e list.
  - Spec status: `Status: Approved 2026-09-28; implemented (plan \`plans/2026-09-28-auto-allow.md\`).`
- [ ] **Step 4: Run** `go vet ./... && go test -race ./...` and `cd desktop && npm run typecheck && npm test` — PASS.
- [ ] **Step 5: Commit** `test(desktop): auto-allow e2e; docs: auto-allow replaces the no-auto-approval rule`
