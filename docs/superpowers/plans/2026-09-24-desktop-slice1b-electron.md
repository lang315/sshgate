# Desktop Slice 1b: Electron Shell Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A desktop app (macOS, Windows, Linux) that runs `ssh-mcp hub` as a child process and gives the author an unlock screen, a host list, SSH terminal tabs, and a non-modal approval panel with OS notifications for AI commands, replacing `hub --cli` for daily use.

**Architecture:** Electron main spawns `ssh-mcp hub` and speaks newline JSON-RPC over its stdio (the UI door built in slice 1a). A sandboxed preload exposes a small typed bridge; the React renderer talks to Go only through `src/renderer/transport.ts`. The hub gains a `hello` method, a `locked` notification, and the spec's 15-minute idle auto-lock. A dev-only fake SSH server (`sshtestd`) makes the Playwright smoke test and CI run without Docker.

**Tech Stack:** Go 1.26 (hub side). Node 22.12+, Electron 44, React 19, TypeScript 5, Vite 8 + `@vitejs/plugin-react`, `@xterm/xterm` 6 + `@xterm/addon-fit`, Vitest 5, `@playwright/test` (Electron mode). No other runtime dependencies.

**Spec:** `docs/superpowers/specs/2026-09-24-desktop-app-design.md` (Architecture → Components `desktop/`, Vault lifecycle, AI Command Flow steps 5–6, Terminal Sessions, Testing, Success Criteria, Amendments). Standing decisions: `docs/superpowers/ROADMAP.md`.

## Global Constraints

- Audience is the author; deliverable is `npm start` in `desktop/`. No signing, notarization, auto-update, or installers.
- Renderer security: `contextIsolation: true`, `sandbox: true`, `nodeIntegration: false`, strict CSP (`default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'none'; object-src 'none'; frame-ancestors 'none'`). `'unsafe-inline'` for styles only, because xterm.js injects style elements.
- Only `src/renderer/transport.ts` touches `window.sshmcp`. Main forwards only whitelisted hub methods.
- UI-door protocol (spec §Amendments): integer JSON-RPC ids; requests: `hello`, `status`, `unlock`, `lock`, `servers`, `pending`, `decide`, `denyAll`, `term.open`, `term.close`; notifications from UI to hub: `term.write`, `term.ack`, `term.resize`; notifications from hub: `pending`, `decided`, `locked`, `term.data`, `term.exit`, `term.dropped`. Terminal ids are client-chosen, 1–64 chars of `[A-Za-z0-9_-]`. Send nothing to a terminal before its `term.open` reply. Ignore data for unknown ids. `[]byte` fields are base64 in JSON.
- Hub restart: backoff 1 s, 3 s, 10 s; after three crashes within 60 s, stop and show the hub's last stderr. A restarted hub starts locked.
- Approval panel: non-modal side panel that never takes focus from the terminal; full command in monospace, never truncated, non-ASCII highlighted; requested timeout; description and client name labelled "(unverified)"; Deny is the default and the only action reachable by Enter; Allow has no keyboard shortcut and is disabled for 500 ms after the request appears; Deny all; Send to tab pastes with `terminal.paste()` and never presses Enter.
- OS notification when a request arrives and the window is not focused; clicking it focuses the window. Tray shows the pending count.
- Idle auto-lock: 15 minutes with no UI-door request and no pending or running AI request. Open terminals keep their SSH connections.
- Resize debounce 50 ms. Flow control: ack each `term.data` chunk after `xterm.write` renders it.
- Go: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...` before every Go commit. Desktop: `npm run typecheck && npm test` before every desktop commit.
- Commit messages: Conventional Commits, ending with a blank line and exactly:
  `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`
  `Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2`

## File Structure

| Path | Responsibility |
|---|---|
| `internal/hub/idle.go` (new) | Idle auto-lock timer and activity accounting |
| `internal/hub/uidoor.go` (modify) | `hello`, `locked` notification, touch activity on every request |
| `internal/hub/hub.go` (modify) | Running-exec counter; `Lock` emits a lock event; `Options.IdleLock` |
| `internal/sshx/sshtest/sshtest.go` (modify) | `Listen(cleanup)` usable outside `testing` |
| `internal/sshx/sshtest/sshtestd/main.go` (new) | Dev/test fake sshd that can write a ready vault |
| `desktop/package.json`, `package-lock.json`, `tsconfig*.json`, `vite.config.ts`, `vitest.config.ts`, `playwright.config.ts`, `.gitignore` | Toolchain |
| `desktop/index.html` | Renderer entry with CSP |
| `desktop/src/main/main.ts` | App lifecycle, window, wiring |
| `desktop/src/main/hubProcess.ts` | Spawn/restart the hub, JSON-RPC client over stdio |
| `desktop/src/main/ipc.ts` | Whitelisted IPC relay between renderer and hub |
| `desktop/src/main/attention.ts` | Tray count and OS notifications |
| `desktop/src/preload/preload.ts` | `contextBridge` API |
| `desktop/src/shared/protocol.ts` | Types shared by main, preload, renderer |
| `desktop/src/renderer/transport.ts` | The only module that talks to the hub |
| `desktop/src/renderer/main.tsx`, `App.tsx`, `Unlock.tsx`, `HostList.tsx` | Shell UI |
| `desktop/src/renderer/terminals.ts`, `TermView.tsx`, `Terminals.tsx` | Terminal tabs |
| `desktop/src/renderer/approvals.ts`, `ApprovalPanel.tsx` | Approval panel |
| `desktop/src/renderer/styles.css` | Styling |
| `desktop/test/*.test.ts`, `desktop/test/fixtures/fakeHub.mjs` | Unit tests |
| `desktop/e2e/smoke.spec.ts` | Playwright Electron smoke test |
| `.github/workflows/ci.yml` (modify) | `desktop` job |
| `README.md`, `CLAUDE.md`, `docs/superpowers/checklists/slice1-manual.md`, `docs/superpowers/ROADMAP.md` | Docs |

---

### Task 1: Hub `hello`, `locked` notification, idle auto-lock

**Files:**
- Create: `internal/hub/idle.go`, `internal/hub/idle_test.go`
- Modify: `internal/hub/hub.go` (Options, Exec running counter, Lock event), `internal/hub/uidoor.go`
- Test: `internal/hub/uidoor_test.go`

**Interfaces:**
- Produces (UI door): request `hello` → `{"protocol": 1}`; notification `locked` `{"reason": "idle" | "manual"}` whenever the vault goes from unlocked to locked.
- Produces (Go): `Options.IdleLock time.Duration` (0 → 15 minutes; negative → disabled); `func (h *Hub) touch()`; `const ProtocolVersion = 1`.
- Consumes: existing `Hub.Lock`, `Hub.Locked`, broker `Pending()`, `setEventSink`.

Design: the hub keeps `lastActivity time.Time` and `running int` under `h.mu`. `touch()` sets `lastActivity = now`. `Exec` increments `running` after approval and decrements when the command returns (defer), and calls `touch()` on submit and on return. An idle goroutine started by `New` (stopped by a new `Close()` method, which `runHub` defers) wakes every `IdleLock/15` (min 1 s) and locks when: the vault is unlocked, `len(Pending()) == 0`, `running == 0`, and `now - lastActivity >= IdleLock`. `Lock()` gains an internal variant `lockWithReason(reason)` that, if the vault was unlocked, zeroes the key and emits a hub-level event to the UI sink. Because the event sink currently carries `broker.Event`, add a second sink field `lockSink func(reason string)` set by `ServeUIDoor` alongside the broker sink (same release-token pattern: return a release func).

- [ ] **Step 1: Write the failing tests**

`internal/hub/idle_test.go`:

```go
package hub

import (
	"context"
	"testing"
	"time"
)

func TestIdleLockLocksAfterQuietPeriod(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: 200 * time.Millisecond})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !h.Locked() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !h.Locked() {
		t.Fatal("vault did not auto-lock")
	}
}

func TestIdleLockHeldOffByActivity(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: 300 * time.Millisecond})
	h.Unlock("pw")
	for i := 0; i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
		h.touch()
	}
	if h.Locked() {
		t.Fatal("locked despite activity")
	}
}

func TestIdleLockHeldOffByPendingRequest(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: 200 * time.Millisecond, ApprovalExpiry: time.Minute})
	h.Unlock("pw")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Exec(ctx, ExecRequest{Server: "vis", Command: "ls"})
	waitPending(t, h.Broker())
	time.Sleep(600 * time.Millisecond)
	if h.Locked() {
		t.Fatal("locked while a request was pending")
	}
}

func TestIdleLockDisabledWhenNegative(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: -1})
	h.Unlock("pw")
	time.Sleep(300 * time.Millisecond)
	if h.Locked() {
		t.Fatal("negative IdleLock must disable auto-lock")
	}
}
```

`newEncryptedHub(t, o Options) (*Hub, string)` is a new helper in `idle_test.go`: it writes a vault with a KDF for master password `pw` and one AI-visible **encrypted** password server `vis` with a pinned host key (follow the vault-building code already in `hub_test.go`'s locked-vault tests), sets `o.StorePath`, opens an audit in `t.TempDir()`, uses a fake executor (`Dialer` returning the existing `fakeExec`), calls `New(o)`, and registers `t.Cleanup(h.Close)`. Reuse the existing `waitPending` helper (Task 1a's bounded wait); if its name differs, use the existing bounded helper.

Append to `internal/hub/uidoor_test.go`:

```go
func TestUIDoorHelloAndLockedNotification(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: -1})
	c, notes := startUI(t, h)
	var hello struct {
		Protocol int `json:"protocol"`
	}
	if err := c.Call(context.Background(), "hello", nil, &hello); err != nil || hello.Protocol != 1 {
		t.Fatalf("hello: %v %+v", err, hello)
	}
	if err := c.Call(context.Background(), "unlock", map[string]string{"password": "pw"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(context.Background(), "lock", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-notes:
		if m != "locked" {
			t.Fatalf("want locked notification, got %s", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no locked notification")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/hub -run 'TestIdleLock|TestUIDoorHello' -v`
Expected: compile failure (`IdleLock`, `touch`, `Close`, `hello`).

- [ ] **Step 3: Implement**

`internal/hub/idle.go`:

```go
package hub

import "time"

const ProtocolVersion = 1

const defaultIdleLock = 15 * time.Minute

// touch records UI or AI activity; the idle auto-lock counts from the last one.
func (h *Hub) touch() {
	h.mu.Lock()
	h.lastActivity = time.Now()
	h.mu.Unlock()
}

// idleLoop locks the vault after a quiet period with nothing pending or
// running. It exits when h.done is closed (Close).
func (h *Hub) idleLoop(idle time.Duration) {
	tick := idle / 15
	if tick < time.Second {
		tick = time.Second
	}
	if idle < tick {
		tick = idle
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-h.done:
			return
		case now := <-t.C:
			h.mu.Lock()
			quiet := now.Sub(h.lastActivity) >= idle && h.running == 0
			h.mu.Unlock()
			if quiet && len(h.broker.Pending()) == 0 {
				h.lockWithReason("idle")
			}
		}
	}
}
```

Ticks shorter than 1 s are allowed only when `idle` itself is shorter (tests); the `if idle < tick` line covers that.

`internal/hub/hub.go` changes:
- `Options` gains `IdleLock time.Duration`.
- `Hub` gains `lastActivity time.Time`, `running int`, `done chan struct{}`, `closeOnce sync.Once`, `lockSink func(string)`, `lockSinkGen uint64`.
- `New`: set `lastActivity = time.Now()`, `done = make(chan struct{})`; resolve idle: `0 → defaultIdleLock`; if `> 0`, `go h.idleLoop(idle)`.
- `func (h *Hub) Close() { h.closeOnce.Do(func() { close(h.done) }) }`.
- `Lock()` becomes `func (h *Hub) Lock() { h.lockWithReason("manual") }` and:

```go
func (h *Hub) lockWithReason(reason string) {
	h.mu.Lock()
	wasUnlocked := h.deps.MasterKey != nil
	clear(h.deps.MasterKey)
	h.deps.MasterKey = nil
	sink := h.lockSink
	h.mu.Unlock()
	if wasUnlocked && sink != nil {
		sink(reason)
	}
}
```

(Keep whatever the existing `Lock` did beyond zeroing the key.)
- `setLockSink(f func(string)) (release func())` with the same generation-token pattern as `setEventSink`.
- `Exec`: call `h.touch()` at entry; after the approval branch reaches execution, `h.mu.Lock(); h.running++; h.mu.Unlock()` and `defer func() { h.mu.Lock(); h.running--; h.lastActivity = time.Now(); h.mu.Unlock() }()`.
- `Unlock` calls `h.touch()` on success.

`internal/hub/uidoor.go`:
- After installing the broker sink: `releaseLock := h.setLockSink(func(reason string) { s.Notify("locked", map[string]string{"reason": reason}) })`, `defer releaseLock()`.
- Register `s.HandleRequest("hello", func(context.Context, json.RawMessage) (any, error) { return map[string]int{"protocol": ProtocolVersion}, nil })`.
- Every UI-door request handler must call `h.touch()`. Do it once: wrap registration in a local helper `req := func(name string, fn rpc.Handler) { s.HandleRequest(name, func(ctx context.Context, raw json.RawMessage) (any, error) { h.touch(); return fn(ctx, raw) }) }` and use `req(...)` for every request method in uidoor.go. `registerTermMethods` keeps its own registrations; add `h.touch()` at the top of `term.open` and `term.close`, and in the `term.write` notification handler (non-blocking: `touch` only takes `h.mu` briefly).
- `status` must not count as activity (the desktop app polls it): register `status` with plain `s.HandleRequest`, not `req`.

`cmd/ssh-mcp/hub.go`: after `hub.New`, add `defer h.Close()`.

- [ ] **Step 4: Run tests**

Run: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/hub cmd/ssh-mcp/hub.go
git commit -m "feat(hub): hello method, locked notification, 15-minute idle auto-lock"
```

---

### Task 2: `sshtestd` dev server that can write a ready vault

**Files:**
- Modify: `internal/sshx/sshtest/sshtest.go`
- Create: `internal/sshx/sshtest/sshtestd/main.go`, `internal/sshx/sshtest/sshtestd/main_test.go`

**Interfaces:**
- Produces: `func Listen() (*Server, func(), error)` in `sshtest` (no `testing` dependency; returns a stop func); `Start(t)` becomes a thin wrapper. `Server` gains `HostKeyFingerprint string` (`ssh.FingerprintSHA256` of its host key).
- Produces: binary `sshtestd` with flags `-write-store=<path>` and `-password=<master>`; prints `PORT=<n>` on its first stdout line; auto-releases execs (closes `Release` at start); runs until SIGINT/SIGTERM or stdin EOF.
- The written store contains one server `box` (`127.0.0.1:<port>`, user `test`, auth `password`, encrypted password `testpass`, `AIVisible: true`, `HostKey` = the server's fingerprint), encrypted with a KDF for `-password`.

- [ ] **Step 1: Write the failing test**

`internal/sshx/sshtest/sshtestd/main_test.go`:

```go
package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/ssh-mcp/internal/config"
)

func TestSSHTestdWritesReadyStore(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "sshtestd")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	store := filepath.Join(t.TempDir(), "servers.json")
	cmd := exec.Command(bin, "-write-store="+store, "-password=pw")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); cmd.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "PORT=") {
		t.Fatalf("first line %q err %v", line, err)
	}
	f, err := config.Load(store)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := f.FindServer("box")
	if !ok || !s.AIVisible || s.HostKey == "" || s.EncPassword == "" || f.KDF == nil {
		t.Fatalf("store not ready: %+v", f)
	}
	mk, err := f.KDF.DeriveKey("pw")
	if err != nil || !f.KDF.Verify(mk) || f.VerifyMAC(mk) != nil {
		t.Fatalf("store does not open with pw: %v", err)
	}
	info, _ := os.Stat(store)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm %v", info.Mode().Perm())
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/sshx/sshtest/sshtestd -v`
Expected: FAIL (no main package).

- [ ] **Step 3: Implement**

In `sshtest.go`, move the body of `Start` into `Listen`:

```go
// Listen starts a server without the testing package. stop closes it.
func Listen() (*Server, func(), error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, nil, err
	}
	// cfg exactly as today
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	s := &Server{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port,
		HostKeyFingerprint: ssh.FingerprintSHA256(signer.PublicKey()),
		Execs: make(chan string, 16), Release: make(chan struct{}), done: make(chan struct{})}
	var once sync.Once
	stop := func() { once.Do(func() { close(s.done); ln.Close() }) }
	go func() { /* accept loop exactly as today */ }()
	return s, stop, nil
}

func Start(t testing.TB) *Server {
	t.Helper()
	s, stop, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return s
}
```

Keep every other part of the file unchanged (if Task 1a added host-key options to `Start`, keep them by routing through `Listen`). In `sshtestd` the `Execs` channel must not fill up: drain it in a goroutine.

`internal/sshx/sshtest/sshtestd/main.go`:

```go
// Command sshtestd is a development and test SSH server. It accepts any
// password, echoes shell input, and answers exec with the command text.
// With -write-store it writes a vault containing one AI-visible server
// "box" that points at itself, ready to unlock with -password.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx/sshtest"
)

func main() {
	store := flag.String("write-store", "", "write a ready vault to this path")
	pw := flag.String("password", "pw", "master password for -write-store")
	flag.Parse()

	srv, stop, err := sshtest.Listen()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer stop()
	close(srv.Release)
	go func() {
		for range srv.Execs {
		}
	}()
	if *store != "" {
		if err := writeStore(*store, *pw, srv); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("PORT=%d\n", srv.Port)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	eof := make(chan struct{})
	go func() { io.Copy(io.Discard, os.Stdin); close(eof) }()
	select {
	case <-sig:
	case <-eof:
	}
}

func writeStore(path, pw string, srv *sshtest.Server) error {
	k, mk, err := config.NewKDF(pw)
	if err != nil {
		return err
	}
	f := &config.File{Version: 1, KDF: &k, Servers: []config.Server{{
		Name: "box", Host: srv.Host, Port: srv.Port, User: "test", Auth: "password",
		AIVisible: true, HostKey: srv.HostKeyFingerprint,
	}}}
	enc, err := config.Encrypt(mk, "box/encPassword", config.AADFor(f, f.Servers[0], "encPassword"), "testpass")
	if err != nil {
		return err
	}
	f.Servers[0].EncPassword = enc
	return config.Save(path, f, mk)
}
```

Note: `syscall.SIGTERM` exists on Windows in Go's syscall package, so this builds everywhere.

- [ ] **Step 4: Run tests**

Run: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sshx/sshtest
git commit -m "feat(sshtest): sshtestd dev server that writes a ready vault"
```

---

### Task 3: `desktop/` toolchain and an empty secure window

**Files:**
- Create: `desktop/package.json`, `desktop/tsconfig.json`, `desktop/tsconfig.main.json`, `desktop/vite.config.ts`, `desktop/vitest.config.ts`, `desktop/.gitignore`, `desktop/index.html`, `desktop/src/main/main.ts`, `desktop/src/preload/preload.ts`, `desktop/src/renderer/main.tsx`, `desktop/src/renderer/App.tsx`, `desktop/src/renderer/styles.css`, `desktop/test/smoke.test.ts`
- Generated: `desktop/package-lock.json` (commit it)

**Interfaces:**
- Produces scripts: `npm run build` (renderer to `dist/renderer`, main+preload to `dist/main` and `dist/preload`), `npm start` (build then `electron .`), `npm test` (vitest), `npm run typecheck`, `npm run e2e` (Task 10).
- `package.json` `"main": "dist/main/main.js"`.

- [ ] **Step 1: Create the toolchain files**

`desktop/package.json`:

```json
{
  "name": "ssh-mcp-desktop",
  "private": true,
  "version": "0.1.0",
  "description": "Desktop app for ssh-mcp: terminals and AI command approval",
  "main": "dist/main/main.js",
  "scripts": {
    "build:renderer": "vite build",
    "build:main": "tsc -p tsconfig.main.json",
    "build": "npm run build:renderer && npm run build:main",
    "start": "npm run build && electron .",
    "typecheck": "tsc -p tsconfig.json --noEmit && tsc -p tsconfig.main.json --noEmit",
    "test": "vitest run",
    "e2e": "npm run build && playwright test"
  }
}
```

Install dependencies (exact versions come from the lockfile; use the current majors named in Tech Stack):

```bash
cd desktop
npm install --save react@19 react-dom@19 @xterm/xterm@6 @xterm/addon-fit
npm install --save-dev electron@44 typescript@5 vite@8 @vitejs/plugin-react vitest@5 @types/react@19 @types/react-dom@19 @types/node@22 @playwright/test
```

If `@vitejs/plugin-react` requires an optional peer that npm cannot resolve, pin the latest plugin version whose peer range includes the installed Vite and note it in the report.

`desktop/tsconfig.json` (renderer + shared + tests):

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noEmit": true,
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "types": ["vite/client"],
    "skipLibCheck": true
  },
  "include": ["src/renderer", "src/shared", "test", "e2e", "vite.config.ts", "vitest.config.ts", "playwright.config.ts"]
}
```

`desktop/tsconfig.main.json` (Electron main + preload, CommonJS so the sandboxed preload is one file):

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "CommonJS",
    "moduleResolution": "Node",
    "strict": true,
    "outDir": "dist",
    "rootDir": "src",
    "types": ["node"],
    "skipLibCheck": true
  },
  "include": ["src/main", "src/preload", "src/shared"]
}
```

`desktop/vite.config.ts`:

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  base: './',
  build: { outDir: 'dist/renderer', emptyOutDir: true },
})
```

`desktop/vitest.config.ts`:

```ts
import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: { include: ['test/**/*.test.ts'], environment: 'node' },
})
```

`desktop/.gitignore`:

```
node_modules/
dist/
test-results/
playwright-report/
```

`desktop/index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta http-equiv="Content-Security-Policy"
      content="default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'none'; object-src 'none'; frame-ancestors 'none'" />
    <title>ssh-mcp</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/renderer/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 2: Minimal main, preload, renderer**

`desktop/src/main/main.ts`:

```ts
import { app, BrowserWindow } from 'electron'
import * as path from 'node:path'

function createWindow(): BrowserWindow {
  const win = new BrowserWindow({
    width: 1200,
    height: 800,
    webPreferences: {
      preload: path.join(__dirname, '..', 'preload', 'preload.js'),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
    },
  })
  win.loadFile(path.join(__dirname, '..', 'renderer', 'index.html'))
  return win
}

app.whenReady().then(() => {
  createWindow()
})

app.on('window-all-closed', () => app.quit())
```

`desktop/src/preload/preload.ts`:

```ts
import { contextBridge } from 'electron'

contextBridge.exposeInMainWorld('sshmcp', {})
```

`desktop/src/renderer/main.tsx`:

```tsx
import { createRoot } from 'react-dom/client'
import { App } from './App'
import './styles.css'

createRoot(document.getElementById('root')!).render(<App />)
```

`desktop/src/renderer/App.tsx`:

```tsx
export function App() {
  return <div className="app">ssh-mcp</div>
}
```

`desktop/src/renderer/styles.css`:

```css
html, body, #root { height: 100%; margin: 0; }
body { font-family: system-ui, sans-serif; background: #1e1e1e; color: #ddd; }
.app { height: 100%; }
```

`desktop/test/smoke.test.ts` (proves the test runner works; replaced by real tests later):

```ts
import { describe, expect, it } from 'vitest'

describe('toolchain', () => {
  it('runs', () => {
    expect(1 + 1).toBe(2)
  })
})
```

- [ ] **Step 3: Verify**

Run (in `desktop/`): `npm run typecheck && npm test && npm run build`
Expected: all pass; `dist/main/main.js`, `dist/preload/preload.js`, `dist/renderer/index.html` exist. Then `npx electron . ` must open a window showing "ssh-mcp" (close it). Record in the report that the window opened, or that no display is available.

- [ ] **Step 4: Commit**

```bash
git add desktop
git commit -m "feat(desktop): Electron + React + Vite toolchain with a sandboxed window"
```

---

### Task 4: Hub process manager with restart backoff

**Files:**
- Create: `desktop/src/shared/protocol.ts`, `desktop/src/main/hubProcess.ts`, `desktop/test/hubProcess.test.ts`, `desktop/test/fixtures/fakeHub.mjs`

**Interfaces:**
- Produces (`protocol.ts`):

```ts
export type HubState =
  | { kind: 'starting' }
  | { kind: 'running' }
  | { kind: 'restarting'; attempt: number; inMs: number }
  | { kind: 'failed'; message: string; stderr: string }

export type Outcome = 'allowed' | 'denied' | 'expired' | 'withdrawn' | 'sent_to_tab'

export interface ApprovalRequest {
  id: string; client: string; server: string; command: string
  description: string; sudo: boolean; timeoutSec: number; receivedAt: string
}

export interface ServerInfo {
  name: string; host: string; port: number; user: string; auth: string
  hostKey: string; aiVisible: boolean; locked: boolean
}

export type HubEvent =
  | { method: 'pending'; params: { request: ApprovalRequest } }
  | { method: 'decided'; params: { request: ApprovalRequest; decision: { outcome: Outcome; reason: string } } }
  | { method: 'locked'; params: { reason: 'idle' | 'manual' } }
  | { method: 'term.data'; params: { id: string; data: string } }
  | { method: 'term.exit'; params: { id: string; code: number; reason: string } }
  | { method: 'term.dropped'; params: { id: string; bytes: number } }

export const REQUEST_METHODS = ['hello', 'status', 'unlock', 'lock', 'servers', 'pending',
  'decide', 'denyAll', 'term.open', 'term.close'] as const
export type RequestMethod = (typeof REQUEST_METHODS)[number]
export const NOTIFY_METHODS = ['term.write', 'term.ack', 'term.resize'] as const
export type NotifyMethod = (typeof NOTIFY_METHODS)[number]
export const PROTOCOL_VERSION = 1
```

- Produces (`hubProcess.ts`):

```ts
export interface HubProcessOptions {
  command: string
  args: string[]
  env?: NodeJS.ProcessEnv
  backoffMs?: number[]          // default [1000, 3000, 10000]
  crashWindowMs?: number        // default 60000
  maxCrashes?: number           // default 3
}
export class HubProcess extends EventEmitter {
  // events: 'state' (HubState), 'notification' (method: string, params: unknown)
  constructor(opts: HubProcessOptions)
  start(): void
  call<T = unknown>(method: string, params?: unknown, timeoutMs?: number): Promise<T>
  notify(method: string, params?: unknown): void
  stop(): Promise<void>          // closes stdin, waits up to 3 s, then kills
  get state(): HubState
}
```

Behaviour: after spawn, state `starting`; call `hello`; if `protocol !== PROTOCOL_VERSION` → state `failed` with message `hub protocol N, app expects 1` and kill; else state `running`. Pending calls reject with `Error('hub restarted')` when the process exits. On unexpected exit (not after `stop()`): record the crash time; if `maxCrashes` crashes fall within `crashWindowMs`, state `failed` with `stderr` = last 50 stderr lines joined; otherwise state `restarting` with the next backoff (index = crashes-in-window − 1, capped at the last value) and respawn after it. Stdout is parsed as newline-delimited JSON; lines with `id` resolve/reject calls (`error.message` becomes the Error message); lines with `method` and no `id` are emitted as `notification`. Non-JSON lines are ignored. Request ids are increasing integers.

- [ ] **Step 1: Write the fake hub fixture**

`desktop/test/fixtures/fakeHub.mjs` — a tiny JSON-RPC peer controlled by env vars:

```js
// Fake ssh-mcp hub for tests. FAKE_HUB_MODE: "ok" | "crash" | "badproto".
import readline from 'node:readline'

const mode = process.env.FAKE_HUB_MODE ?? 'ok'
if (mode === 'crash') {
  process.stderr.write('boom: fake hub crashed\n')
  process.exit(3)
}
const rl = readline.createInterface({ input: process.stdin })
const send = (m) => process.stdout.write(JSON.stringify({ jsonrpc: '2.0', ...m }) + '\n')
rl.on('line', (line) => {
  const m = JSON.parse(line)
  if (m.id === undefined) {
    if (m.method === 'term.write') send({ method: 'term.data', params: { id: m.params.id, data: m.params.data } })
    return
  }
  switch (m.method) {
    case 'hello':
      return send({ id: m.id, result: { protocol: mode === 'badproto' ? 99 : 1 } })
    case 'status':
      return send({ id: m.id, result: { locked: true, hasStore: true, pending: 0 } })
    case 'fail':
      return send({ id: m.id, error: { code: -32000, message: 'nope' } })
    case 'exit':
      send({ id: m.id, result: {} })
      return process.exit(1)
    default:
      return send({ id: m.id, error: { code: -32601, message: 'method not found: ' + m.method } })
  }
})
rl.on('close', () => process.exit(0))
```

- [ ] **Step 2: Write the failing tests**

`desktop/test/hubProcess.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import * as path from 'node:path'
import { HubProcess } from '../src/main/hubProcess'
import type { HubState } from '../src/shared/protocol'

const fixture = path.join(__dirname, 'fixtures', 'fakeHub.mjs')

function hub(mode: string, extra: Partial<ConstructorParameters<typeof HubProcess>[0]> = {}) {
  return new HubProcess({
    command: process.execPath, args: [fixture],
    env: { ...process.env, FAKE_HUB_MODE: mode },
    backoffMs: [20, 40, 80], crashWindowMs: 5000, maxCrashes: 3, ...extra,
  })
}

function waitState(h: HubProcess, kind: HubState['kind'], ms = 3000): Promise<HubState> {
  return new Promise((resolve, reject) => {
    if (h.state.kind === kind) return resolve(h.state)
    const t = setTimeout(() => reject(new Error(`timeout waiting for ${kind}, at ${h.state.kind}`)), ms)
    h.on('state', (s: HubState) => { if (s.kind === kind) { clearTimeout(t); resolve(s) } })
  })
}

describe('HubProcess', () => {
  it('handshakes, calls, and relays notifications', async () => {
    const h = hub('ok')
    h.start()
    await waitState(h, 'running')
    expect(await h.call('status')).toEqual({ locked: true, hasStore: true, pending: 0 })
    await expect(h.call('fail')).rejects.toThrow('nope')
    const note = new Promise<[string, unknown]>((r) => h.once('notification', (m, p) => r([m, p])))
    h.notify('term.write', { id: 't1', data: 'aGk=' })
    expect(await note).toEqual(['term.data', { id: 't1', data: 'aGk=' }])
    await h.stop()
  })

  it('fails on a protocol mismatch', async () => {
    const h = hub('badproto')
    h.start()
    const s = await waitState(h, 'failed')
    expect(s.kind === 'failed' && s.message).toContain('protocol')
    await h.stop()
  })

  it('restarts with backoff, then gives up with stderr after three crashes', async () => {
    const h = hub('crash')
    const seen: string[] = []
    h.on('state', (s: HubState) => seen.push(s.kind))
    h.start()
    const s = await waitState(h, 'failed')
    expect(seen.filter((k) => k === 'restarting').length).toBe(2)
    expect(s.kind === 'failed' && s.stderr).toContain('boom')
    await h.stop()
  })

  it('rejects in-flight calls when the hub exits', async () => {
    const h = hub('ok')
    h.start()
    await waitState(h, 'running')
    await expect(h.call('exit').then(() => h.call('status', undefined, 1000))).rejects.toThrow()
    await h.stop()
  })

  it('stop() does not trigger a restart', async () => {
    const h = hub('ok')
    h.start()
    await waitState(h, 'running')
    const seen: string[] = []
    h.on('state', (s: HubState) => seen.push(s.kind))
    await h.stop()
    await new Promise((r) => setTimeout(r, 200))
    expect(seen).not.toContain('restarting')
  })
})
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `npm test -- hubProcess`
Expected: FAIL (module not found).

- [ ] **Step 4: Implement `protocol.ts` and `hubProcess.ts`**

`protocol.ts` exactly as in Interfaces. `hubProcess.ts`:

```ts
import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process'
import { EventEmitter } from 'node:events'
import * as readline from 'node:readline'
import { PROTOCOL_VERSION, type HubState } from '../shared/protocol'

export interface HubProcessOptions {
  command: string
  args: string[]
  env?: NodeJS.ProcessEnv
  backoffMs?: number[]
  crashWindowMs?: number
  maxCrashes?: number
}

interface Pending { resolve: (v: unknown) => void; reject: (e: Error) => void; timer?: NodeJS.Timeout }

export class HubProcess extends EventEmitter {
  private child?: ChildProcessWithoutNullStreams
  private nextId = 1
  private pending = new Map<number, Pending>()
  private crashes: number[] = []
  private stderrTail: string[] = []
  private stopping = false
  private restartTimer?: NodeJS.Timeout
  private _state: HubState = { kind: 'starting' }

  constructor(private readonly opts: HubProcessOptions) { super() }

  get state(): HubState { return this._state }

  private setState(s: HubState) { this._state = s; this.emit('state', s) }

  start(): void {
    this.stopping = false
    this.spawnChild()
  }

  private spawnChild(): void {
    this.setState({ kind: 'starting' })
    const child = spawn(this.opts.command, this.opts.args, { env: this.opts.env, stdio: 'pipe' })
    this.child = child
    readline.createInterface({ input: child.stdout }).on('line', (l) => this.onLine(l))
    readline.createInterface({ input: child.stderr }).on('line', (l) => {
      this.stderrTail.push(l)
      if (this.stderrTail.length > 50) this.stderrTail.shift()
    })
    child.on('error', (err) => this.stderrTail.push(String(err)))
    child.on('exit', () => this.onExit(child))
    this.call<{ protocol: number }>('hello', undefined, 10000).then((r) => {
      if (this.child !== child) return
      if (r.protocol !== PROTOCOL_VERSION) {
        this.fail(`hub protocol ${r.protocol}, app expects ${PROTOCOL_VERSION}`)
        child.kill()
        return
      }
      this.setState({ kind: 'running' })
    }, () => { /* exit handler reports it */ })
  }

  private onLine(line: string): void {
    let m: { id?: number; method?: string; params?: unknown; result?: unknown; error?: { message: string } }
    try { m = JSON.parse(line) } catch { return }
    if (typeof m.id === 'number') {
      const p = this.pending.get(m.id)
      if (!p) return
      this.pending.delete(m.id)
      if (p.timer) clearTimeout(p.timer)
      if (m.error) p.reject(new Error(m.error.message))
      else p.resolve(m.result)
    } else if (typeof m.method === 'string') {
      this.emit('notification', m.method, m.params)
    }
  }

  private onExit(child: ChildProcessWithoutNullStreams): void {
    if (this.child !== child) return
    this.child = undefined
    for (const p of this.pending.values()) { if (p.timer) clearTimeout(p.timer); p.reject(new Error('hub restarted')) }
    this.pending.clear()
    if (this.stopping || this._state.kind === 'failed') return
    const now = Date.now()
    const windowMs = this.opts.crashWindowMs ?? 60000
    this.crashes = this.crashes.filter((t) => now - t < windowMs)
    this.crashes.push(now)
    const max = this.opts.maxCrashes ?? 3
    if (this.crashes.length >= max) {
      this.fail(`the hub crashed ${this.crashes.length} times in ${Math.round(windowMs / 1000)} s`)
      return
    }
    const backoff = this.opts.backoffMs ?? [1000, 3000, 10000]
    const inMs = backoff[Math.min(this.crashes.length - 1, backoff.length - 1)]
    this.setState({ kind: 'restarting', attempt: this.crashes.length, inMs })
    this.restartTimer = setTimeout(() => { if (!this.stopping) this.spawnChild() }, inMs)
  }

  private fail(message: string): void {
    this.setState({ kind: 'failed', message, stderr: this.stderrTail.join('\n') })
  }

  call<T = unknown>(method: string, params?: unknown, timeoutMs?: number): Promise<T> {
    const child = this.child
    if (!child) return Promise.reject(new Error('hub is not running'))
    const id = this.nextId++
    return new Promise<T>((resolve, reject) => {
      const p: Pending = { resolve: resolve as (v: unknown) => void, reject }
      if (timeoutMs) p.timer = setTimeout(() => { this.pending.delete(id); reject(new Error(`${method} timed out`)) }, timeoutMs)
      this.pending.set(id, p)
      child.stdin.write(JSON.stringify({ jsonrpc: '2.0', id, method, params: params ?? {} }) + '\n')
    })
  }

  notify(method: string, params?: unknown): void {
    this.child?.stdin.write(JSON.stringify({ jsonrpc: '2.0', method, params: params ?? {} }) + '\n')
  }

  async stop(): Promise<void> {
    this.stopping = true
    if (this.restartTimer) clearTimeout(this.restartTimer)
    const child = this.child
    if (!child) return
    await new Promise<void>((resolve) => {
      const t = setTimeout(() => { child.kill(); resolve() }, 3000)
      child.once('exit', () => { clearTimeout(t); resolve() })
      child.stdin.end()
    })
  }
}
```

- [ ] **Step 5: Run tests**

Run: `npm run typecheck && npm test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add desktop/src desktop/test
git commit -m "feat(desktop): hub process manager with handshake and restart backoff"
```

---

### Task 5: Whitelisted IPC relay, preload bridge, renderer transport

**Files:**
- Create: `desktop/src/main/ipc.ts`, `desktop/src/renderer/transport.ts`, `desktop/test/ipc.test.ts`, `desktop/test/transport.test.ts`
- Modify: `desktop/src/preload/preload.ts`, `desktop/src/main/main.ts`

**Interfaces:**
- Produces (`ipc.ts`):

```ts
export interface HubLike {
  call(method: string, params?: unknown, timeoutMs?: number): Promise<unknown>
  notify(method: string, params?: unknown): void
}
export function relayCall(hub: HubLike, method: unknown, params: unknown): Promise<unknown>   // rejects non-whitelisted
export function relayNotify(hub: HubLike, method: unknown, params: unknown): void            // drops non-whitelisted
export function registerIpc(hub: HubProcess, getWindow: () => BrowserWindow | undefined): void
```

IPC channels: `hub:call` (invoke), `hub:notify` (send), `hub:event` (main → renderer, payload `{method, params}`), `hub:state` (main → renderer, `HubState`), `hub:get-state` (invoke).

- Produces (preload, `window.sshmcp`):

```ts
interface SshMcpBridge {
  call(method: string, params?: unknown): Promise<unknown>
  notify(method: string, params?: unknown): void
  getState(): Promise<HubState>
  onEvent(cb: (e: { method: string; params: unknown }) => void): () => void
  onState(cb: (s: HubState) => void): () => void
}
```

- Produces (`transport.ts`), the only renderer module that touches `window.sshmcp`:

```ts
export const hub = {
  hello(): Promise<{ protocol: number }>
  status(): Promise<{ locked: boolean; hasStore: boolean; pending: number }>
  unlock(password: string): Promise<void>
  lock(): Promise<void>
  servers(): Promise<ServerInfo[]>
  pending(): Promise<ApprovalRequest[]>
  decide(id: string, outcome: 'allowed' | 'denied' | 'sent_to_tab', reason?: string): Promise<void>
  denyAll(reason?: string): Promise<void>
  termOpen(id: string, server: string, rows: number, cols: number): Promise<void>
  termClose(id: string): Promise<void>
  termWrite(id: string, data: Uint8Array): void
  termAck(id: string, n: number): void
  termResize(id: string, rows: number, cols: number): void
  onEvent(cb: (e: HubEvent) => void): () => void
  onState(cb: (s: HubState) => void): () => void
  getState(): Promise<HubState>
}
export function toBase64(b: Uint8Array): string
export function fromBase64(s: string): Uint8Array
```

- [ ] **Step 1: Write the failing tests**

`desktop/test/ipc.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest'
import { relayCall, relayNotify } from '../src/main/ipc'

const fakeHub = () => ({ call: vi.fn(async () => ({ ok: true })), notify: vi.fn() })

describe('ipc whitelist', () => {
  it('relays whitelisted requests', async () => {
    const h = fakeHub()
    expect(await relayCall(h, 'servers', {})).toEqual({ ok: true })
    expect(h.call).toHaveBeenCalledWith('servers', {})
  })
  it('rejects anything else', async () => {
    const h = fakeHub()
    await expect(relayCall(h, 'exec', {})).rejects.toThrow('not allowed')
    await expect(relayCall(h, 42, {})).rejects.toThrow('not allowed')
    expect(h.call).not.toHaveBeenCalled()
  })
  it('relays only whitelisted notifications', () => {
    const h = fakeHub()
    relayNotify(h, 'term.write', { id: 'a', data: '' })
    relayNotify(h, 'unlock', { password: 'x' })
    expect(h.notify).toHaveBeenCalledTimes(1)
    expect(h.notify).toHaveBeenCalledWith('term.write', { id: 'a', data: '' })
  })
})
```

`desktop/test/transport.test.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest'

const bridge = {
  call: vi.fn(async () => ({})),
  notify: vi.fn(),
  getState: vi.fn(async () => ({ kind: 'running' })),
  onEvent: vi.fn(() => () => {}),
  onState: vi.fn(() => () => {}),
}
;(globalThis as any).window = { sshmcp: bridge }

const { hub, toBase64, fromBase64 } = await import('../src/renderer/transport')

describe('transport', () => {
  beforeEach(() => { bridge.call.mockClear(); bridge.notify.mockClear() })

  it('base64 round-trips arbitrary bytes, including invalid UTF-8', () => {
    const b = new Uint8Array([0, 255, 0xc3, 0x28, 10, 13, 0xe2, 0x82])
    expect(fromBase64(toBase64(b))).toEqual(b)
    expect(toBase64(new TextEncoder().encode('xin chào'))).toBe(Buffer.from('xin chào').toString('base64'))
  })

  it('termWrite sends base64 as a notification', () => {
    hub.termWrite('t1', new TextEncoder().encode('ls\r'))
    expect(bridge.notify).toHaveBeenCalledWith('term.write', { id: 't1', data: Buffer.from('ls\r').toString('base64') })
  })

  it('decide passes outcome and reason', async () => {
    await hub.decide('abc', 'denied', 'no')
    expect(bridge.call).toHaveBeenCalledWith('decide', { id: 'abc', outcome: 'denied', reason: 'no' })
  })

  it('termOpen sends the client-chosen id', async () => {
    await hub.termOpen('t2', 'box', 24, 80)
    expect(bridge.call).toHaveBeenCalledWith('term.open', { id: 't2', server: 'box', rows: 24, cols: 80 })
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm test -- ipc transport`
Expected: FAIL (modules not found).

- [ ] **Step 3: Implement**

`desktop/src/main/ipc.ts`:

```ts
import { BrowserWindow, ipcMain } from 'electron'
import { NOTIFY_METHODS, REQUEST_METHODS, type HubState } from '../shared/protocol'
import type { HubProcess } from './hubProcess'

export interface HubLike {
  call(method: string, params?: unknown, timeoutMs?: number): Promise<unknown>
  notify(method: string, params?: unknown): void
}

const requests = new Set<string>(REQUEST_METHODS)
const notifies = new Set<string>(NOTIFY_METHODS)

export function relayCall(hub: HubLike, method: unknown, params: unknown): Promise<unknown> {
  if (typeof method !== 'string' || !requests.has(method)) {
    return Promise.reject(new Error(`hub method not allowed: ${String(method)}`))
  }
  return hub.call(method, params ?? {})
}

export function relayNotify(hub: HubLike, method: unknown, params: unknown): void {
  if (typeof method === 'string' && notifies.has(method)) hub.notify(method, params ?? {})
}

export function registerIpc(hub: HubProcess, getWindow: () => BrowserWindow | undefined): void {
  ipcMain.handle('hub:call', (_e, method: unknown, params: unknown) => relayCall(hub, method, params))
  ipcMain.on('hub:notify', (_e, method: unknown, params: unknown) => relayNotify(hub, method, params))
  ipcMain.handle('hub:get-state', () => hub.state)
  hub.on('notification', (method: string, params: unknown) => {
    getWindow()?.webContents.send('hub:event', { method, params })
  })
  hub.on('state', (s: HubState) => getWindow()?.webContents.send('hub:state', s))
}
```

Note: `ipcMain.handle` rejects with the Error's message prefixed by Electron ("Error invoking remote method 'hub:call': Error: ..."). `transport.ts` strips that prefix so UI messages show only the hub's text (see `cleanError` below).

`desktop/src/preload/preload.ts`:

```ts
import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron'

contextBridge.exposeInMainWorld('sshmcp', {
  call: (method: string, params?: unknown) => ipcRenderer.invoke('hub:call', method, params),
  notify: (method: string, params?: unknown) => ipcRenderer.send('hub:notify', method, params),
  getState: () => ipcRenderer.invoke('hub:get-state'),
  onEvent: (cb: (e: unknown) => void) => {
    const h = (_: IpcRendererEvent, e: unknown) => cb(e)
    ipcRenderer.on('hub:event', h)
    return () => ipcRenderer.removeListener('hub:event', h)
  },
  onState: (cb: (s: unknown) => void) => {
    const h = (_: IpcRendererEvent, s: unknown) => cb(s)
    ipcRenderer.on('hub:state', h)
    return () => ipcRenderer.removeListener('hub:state', h)
  },
})
```

`desktop/src/renderer/transport.ts`:

```ts
import type { ApprovalRequest, HubEvent, HubState, ServerInfo } from '../shared/protocol'

interface Bridge {
  call(method: string, params?: unknown): Promise<unknown>
  notify(method: string, params?: unknown): void
  getState(): Promise<HubState>
  onEvent(cb: (e: HubEvent) => void): () => void
  onState(cb: (s: HubState) => void): () => void
}

const bridge = (): Bridge => (window as unknown as { sshmcp: Bridge }).sshmcp

// Electron prefixes invoke errors with "Error invoking remote method 'hub:call': Error: ".
export function cleanError(e: unknown): Error {
  const msg = e instanceof Error ? e.message : String(e)
  return new Error(msg.replace(/^Error invoking remote method '[^']+': (Error: )?/, ''))
}

async function call<T>(method: string, params?: unknown): Promise<T> {
  try {
    return (await bridge().call(method, params)) as T
  } catch (e) {
    throw cleanError(e)
  }
}

export function toBase64(b: Uint8Array): string {
  let s = ''
  for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000))
  return btoa(s)
}

export function fromBase64(s: string): Uint8Array {
  const bin = atob(s)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

export const hub = {
  hello: () => call<{ protocol: number }>('hello'),
  status: () => call<{ locked: boolean; hasStore: boolean; pending: number }>('status'),
  unlock: async (password: string) => { await call('unlock', { password }) },
  lock: async () => { await call('lock') },
  servers: () => call<ServerInfo[]>('servers'),
  pending: () => call<ApprovalRequest[]>('pending'),
  decide: async (id: string, outcome: 'allowed' | 'denied' | 'sent_to_tab', reason = '') => {
    await call('decide', { id, outcome, reason })
  },
  denyAll: async (reason = '') => { await call('denyAll', { reason }) },
  termOpen: async (id: string, server: string, rows: number, cols: number) => {
    await call('term.open', { id, server, rows, cols })
  },
  termClose: async (id: string) => { await call('term.close', { id }) },
  termWrite: (id: string, data: Uint8Array) => bridge().notify('term.write', { id, data: toBase64(data) }),
  termAck: (id: string, n: number) => bridge().notify('term.ack', { id, n }),
  termResize: (id: string, rows: number, cols: number) => bridge().notify('term.resize', { id, rows, cols }),
  onEvent: (cb: (e: HubEvent) => void) => bridge().onEvent(cb),
  onState: (cb: (s: HubState) => void) => bridge().onState(cb),
  getState: () => bridge().getState(),
}
```

`desktop/src/main/main.ts` — spawn the hub and register IPC:

```ts
import { app, BrowserWindow } from 'electron'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { HubProcess } from './hubProcess'
import { registerIpc } from './ipc'

let win: BrowserWindow | undefined

// Hub binary: SSH_MCP_BIN, else the repo-root build next to desktop/, else PATH.
function hubCommand(): string {
  if (process.env.SSH_MCP_BIN) return process.env.SSH_MCP_BIN
  const exe = process.platform === 'win32' ? 'ssh-mcp.exe' : 'ssh-mcp'
  const local = path.join(app.getAppPath(), '..', exe)
  return fs.existsSync(local) ? local : exe
}

function hubArgs(): string[] {
  const args = ['hub']
  if (process.env.SSH_MCP_STORE) args.push(`--store=${process.env.SSH_MCP_STORE}`)
  return args
}

const hub = new HubProcess({ command: hubCommand(), args: hubArgs(), env: process.env })

function createWindow(): BrowserWindow {
  const w = new BrowserWindow({
    width: 1200,
    height: 800,
    webPreferences: {
      preload: path.join(__dirname, '..', 'preload', 'preload.js'),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
    },
  })
  w.loadFile(path.join(__dirname, '..', 'renderer', 'index.html'))
  return w
}

app.whenReady().then(() => {
  win = createWindow()
  registerIpc(hub, () => win)
  hub.start()
})

let quitting = false
app.on('before-quit', (e) => {
  if (quitting) return
  e.preventDefault()
  quitting = true
  hub.stop().finally(() => app.quit())
})

app.on('window-all-closed', () => app.quit())
```

`SSH_MCP_STORE` is a development/test knob (the Playwright test uses it). Document it in README in Task 11.

- [ ] **Step 4: Run tests**

Run: `npm run typecheck && npm test`
Expected: PASS. The transport test uses top-level `await import` after installing `window`; if the Vitest environment rejects top-level await, move the import into a `beforeAll`.

- [ ] **Step 5: Commit**

```bash
git add desktop/src desktop/test
git commit -m "feat(desktop): whitelisted IPC relay, preload bridge, renderer transport"
```

---

### Task 6: Shell UI — hub state, unlock, host list, lock

**Files:**
- Create: `desktop/src/renderer/shell.ts`, `desktop/src/renderer/Unlock.tsx`, `desktop/src/renderer/HostList.tsx`, `desktop/test/shell.test.ts`
- Modify: `desktop/src/renderer/App.tsx`, `desktop/src/renderer/styles.css`

**Interfaces:**
- Produces (`shell.ts`, pure):

```ts
export type Screen =
  | { kind: 'hub'; state: HubState }          // hub not running: starting / restarting / failed
  | { kind: 'no-store' }                      // hub running, no vault yet
  | { kind: 'locked'; error?: string }
  | { kind: 'ready' }
export function screenFor(hub: HubState, status?: { locked: boolean; hasStore: boolean }, unlockError?: string): Screen
```

Rules: hub state not `running` → `{kind:'hub'}`; running with no status yet → `{kind:'hub', state: {kind:'starting'}}`; `!hasStore` → `no-store`; `locked` → `locked` (carrying `unlockError`); else `ready`.

- Produces components: `<Unlock onUnlock={(pw) => Promise<void>} error?: string />`, `<HostList servers onOpen={(name) => void} />`.
- `App` owns: hub state (from `hub.getState()` + `hub.onState`), status (fetched on `running`, on `locked` event, after unlock/lock), servers (fetched when `ready`), and passes `onOpen` to the terminals area (Task 7 fills it; here it is a no-op placeholder button handler that Task 7 replaces). A header shows a **Lock** button when ready.

- [ ] **Step 1: Write the failing test**

`desktop/test/shell.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { screenFor } from '../src/renderer/shell'

describe('screenFor', () => {
  it('shows the hub screen until the hub runs', () => {
    expect(screenFor({ kind: 'starting' })).toEqual({ kind: 'hub', state: { kind: 'starting' } })
    const failed = { kind: 'failed' as const, message: 'x', stderr: 'y' }
    expect(screenFor(failed, { locked: false, hasStore: true })).toEqual({ kind: 'hub', state: failed })
  })
  it('waits for status after the hub runs', () => {
    expect(screenFor({ kind: 'running' })).toEqual({ kind: 'hub', state: { kind: 'starting' } })
  })
  it('distinguishes no-store, locked, ready', () => {
    expect(screenFor({ kind: 'running' }, { locked: false, hasStore: false })).toEqual({ kind: 'no-store' })
    expect(screenFor({ kind: 'running' }, { locked: true, hasStore: true }, 'wrong master password'))
      .toEqual({ kind: 'locked', error: 'wrong master password' })
    expect(screenFor({ kind: 'running' }, { locked: false, hasStore: true })).toEqual({ kind: 'ready' })
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `npm test -- shell`
Expected: FAIL.

- [ ] **Step 3: Implement**

`desktop/src/renderer/shell.ts`:

```ts
import type { HubState } from '../shared/protocol'

export type Screen =
  | { kind: 'hub'; state: HubState }
  | { kind: 'no-store' }
  | { kind: 'locked'; error?: string }
  | { kind: 'ready' }

export function screenFor(
  hub: HubState,
  status?: { locked: boolean; hasStore: boolean },
  unlockError?: string,
): Screen {
  if (hub.kind !== 'running') return { kind: 'hub', state: hub }
  if (!status) return { kind: 'hub', state: { kind: 'starting' } }
  if (!status.hasStore) return { kind: 'no-store' }
  if (status.locked) return unlockError ? { kind: 'locked', error: unlockError } : { kind: 'locked' }
  return { kind: 'ready' }
}
```

`desktop/src/renderer/Unlock.tsx`:

```tsx
import { useState, type FormEvent } from 'react'

export function Unlock({ onUnlock, error }: { onUnlock: (pw: string) => Promise<void>; error?: string }) {
  const [pw, setPw] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try { await onUnlock(pw) } finally { setBusy(false); setPw('') }
  }
  return (
    <form className="unlock" onSubmit={submit}>
      <h2>Unlock vault</h2>
      <input type="password" autoFocus aria-label="Master password" value={pw}
        onChange={(e) => setPw(e.target.value)} disabled={busy} />
      <button type="submit" disabled={busy || pw === ''}>Unlock</button>
      {error && <p className="error">{error}</p>}
    </form>
  )
}
```

`desktop/src/renderer/HostList.tsx`:

```tsx
import type { ServerInfo } from '../shared/protocol'

export function HostList({ servers, onOpen }: { servers: ServerInfo[]; onOpen: (name: string) => void }) {
  return (
    <nav className="hosts">
      <h3>Servers</h3>
      <ul>
        {servers.map((s) => (
          <li key={s.name}>
            <button onClick={() => onOpen(s.name)} title={`${s.user}@${s.host}:${s.port}`}>
              {s.name}
            </button>
            {s.aiVisible && <span className="badge" title="Visible to AI">AI</span>}
            {!s.hostKey && <span className="badge warn" title="Host key not pinned yet">new</span>}
          </li>
        ))}
      </ul>
    </nav>
  )
}
```

`desktop/src/renderer/App.tsx`:

```tsx
import { useCallback, useEffect, useState } from 'react'
import type { HubState, ServerInfo } from '../shared/protocol'
import { hub } from './transport'
import { screenFor } from './shell'
import { Unlock } from './Unlock'
import { HostList } from './HostList'

export function App() {
  const [hubState, setHubState] = useState<HubState>({ kind: 'starting' })
  const [status, setStatus] = useState<{ locked: boolean; hasStore: boolean }>()
  const [unlockError, setUnlockError] = useState<string>()
  const [servers, setServers] = useState<ServerInfo[]>([])

  const refresh = useCallback(async () => {
    try { setStatus(await hub.status()) } catch { setStatus(undefined) }
  }, [])

  useEffect(() => {
    hub.getState().then(setHubState)
    const offState = hub.onState(setHubState)
    const offEvent = hub.onEvent((e) => { if (e.method === 'locked') refresh() })
    return () => { offState(); offEvent() }
  }, [refresh])

  useEffect(() => {
    if (hubState.kind === 'running') refresh()
    else setStatus(undefined)
  }, [hubState, refresh])

  const screen = screenFor(hubState, status, unlockError)

  useEffect(() => {
    if (screen.kind === 'ready') hub.servers().then(setServers).catch(() => setServers([]))
  }, [screen.kind])

  const unlock = async (pw: string) => {
    try { await hub.unlock(pw); setUnlockError(undefined) } catch (e) { setUnlockError((e as Error).message) }
    await refresh()
  }

  switch (screen.kind) {
    case 'hub':
      return <HubScreen state={screen.state} />
    case 'no-store':
      return <div className="center"><p>No vault yet. Run <code>ssh-mcp web</code> to add servers, then restart the app.</p></div>
    case 'locked':
      return <div className="center"><Unlock onUnlock={unlock} error={screen.error} /></div>
    case 'ready':
      return (
        <div className="layout">
          <header>
            <span>ssh-mcp</span>
            <button onClick={async () => { await hub.lock(); await refresh() }}>Lock</button>
          </header>
          <HostList servers={servers} onOpen={() => {}} />
          <main className="work" />
        </div>
      )
  }
}

function HubScreen({ state }: { state: HubState }) {
  if (state.kind === 'failed') {
    return (
      <div className="center">
        <h2>The hub stopped</h2>
        <p>{state.message}</p>
        <pre className="stderr">{state.stderr}</pre>
      </div>
    )
  }
  if (state.kind === 'restarting') {
    return <div className="center"><p>Hub crashed; restarting in {Math.round(state.inMs / 1000)} s (attempt {state.attempt}).</p></div>
  }
  return <div className="center"><p>Starting the hub…</p></div>
}
```

Append to `styles.css`:

```css
.center { height: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; }
.layout { height: 100%; display: grid; grid-template-rows: 36px 1fr; grid-template-columns: 200px 1fr 360px; }
.layout > header { grid-column: 1 / 4; display: flex; justify-content: space-between; align-items: center; padding: 0 12px; background: #252526; }
.hosts { overflow: auto; background: #252526; border-right: 1px solid #333; }
.hosts ul { list-style: none; padding: 0 8px; margin: 0; }
.hosts li { display: flex; gap: 6px; align-items: center; margin: 4px 0; }
.hosts button { flex: 1; text-align: left; background: none; color: inherit; border: 0; cursor: pointer; padding: 4px; }
.badge { font-size: 10px; padding: 1px 4px; border-radius: 3px; background: #0e639c; }
.badge.warn { background: #8a6d00; }
.work { overflow: hidden; }
.unlock { display: flex; flex-direction: column; gap: 8px; width: 280px; }
.error { color: #f48771; }
.stderr { max-width: 80%; max-height: 50%; overflow: auto; background: #111; padding: 8px; }
```

- [ ] **Step 4: Run tests and a manual check**

Run: `npm run typecheck && npm test && npm run build`
Manual: build the hub (`go build -o ssh-mcp ./cmd/ssh-mcp` at the repo root), start `go run ./internal/sshx/sshtest/sshtestd -write-store=/tmp/sm-store/servers.json -password=pw` in another terminal, then in `desktop/`: `SSH_MCP_STORE=/tmp/sm-store/servers.json SSH_MCP_RUNTIME_DIR=$(mktemp -d /tmp/smXXXX) npx electron .` — the unlock screen appears; `pw` unlocks; the list shows `box` with an `AI` badge; Lock returns to the unlock screen. Record the result (or "no display") in the report.

- [ ] **Step 5: Commit**

```bash
git add desktop/src desktop/test
git commit -m "feat(desktop): hub status, unlock screen, host list, lock"
```

---

### Task 7: Terminal tabs

**Files:**
- Create: `desktop/src/renderer/terminals.ts`, `desktop/src/renderer/TermView.tsx`, `desktop/src/renderer/Terminals.tsx`, `desktop/test/terminals.test.ts`
- Modify: `desktop/src/renderer/App.tsx` (render `<Terminals>` in `.work`, wire `onOpen`), `desktop/src/renderer/styles.css`

**Interfaces:**
- Produces (`terminals.ts`, pure and testable):

```ts
export function newTermId(): string                        // "t-" + 16 hex chars from crypto.getRandomValues
export interface Tab { id: string; server: string; state: 'opening' | 'open' | 'exited'; exitReason?: string; sawOutput: boolean }
export class TabSet {
  tabs: Tab[]; active?: string
  open(server: string): Tab                                  // appends an 'opening' tab and makes it active
  opened(id: string): void                                   // opening -> open
  output(id: string): void                                   // sawOutput = true
  exited(id: string, reason: string): void                   // -> exited
  close(id: string): void                                    // removes; active moves to the previous tab
  mostRecentFor(server: string): Tab | undefined            // most recently activated non-exited tab for server
  activate(id: string): void
}
export class Debouncer { constructor(ms: number, fn: () => void); poke(): void; cancel(): void }
```

`TabSet` records activation order to answer `mostRecentFor`. Components re-render via a version counter (`useState` holding a number bumped after each mutation).

- Produces (`Terminals` component props): `{ server events via hub.onEvent; tabs: TabSet; onChange(): void }` plus an imperative handle used by the approval panel (Task 8):

```ts
export interface TerminalsHandle {
  open(server: string): void
  sendToTab(server: string, text: string): Promise<void>   // most recent tab for server, or a new one; pastes after first output
}
```

TermView behaviour (one xterm per tab, kept mounted while the tab exists so scrollback survives tab switches; hidden with CSS when inactive):
- On mount: create `Terminal({ convertEol: false, fontFamily: 'Menlo, Consolas, monospace', fontSize: 13 })` and `FitAddon`; `term.open(div)`; `fit.fit()`; call `hub.termOpen(id, server, term.rows, term.cols)`; mark opened on success; on failure mark exited with the error message.
- Until the `term.open` reply arrives, keystrokes are dropped (spec: send nothing before the reply) — gate `onData` on `tab.state === 'open'`.
- `term.onData(s => hub.termWrite(id, new TextEncoder().encode(s)))`.
- On `term.data` for this id: `const bytes = fromBase64(data); term.write(bytes, () => hub.termAck(id, bytes.length)); tabs.output(id)`.
- On `term.exit` for this id: write `\r\n[exited: <reason or code>]\r\n`, mark exited; show a **Reconnect** button that closes this tab and opens a new one for the same server.
- On `term.dropped`: write `\r\n[input dropped: <bytes> bytes]\r\n`.
- `ResizeObserver` on the container → `Debouncer(50ms)` → `fit.fit()` then `hub.termResize(id, term.rows, term.cols)` when open.
- On unmount: `hub.termClose(id).catch(() => {})`, dispose xterm.
- On hub state leaving `running`: every tab becomes exited with reason `hub restarted`.
- Events for unknown ids are ignored.

- [ ] **Step 1: Write the failing tests**

`desktop/test/terminals.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest'
import { Debouncer, newTermId, TabSet } from '../src/renderer/terminals'

describe('newTermId', () => {
  it('matches the hub id rules and is unique', () => {
    const a = newTermId(), b = newTermId()
    expect(a).toMatch(/^[A-Za-z0-9_-]{1,64}$/)
    expect(a).not.toBe(b)
  })
})

describe('TabSet', () => {
  it('tracks lifecycle and activation order', () => {
    const t = new TabSet()
    const a = t.open('box')
    const b = t.open('box')
    const c = t.open('other')
    expect(t.active).toBe(c.id)
    t.activate(a.id)
    expect(t.mostRecentFor('box')?.id).toBe(a.id)
    t.opened(a.id); t.output(a.id)
    expect(t.tabs.find((x) => x.id === a.id)).toMatchObject({ state: 'open', sawOutput: true })
    t.exited(a.id, 'bye')
    expect(t.mostRecentFor('box')?.id).toBe(b.id)
    t.activate(c.id)
    t.close(c.id)
    expect(t.tabs.map((x) => x.id)).toEqual([a.id, b.id])
    expect(t.active).toBe(a.id) // previous in activation order
  })
})

describe('Debouncer', () => {
  it('fires once after quiet time', async () => {
    vi.useFakeTimers()
    const fn = vi.fn()
    const d = new Debouncer(50, fn)
    d.poke(); vi.advanceTimersByTime(30); d.poke(); vi.advanceTimersByTime(30)
    expect(fn).not.toHaveBeenCalled()
    vi.advanceTimersByTime(30)
    expect(fn).toHaveBeenCalledTimes(1)
    vi.useRealTimers()
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `npm test -- terminals`
Expected: FAIL.

- [ ] **Step 3: Implement `terminals.ts`**

```ts
export function newTermId(): string {
  const b = new Uint8Array(8)
  crypto.getRandomValues(b)
  return 't-' + Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

export interface Tab {
  id: string
  server: string
  state: 'opening' | 'open' | 'exited'
  exitReason?: string
  sawOutput: boolean
}

export class TabSet {
  tabs: Tab[] = []
  active?: string
  private order: string[] = [] // most recent last

  open(server: string): Tab {
    const tab: Tab = { id: newTermId(), server, state: 'opening', sawOutput: false }
    this.tabs.push(tab)
    this.activate(tab.id)
    return tab
  }
  private find(id: string) { return this.tabs.find((t) => t.id === id) }
  opened(id: string) { const t = this.find(id); if (t && t.state === 'opening') t.state = 'open' }
  output(id: string) { const t = this.find(id); if (t) t.sawOutput = true }
  exited(id: string, reason: string) { const t = this.find(id); if (t) { t.state = 'exited'; t.exitReason = reason } }
  activate(id: string) {
    this.active = id
    this.order = this.order.filter((x) => x !== id).concat(id)
  }
  close(id: string) {
    this.tabs = this.tabs.filter((t) => t.id !== id)
    this.order = this.order.filter((x) => x !== id)
    if (this.active === id) this.active = this.order[this.order.length - 1]
  }
  mostRecentFor(server: string): Tab | undefined {
    for (let i = this.order.length - 1; i >= 0; i--) {
      const t = this.find(this.order[i])
      if (t && t.server === server && t.state !== 'exited') return t
    }
    return undefined
  }
}

export class Debouncer {
  private timer?: ReturnType<typeof setTimeout>
  constructor(private readonly ms: number, private readonly fn: () => void) {}
  poke() { if (this.timer) clearTimeout(this.timer); this.timer = setTimeout(this.fn, this.ms) }
  cancel() { if (this.timer) clearTimeout(this.timer) }
}
```

- [ ] **Step 4: Implement `TermView.tsx` and `Terminals.tsx`**

`TermView.tsx`:

```tsx
import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { hub, fromBase64 } from './transport'
import { Debouncer, type Tab, type TabSet } from './terminals'

export interface TermApi { paste(text: string): void }

export function TermView({ tab, tabs, visible, onChange, register }: {
  tab: Tab; tabs: TabSet; visible: boolean; onChange: () => void
  register: (id: string, api: TermApi | undefined) => void
}) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = ref.current!
    const term = new Terminal({ fontFamily: 'Menlo, Consolas, monospace', fontSize: 13 })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(el)
    fit.fit()
    const enc = new TextEncoder()
    let open = false

    register(tab.id, { paste: (text) => term.paste(text) })

    hub.termOpen(tab.id, tab.server, term.rows, term.cols).then(
      () => { open = true; tabs.opened(tab.id); onChange() },
      (e) => { term.write(`\r\n[open failed: ${(e as Error).message}]\r\n`); tabs.exited(tab.id, (e as Error).message); onChange() },
    )

    const dataSub = term.onData((s) => { if (open) hub.termWrite(tab.id, enc.encode(s)) })
    const off = hub.onEvent((e) => {
      if (!('params' in e) || (e.params as { id?: string }).id !== tab.id) return
      if (e.method === 'term.data') {
        const bytes = fromBase64(e.params.data)
        term.write(bytes, () => hub.termAck(tab.id, bytes.length))
        if (!tab.sawOutput) { tabs.output(tab.id); onChange() }
      } else if (e.method === 'term.exit') {
        open = false
        term.write(`\r\n[exited: ${e.params.reason || `code ${e.params.code}`}]\r\n`)
        tabs.exited(tab.id, e.params.reason || `code ${e.params.code}`)
        onChange()
      } else if (e.method === 'term.dropped') {
        term.write(`\r\n[input dropped: ${e.params.bytes} bytes]\r\n`)
      }
    })
    const offState = hub.onState((s) => {
      if (s.kind !== 'running' && open) {
        open = false
        term.write('\r\n[exited: hub restarted]\r\n')
        tabs.exited(tab.id, 'hub restarted')
        onChange()
      }
    })
    const resize = new Debouncer(50, () => {
      fit.fit()
      if (open) hub.termResize(tab.id, term.rows, term.cols)
    })
    const ro = new ResizeObserver(() => resize.poke())
    ro.observe(el)

    return () => {
      ro.disconnect(); resize.cancel(); dataSub.dispose(); off(); offState()
      register(tab.id, undefined)
      hub.termClose(tab.id).catch(() => {})
      term.dispose()
    }
    // tab identity is fixed for the life of this component
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return <div className="term" ref={ref} style={{ display: visible ? 'block' : 'none' }} />
}
```

`Terminals.tsx`:

```tsx
import { forwardRef, useImperativeHandle, useRef, useState } from 'react'
import { TabSet } from './terminals'
import { TermView, type TermApi } from './TermView'

export interface TerminalsHandle {
  open(server: string): void
  sendToTab(server: string, text: string): Promise<void>
}

export const Terminals = forwardRef<TerminalsHandle>(function Terminals(_props, ref) {
  const tabs = useRef(new TabSet()).current
  const apis = useRef(new Map<string, TermApi>()).current
  const [, setVersion] = useState(0)
  const changed = () => setVersion((v) => v + 1)
  const register = (id: string, api: TermApi | undefined) => { if (api) apis.set(id, api); else apis.delete(id) }

  const waitReady = (id: string) => new Promise<void>((resolve, reject) => {
    const started = Date.now()
    const check = () => {
      const t = tabs.tabs.find((x) => x.id === id)
      if (!t || t.state === 'exited') return reject(new Error('terminal closed'))
      if (t.state === 'open' && t.sawOutput && apis.has(id)) return resolve()
      if (Date.now() - started > 15000) return reject(new Error('terminal did not become ready'))
      setTimeout(check, 50)
    }
    check()
  })

  useImperativeHandle(ref, () => ({
    open(server) { tabs.open(server); changed() },
    async sendToTab(server, text) {
      let tab = tabs.mostRecentFor(server)
      if (!tab) { tab = tabs.open(server); changed() }
      tabs.activate(tab.id); changed()
      await waitReady(tab.id)
      apis.get(tab.id)!.paste(text)
    },
  }))

  return (
    <div className="terms">
      <div className="tabbar">
        {tabs.tabs.map((t) => (
          <div key={t.id} className={'tab' + (t.id === tabs.active ? ' active' : '')}>
            <button onClick={() => { tabs.activate(t.id); changed() }}>
              {t.server}{t.state === 'exited' ? ' (exited)' : ''}
            </button>
            {t.state === 'exited' && (
              <button title="Reconnect" onClick={() => { tabs.close(t.id); tabs.open(t.server); changed() }}>↻</button>
            )}
            <button title="Close" onClick={() => { tabs.close(t.id); changed() }}>×</button>
          </div>
        ))}
      </div>
      <div className="termarea">
        {tabs.tabs.map((t) => (
          <TermView key={t.id} tab={t} tabs={tabs} visible={t.id === tabs.active} onChange={changed} register={register} />
        ))}
      </div>
    </div>
  )
})
```

`App.tsx`: in the `ready` branch, create `const terms = useRef<TerminalsHandle>(null)` at the top of `App` (hooks must not be conditional), render `<main className="work"><Terminals ref={terms} /></main>`, and pass `onOpen={(name) => terms.current?.open(name)}` to `HostList`.

Append to `styles.css`:

```css
.terms { height: 100%; display: flex; flex-direction: column; }
.tabbar { display: flex; gap: 2px; background: #2d2d2d; overflow-x: auto; }
.tab { display: flex; align-items: center; background: #333; }
.tab.active { background: #1e1e1e; }
.tab button { background: none; color: inherit; border: 0; padding: 6px 8px; cursor: pointer; }
.termarea { flex: 1; position: relative; }
.term { position: absolute; inset: 0; padding: 4px; }
```

- [ ] **Step 5: Run tests and a manual check**

Run: `npm run typecheck && npm test && npm run build`
Manual (same setup as Task 6): click `box` → a tab opens; typing `echo hi` echoes (sshtestd's shell echoes input back); resize the window — no errors in DevTools console; close the tab. Record the result (or "no display").

- [ ] **Step 6: Commit**

```bash
git add desktop/src desktop/test
git commit -m "feat(desktop): terminal tabs with xterm.js, ack flow control, resize, reconnect"
```

---

### Task 8: Approval panel

**Files:**
- Create: `desktop/src/renderer/approvals.ts`, `desktop/src/renderer/ApprovalPanel.tsx`, `desktop/test/approvals.test.ts`
- Modify: `desktop/src/renderer/App.tsx`, `desktop/src/renderer/styles.css`

**Interfaces:**
- Produces (`approvals.ts`, pure):

```ts
export interface PendingItem { request: ApprovalRequest; shownAt: number }
export function reduceApprovals(items: PendingItem[], e: HubEvent, now: number): PendingItem[]
  // 'pending' → append if id not present; 'decided' → remove by id (any outcome, incl. expired/withdrawn); 'locked' → unchanged; others → unchanged
export function seed(requests: ApprovalRequest[], now: number): PendingItem[]
export const ALLOW_DELAY_MS = 500
export function allowEnabled(item: PendingItem, now: number): boolean   // now - shownAt >= 500
export type Segment = { text: string; nonAscii: boolean }
export function highlightNonAscii(s: string): Segment[]                 // splits into runs of ASCII / non-ASCII code points
```

- Produces `<ApprovalPanel items servers onDecide onDenyAll onSendToTab />`:
  - `onDecide(id, 'allowed' | 'denied', reason)`; `onDenyAll()`; `onSendToTab(item)`.
  - Rendering per item: server name and host (`user@host:port` from `servers`), `[SUDO]` badge if `sudo`, the command in a `<pre>` with non-ASCII segments wrapped in `<mark>`, `timeout: Ns`, `AI's description (unverified): …` as plain text, `client: <name> (unverified)`, received time.
  - Buttons in this DOM order: **Deny** (`type="submit"` of a per-item `<form>`; the reason `<input>` is inside the form so Enter in it submits Deny), **Allow** (`type="button"`, disabled until `allowEnabled`, no `accessKey`), **Send to tab** (`type="button"`).
  - A **Deny all** button when 2+ items.
  - The panel never calls `.focus()` and uses no `autoFocus`. Keyboard focus stays in the terminal until the user clicks the panel.
  - A 100 ms re-render ticker while any item is younger than 500 ms, so Allow enables itself.

`App` keeps `items` via `reduceApprovals` on every hub event, seeds from `hub.pending()` whenever the screen becomes `ready`, and clears them when the hub is not running. `onSendToTab(item)`: `await terms.current.sendToTab(item.request.server, item.request.command)` then `hub.decide(id, 'sent_to_tab')`; on error show it inline and leave the request pending.

- [ ] **Step 1: Write the failing tests**

`desktop/test/approvals.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { allowEnabled, highlightNonAscii, reduceApprovals, seed } from '../src/renderer/approvals'
import type { ApprovalRequest } from '../src/shared/protocol'

const req = (id: string): ApprovalRequest => ({
  id, client: 'claude-code', server: 'box', command: 'ls', description: '', sudo: false, timeoutSec: 60, receivedAt: '',
})

describe('reduceApprovals', () => {
  it('adds pending once and removes on any decided outcome', () => {
    let s = seed([], 0)
    s = reduceApprovals(s, { method: 'pending', params: { request: req('a') } }, 10)
    s = reduceApprovals(s, { method: 'pending', params: { request: req('a') } }, 20)
    s = reduceApprovals(s, { method: 'pending', params: { request: req('b') } }, 30)
    expect(s.map((x) => x.request.id)).toEqual(['a', 'b'])
    for (const outcome of ['expired', 'withdrawn'] as const) {
      s = reduceApprovals(s, { method: 'decided', params: { request: req(s[0].request.id), decision: { outcome, reason: '' } } }, 40)
    }
    expect(s).toEqual([])
  })
})

describe('allowEnabled', () => {
  it('is false for the first 500 ms', () => {
    const [item] = seed([req('a')], 1000)
    expect(allowEnabled(item, 1499)).toBe(false)
    expect(allowEnabled(item, 1500)).toBe(true)
  })
})

describe('highlightNonAscii', () => {
  it('marks non-ASCII runs, including homoglyphs', () => {
    expect(highlightNonAscii('rm -rf /tmp/а')).toEqual([
      { text: 'rm -rf /tmp/', nonAscii: false },
      { text: 'а', nonAscii: true },
    ])
    expect(highlightNonAscii('ls')).toEqual([{ text: 'ls', nonAscii: false }])
    expect(highlightNonAscii('')).toEqual([])
  })
})
```

(The `а` above is Cyrillic U+0430.)

- [ ] **Step 2: Run to verify it fails**

Run: `npm test -- approvals`
Expected: FAIL.

- [ ] **Step 3: Implement**

`approvals.ts`:

```ts
import type { ApprovalRequest, HubEvent } from '../shared/protocol'

export interface PendingItem { request: ApprovalRequest; shownAt: number }
export const ALLOW_DELAY_MS = 500

export function seed(requests: ApprovalRequest[], now: number): PendingItem[] {
  return requests.map((request) => ({ request, shownAt: now }))
}

export function reduceApprovals(items: PendingItem[], e: HubEvent, now: number): PendingItem[] {
  if (e.method === 'pending') {
    if (items.some((i) => i.request.id === e.params.request.id)) return items
    return [...items, { request: e.params.request, shownAt: now }]
  }
  if (e.method === 'decided') return items.filter((i) => i.request.id !== e.params.request.id)
  return items
}

export function allowEnabled(item: PendingItem, now: number): boolean {
  return now - item.shownAt >= ALLOW_DELAY_MS
}

export type Segment = { text: string; nonAscii: boolean }

export function highlightNonAscii(s: string): Segment[] {
  const out: Segment[] = []
  for (const ch of s) {
    const nonAscii = ch.codePointAt(0)! > 0x7e || ch.codePointAt(0)! < 0x20
    const last = out[out.length - 1]
    if (last && last.nonAscii === nonAscii) last.text += ch
    else out.push({ text: ch, nonAscii })
  }
  return out
}
```

`ApprovalPanel.tsx`:

```tsx
import { useEffect, useState, type FormEvent } from 'react'
import type { ServerInfo } from '../shared/protocol'
import { allowEnabled, highlightNonAscii, type PendingItem } from './approvals'

export function ApprovalPanel({ items, servers, onDecide, onDenyAll, onSendToTab }: {
  items: PendingItem[]
  servers: ServerInfo[]
  onDecide: (id: string, outcome: 'allowed' | 'denied', reason: string) => Promise<void>
  onDenyAll: () => Promise<void>
  onSendToTab: (item: PendingItem) => Promise<void>
}) {
  const [now, setNow] = useState(Date.now())
  const young = items.some((i) => !allowEnabled(i, now))
  useEffect(() => {
    if (!young) return
    const t = setInterval(() => setNow(Date.now()), 100)
    return () => clearInterval(t)
  }, [young])

  return (
    <aside className="approvals" aria-label="Approval requests">
      <h3>AI requests {items.length > 0 && `(${items.length})`}</h3>
      {items.length > 1 && <button className="denyall" onClick={() => onDenyAll()}>Deny all</button>}
      {items.length === 0 && <p className="muted">Nothing waiting.</p>}
      {items.map((item) => (
        <Item key={item.request.id} item={item} now={now}
          server={servers.find((s) => s.name === item.request.server)}
          onDecide={onDecide} onSendToTab={onSendToTab} />
      ))}
    </aside>
  )
}

function Item({ item, now, server, onDecide, onSendToTab }: {
  item: PendingItem; now: number; server?: ServerInfo
  onDecide: (id: string, outcome: 'allowed' | 'denied', reason: string) => Promise<void>
  onSendToTab: (item: PendingItem) => Promise<void>
}) {
  const r = item.request
  const [reason, setReason] = useState('')
  const [error, setError] = useState<string>()
  const act = (p: Promise<void>) => p.catch((e) => setError((e as Error).message))
  const deny = (e: FormEvent) => { e.preventDefault(); act(onDecide(r.id, 'denied', reason)) }
  return (
    <form className="approval" onSubmit={deny}>
      <div className="who">
        <strong>{r.server}</strong>
        {server && <span className="muted"> {server.user}@{server.host}:{server.port}</span>}
        {r.sudo && <span className="badge warn">SUDO</span>}
      </div>
      <pre className="cmd">
        {highlightNonAscii(r.command).map((s, i) => (s.nonAscii ? <mark key={i}>{s.text}</mark> : <span key={i}>{s.text}</span>))}
      </pre>
      <div className="muted">timeout: {r.timeoutSec}s</div>
      {r.description && <div className="desc">AI's description (unverified): {r.description}</div>}
      <div className="muted">client: {r.client} (unverified) · {new Date(r.receivedAt).toLocaleTimeString()}</div>
      <input placeholder="Reason (optional)" value={reason} onChange={(e) => setReason(e.target.value)} />
      <div className="actions">
        <button type="submit" className="deny">Deny</button>
        <button type="button" className="allow" disabled={!allowEnabled(item, now)}
          onClick={() => act(onDecide(r.id, 'allowed', ''))}>Allow</button>
        <button type="button" onClick={() => act(onSendToTab(item))}>Send to tab</button>
      </div>
      {error && <p className="error">{error}</p>}
    </form>
  )
}
```

`App.tsx` additions (hooks at the top level of `App`):

```tsx
const [items, setItems] = useState<PendingItem[]>([])
useEffect(() => hub.onEvent((e) => setItems((cur) => reduceApprovals(cur, e, Date.now()))), [])
useEffect(() => {
  if (screen.kind === 'ready') hub.pending().then((p) => setItems(seed(p, Date.now()))).catch(() => {})
  if (hubState.kind !== 'running') setItems([])
}, [screen.kind, hubState.kind])
```

and in the `ready` layout, after `<main>`:

```tsx
<ApprovalPanel items={items} servers={servers}
  onDecide={(id, outcome, reason) => hub.decide(id, outcome, reason)}
  onDenyAll={() => hub.denyAll('denied all by user')}
  onSendToTab={async (item) => {
    await terms.current!.sendToTab(item.request.server, item.request.command)
    await hub.decide(item.request.id, 'sent_to_tab')
  }} />
```

Note that the approval requests are for AI-visible servers and arrive while the vault is unlocked, but they can also arrive while the screen shows `locked` (the hub rejects AI execs on a locked vault before they become pending, so in practice none will); the panel only renders in `ready`.

Append to `styles.css`:

```css
.approvals { overflow: auto; background: #252526; border-left: 1px solid #333; padding: 0 8px; }
.approval { border: 1px solid #444; border-radius: 4px; padding: 8px; margin: 8px 0; display: flex; flex-direction: column; gap: 6px; }
.cmd { white-space: pre-wrap; word-break: break-all; background: #111; padding: 6px; margin: 0; font-family: Menlo, Consolas, monospace; }
.cmd mark { background: #b8860b; color: #000; }
.desc { font-size: 12px; font-style: italic; color: #aaa; }
.muted { color: #888; font-size: 12px; }
.actions { display: flex; gap: 6px; }
.actions .deny { background: #5a1d1d; color: #fff; }
.actions .allow { background: #1d5a2a; color: #fff; }
.actions .allow:disabled { opacity: 0.4; }
```

- [ ] **Step 4: Run tests and a manual check**

Run: `npm run typecheck && npm test && npm run build`
Manual (Task 6 setup, app unlocked): send a request to the MCP door from a shell, using the socket under your `SSH_MCP_RUNTIME_DIR`:

```bash
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"exec","params":{"requestId":"r1","client":"manual","server":"box","command":"echo hi","description":"test"}}' | nc -U "$SSH_MCP_RUNTIME_DIR/ssh-mcp/hub.sock"
```

The panel shows the request; Allow is disabled for half a second; Allow returns `{"exitCode":0,"stdout":"echo hi"...}` to `nc`. Repeat and press Enter in the reason field: the request is denied. Record the results.

- [ ] **Step 5: Commit**

```bash
git add desktop/src desktop/test
git commit -m "feat(desktop): non-modal approval panel with delayed Allow and send-to-tab"
```

---

### Task 9: Tray count and OS notifications

**Files:**
- Create: `desktop/src/main/attention.ts`, `desktop/test/attention.test.ts`
- Modify: `desktop/src/main/main.ts`

**Interfaces:**
- Produces (`attention.ts`):

```ts
export function trayTitle(count: number): string              // '' when 0, else String(count)
export function trayTooltip(count: number): string            // 'ssh-mcp' | 'ssh-mcp: 1 request waiting' | 'ssh-mcp: N requests waiting'
export function notificationText(req: ApprovalRequest): { title: string; body: string }
  // title 'AI wants to run a command' (or '… with sudo'), body `${server}: ${command}` truncated to 120 chars with '…'
export class PendingCounter { apply(method: string, params: unknown): number; get count(): number; reset(): void }
export function setupAttention(hub: HubProcess, getWindow: () => BrowserWindow | undefined): void
```

`PendingCounter` tracks ids from `pending`/`decided` notifications. `setupAttention` creates a `Tray` with a 16×16 PNG icon built from a base64 constant in the file (a simple filled circle; generate the PNG bytes once with any tool and paste the base64), sets `setTitle` (macOS only; guarded by `process.platform === 'darwin'`) and `setToolTip` on every change, with a context menu `Show` / `Quit`. On `pending`, if the window is missing or not focused and `Notification.isSupported()`, show a `Notification(notificationText(req))`; its `click` handler restores, shows and focuses the window. On hub state other than `running`, reset the counter.

- [ ] **Step 1: Write the failing test**

`desktop/test/attention.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest'

vi.mock('electron', () => ({}))
const { notificationText, PendingCounter, trayTitle, trayTooltip } = await import('../src/main/attention')

const req = { id: 'a', client: 'c', server: 'box', command: 'x'.repeat(200), description: '', sudo: true, timeoutSec: 60, receivedAt: '' }

describe('attention', () => {
  it('formats tray text', () => {
    expect(trayTitle(0)).toBe('')
    expect(trayTitle(3)).toBe('3')
    expect(trayTooltip(0)).toBe('ssh-mcp')
    expect(trayTooltip(1)).toBe('ssh-mcp: 1 request waiting')
    expect(trayTooltip(2)).toBe('ssh-mcp: 2 requests waiting')
  })
  it('formats notifications and truncates', () => {
    const n = notificationText(req)
    expect(n.title).toBe('AI wants to run a command with sudo')
    expect(n.body.startsWith('box: xxx')).toBe(true)
    expect(n.body.length).toBeLessThanOrEqual(121)
    expect(n.body.endsWith('…')).toBe(true)
  })
  it('counts pending by id', () => {
    const c = new PendingCounter()
    c.apply('pending', { request: { id: 'a' } })
    c.apply('pending', { request: { id: 'a' } })
    c.apply('pending', { request: { id: 'b' } })
    expect(c.count).toBe(2)
    c.apply('decided', { request: { id: 'a' } })
    expect(c.count).toBe(1)
    c.apply('term.data', { id: 'x' })
    expect(c.count).toBe(1)
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `npm test -- attention`
Expected: FAIL.

- [ ] **Step 3: Implement**

```ts
import { app, BrowserWindow, Menu, nativeImage, Notification, Tray } from 'electron'
import type { ApprovalRequest, HubState } from '../shared/protocol'
import type { HubProcess } from './hubProcess'

// 16x16 PNG, a filled circle. Paste the base64 of a real PNG here.
const ICON_PNG_BASE64 = '<base64 of a 16x16 PNG generated in Step 3>'

export function trayTitle(count: number): string { return count === 0 ? '' : String(count) }

export function trayTooltip(count: number): string {
  if (count === 0) return 'ssh-mcp'
  return `ssh-mcp: ${count} request${count === 1 ? '' : 's'} waiting`
}

export function notificationText(req: ApprovalRequest): { title: string; body: string } {
  const title = req.sudo ? 'AI wants to run a command with sudo' : 'AI wants to run a command'
  const full = `${req.server}: ${req.command}`
  return { title, body: full.length > 120 ? full.slice(0, 120) + '…' : full }
}

export class PendingCounter {
  private ids = new Set<string>()
  get count(): number { return this.ids.size }
  reset(): void { this.ids.clear() }
  apply(method: string, params: unknown): number {
    const id = (params as { request?: { id?: string } } | undefined)?.request?.id
    if (id && method === 'pending') this.ids.add(id)
    if (id && method === 'decided') this.ids.delete(id)
    return this.ids.size
  }
}

export function setupAttention(hub: HubProcess, getWindow: () => BrowserWindow | undefined): void {
  const tray = new Tray(nativeImage.createFromDataURL(`data:image/png;base64,${ICON_PNG_BASE64}`))
  const show = () => {
    const w = getWindow()
    if (!w) return
    if (w.isMinimized()) w.restore()
    w.show()
    w.focus()
  }
  tray.setContextMenu(Menu.buildFromTemplate([{ label: 'Show', click: show }, { label: 'Quit', click: () => app.quit() }]))
  const counter = new PendingCounter()
  const render = () => {
    if (process.platform === 'darwin') tray.setTitle(trayTitle(counter.count))
    tray.setToolTip(trayTooltip(counter.count))
  }
  render()
  hub.on('notification', (method: string, params: unknown) => {
    counter.apply(method, params)
    render()
    if (method !== 'pending') return
    const w = getWindow()
    if (w && w.isFocused()) return
    if (!Notification.isSupported()) return
    const n = new Notification(notificationText((params as { request: ApprovalRequest }).request))
    n.on('click', show)
    n.show()
  })
  hub.on('state', (s: HubState) => { if (s.kind !== 'running') { counter.reset(); render() } })
}
```

Generate the icon: run once `node -e "…"` or any image tool to produce a 16×16 PNG and paste its base64 into `ICON_PNG_BASE64` (the literal placeholder above must not remain in the committed file). A one-liner that needs no dependencies: create the PNG with Go (`image/png` in a throwaway `go run` program under the scratch directory) and `base64` it.

`main.ts`: after `registerIpc(...)`, call `setupAttention(hub, () => win)`.

- [ ] **Step 4: Run tests and a manual check**

Run: `npm run typecheck && npm test && npm run build`
Manual: with the app unlocked and another window focused, send the `nc` request from Task 8 — an OS notification appears; clicking it focuses the app; the tray tooltip (and title on macOS) shows 1, then clears after deciding. Record the result.

- [ ] **Step 5: Commit**

```bash
git add desktop/src desktop/test
git commit -m "feat(desktop): tray pending count and OS notifications for approvals"
```

---

### Task 10: Playwright smoke test and CI desktop job

**Files:**
- Create: `desktop/playwright.config.ts`, `desktop/e2e/smoke.spec.ts`, `desktop/e2e/doorClient.ts`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `sshtestd` (Task 2), the hub binary, the app built by `npm run build`.
- Produces: `npm run e2e` (builds, then `playwright test`), a `desktop` CI job.

- [ ] **Step 1: Write the test and helpers**

`desktop/playwright.config.ts`:

```ts
import { defineConfig } from '@playwright/test'

export default defineConfig({ testDir: 'e2e', timeout: 90_000, workers: 1, reporter: 'list' })
```

`desktop/e2e/doorClient.ts` — a raw JSON-RPC call to the MCP door socket (no MCP SDK needed):

```ts
import * as net from 'node:net'

export function doorCall(socketPath: string, method: string, params: unknown, timeoutMs = 30000): Promise<any> {
  return new Promise((resolve, reject) => {
    const sock = net.connect(socketPath)
    let buf = ''
    const t = setTimeout(() => { sock.destroy(); reject(new Error('door call timed out')) }, timeoutMs)
    sock.on('connect', () => sock.write(JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }) + '\n'))
    sock.on('data', (d) => {
      buf += d.toString()
      const nl = buf.indexOf('\n')
      if (nl < 0) return
      clearTimeout(t)
      sock.end()
      const m = JSON.parse(buf.slice(0, nl))
      if (m.error) reject(new Error(m.error.message))
      else resolve(m.result)
    })
    sock.on('error', (e) => { clearTimeout(t); reject(e) })
  })
}
```

`desktop/e2e/smoke.spec.ts`:

```ts
import { test, expect, _electron as electron, type ElectronApplication } from '@playwright/test'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import * as fs from 'node:fs'
import * as path from 'node:path'
import * as readline from 'node:readline'
import { doorCall } from './doorClient'

const repo = path.resolve(__dirname, '..', '..')
const exe = process.platform === 'win32' ? '.exe' : ''
let tmp: string, sshd: ChildProcess, app: ElectronApplication

test.beforeAll(async () => {
  tmp = fs.mkdtempSync('/tmp/sme')
  execFileSync('go', ['build', '-o', path.join(tmp, 'ssh-mcp' + exe), './cmd/ssh-mcp'], { cwd: repo, stdio: 'inherit' })
  execFileSync('go', ['build', '-o', path.join(tmp, 'sshtestd' + exe), './internal/sshx/sshtest/sshtestd'], { cwd: repo, stdio: 'inherit' })
  const store = path.join(tmp, 'store', 'servers.json')
  sshd = spawn(path.join(tmp, 'sshtestd' + exe), [`-write-store=${store}`, '-password=pw'], { stdio: ['pipe', 'pipe', 'inherit'] })
  await new Promise<void>((resolve) => readline.createInterface({ input: sshd.stdout! }).once('line', () => resolve()))
  app = await electron.launch({
    args: ['.'],
    cwd: path.resolve(__dirname, '..'),
    env: { ...process.env, SSH_MCP_BIN: path.join(tmp, 'ssh-mcp' + exe), SSH_MCP_STORE: store, SSH_MCP_RUNTIME_DIR: tmp },
  })
})

test.afterAll(async () => {
  await app?.close()
  sshd?.stdin?.end()
  sshd?.kill()
})

test('unlock, open a terminal, approve an AI command', async () => {
  const win = await app.firstWindow()
  await win.getByLabel('Master password').fill('pw')
  await win.getByRole('button', { name: 'Unlock' }).click()
  await win.getByRole('button', { name: 'box' }).click()
  await expect(win.locator('.xterm')).toBeVisible()
  await win.locator('.xterm').click()
  await win.keyboard.type('echo smoke-ok')
  await expect(win.locator('.xterm-rows')).toContainText('echo smoke-ok')

  const socket = path.join(tmp, 'ssh-mcp', 'hub.sock')
  const result = doorCall(socket, 'exec', { requestId: 'r1', client: 'e2e', server: 'box', command: 'echo approved', description: 'smoke' })
  const allow = win.getByRole('button', { name: 'Allow' })
  await expect(allow).toBeVisible()
  await expect(allow).toBeEnabled({ timeout: 2000 })
  await allow.click()
  await expect(result).resolves.toMatchObject({ exitCode: 0, stdout: 'echo approved' })
  await expect(win.getByText('Nothing waiting.')).toBeVisible()

  const denied = doorCall(socket, 'exec', { requestId: 'r2', client: 'e2e', server: 'box', command: 'rm -rf /', description: '' })
  await win.getByPlaceholder('Reason (optional)').fill('not today')
  await win.getByPlaceholder('Reason (optional)').press('Enter')
  await expect(denied).rejects.toThrow('Denied by user: not today')
})
```

The Unix socket path is used on Linux and macOS; on Windows the door is a named pipe, so skip the test there: add `test.skip(process.platform === 'win32', 'door client uses a Unix socket')` at the top of the test. `SSH_MCP_RUNTIME_DIR=tmp` puts the socket at `<tmp>/ssh-mcp/hub.sock` (spec §Amendments); `tmp` under `/tmp` keeps the path short.

`sshtestd` echoes the exec command text as stdout; that is why `stdout` equals the command. Its shell echoes typed input; that is why the terminal shows the typed text.

- [ ] **Step 2: Run it**

Run (in `desktop/`): `npm run e2e`
Expected: PASS on macOS and Linux with a display (Linux headless: `xvfb-run -a npm run e2e`). If this machine cannot open windows, say so in the report and rely on CI.

- [ ] **Step 3: CI job**

Append to `.github/workflows/ci.yml` under `jobs:` (keep the existing Go job unchanged):

```yaml
  desktop:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: desktop
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: npm
          cache-dependency-path: desktop/package-lock.json
      - run: npm ci
      - run: npm run typecheck
      - run: npm test
      - run: xvfb-run -a npm run e2e
```

Validate the YAML: `ruby -ryaml -e 'YAML.load_file(".github/workflows/ci.yml")'`.

- [ ] **Step 4: Commit**

```bash
git add desktop/playwright.config.ts desktop/e2e .github/workflows/ci.yml
git commit -m "test(desktop): Playwright smoke test against sshtestd; CI desktop job"
```

---

### Task 11: Docs and manual checklist

**Files:**
- Modify: `README.md`, `CLAUDE.md`, `docs/superpowers/ROADMAP.md`
- Create: `docs/superpowers/checklists/slice1-manual.md`

- [ ] **Step 1: README**

Add a "Desktop app" section after the hub section: prerequisites (Go, Node 22.12+), `go build -o ssh-mcp ./cmd/ssh-mcp` at the repo root, `cd desktop && npm ci && npm start`; what the app does (unlock, host list, terminals, approval panel, notifications, tray, 15-minute idle lock); how the AI client connects (unchanged bridge: `claude mcp add --transport stdio ssh-mcp -- ssh-mcp`); `hub --cli` remains for headless use. Document `SSH_MCP_BIN` and `SSH_MCP_STORE` as development knobs.

- [ ] **Step 2: CLAUDE.md**

Commands: `cd desktop && npm ci && npm run typecheck && npm test`, `npm run e2e` (needs a display; `xvfb-run -a` on Linux), `go run ./internal/sshx/sshtest/sshtestd -write-store=… -password=…`. Architecture: one bullet group for `desktop/` (main spawns the hub over stdio JSON-RPC with restart backoff; `ipc.ts` whitelists methods; only `renderer/transport.ts` touches the bridge; approval panel rules; terminals ack after render). Keep it short.

- [ ] **Step 3: Manual checklist**

`docs/superpowers/checklists/slice1-manual.md` with the spec's manual checks, each as a checkbox with exact steps and the pass condition:
1. Vietnamese IME in a terminal: macOS Telex, Windows Unikey, Linux ibus and fcitx5 — typed `xin chào` appears correctly on the remote.
2. Throughput: in a terminal to a real server, `cat` a 50 MB file and `yes | head -c 200M`; compare against `ssh` in the native terminal. Pass: no UI freeze > 200 ms (watch the approval panel's Allow timer / move the window), typing stays responsive, renderer and main RSS (Activity Monitor / Task Manager / `ps`) stay bounded.
3. Notification click focuses the app and the request is visible.
4. Windows: MCP door DACL and SID checks (connect the bridge from the same user: works; from another user: refused).
5. Idle lock: leave the app 15 minutes with nothing pending — it returns to the unlock screen; terminals stay connected.

- [ ] **Step 4: ROADMAP**

Slice 1 row: status "1a and 1b done; manual checklist pending". Remove the carried-forward items that are now done (idle auto-lock, hello method, UI-door protocol note) and keep the rest.

- [ ] **Step 5: Verify and commit**

Run: `go vet ./... && go test -short ./... && (cd desktop && npm run typecheck && npm test)`

```bash
git add README.md CLAUDE.md docs/superpowers
git commit -m "docs: desktop app usage, manual checklist, roadmap status"
```

---

## Self-Review

**Spec coverage:**
- `desktop/` main (spawn, restart backoff 1/3/10 s, stop after 3 in 60 s with stderr, locked after restart, relay RPC, tray badge, notifications) → Tasks 4, 5, 9. Preload typed API with contextIsolation/sandbox/no nodeIntegration and CSP → Tasks 3, 5. Renderer only via `transport.ts` → Task 5.
- Vault lifecycle: unlock in the app → Task 6; 15-minute idle auto-lock with open terminals kept → Task 1 (hub) + Task 6 (UI returns to unlock on `locked`).
- AI command flow steps 5–6: notification when unfocused and click-to-focus → Task 9; non-modal panel, full command, non-ASCII highlight, timeout, unverified labels, Deny default via Enter, Allow no shortcut + 500 ms, Deny all, Send to tab via `paste()` after first output → Task 8 (+ Task 7 `sendToTab`).
- Terminal sessions: client ids, nothing before the reply, ack after render, resize debounce 50 ms, exit + Reconnect, hub restart ends tabs → Task 7.
- Testing: Playwright smoke (launch, unlock, tab, echo, exec → dialog → approve → output) → Task 10; manual checks (IME, throughput, notification click) → Task 11 checklist; CI desktop job → Task 10.
- Success criteria 1 (notification + approve/deny round trip) → Tasks 8–10; 4 (flood cap and focus) → hub cap exists (1a), panel focus rules → Task 8; 5 throughput and 6 manual → Task 11 checklist.
- Amendments: `hello` → Task 1; protocol facts → Global Constraints and Tasks 5, 7.

**Placeholder scan:** one intentional generated value — the tray icon base64 in Task 9 — has explicit generation instructions and a rule that the placeholder must not be committed.

**Type consistency:** `HubState`, `HubEvent`, `ApprovalRequest`, `ServerInfo` defined once in `shared/protocol.ts` (Task 4) and used by Tasks 5–9. `hub.*` transport names (Task 5) match their uses in Tasks 6–8. `TerminalsHandle.sendToTab` (Task 7) matches its use in Task 8. `PendingItem`/`seed`/`reduceApprovals`/`allowEnabled` (Task 8) consistent. `IdleLock`, `touch`, `Close`, `ProtocolVersion` (Task 1) consistent with `cmd/ssh-mcp/hub.go`.
