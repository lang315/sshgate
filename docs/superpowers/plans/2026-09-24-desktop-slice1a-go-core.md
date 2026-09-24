# Desktop Slice 1a: Go Core (hub, broker, MCP door, bridge, CLI approver) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `ssh-mcp` a hub that holds the unlocked vault and SSH connections, gates every AI command behind human approval, and exposes that gate to MCP clients through a same-user socket — usable today from a terminal, before any Electron code exists.

**Architecture:** One Go binary, three modes: `--host` (unchanged, standalone), bridge (default: MCP stdio → hub socket), and `hub` (owns vault, `sshx.Registry`, `broker`, and two doors: a JSON-RPC UI door on stdio and a restricted MCP door on a Unix socket / named pipe). A `--cli` flag on `hub` replaces the UI door with a terminal approver so the whole flow is exercised end to end without Electron. Slice 1b (Electron) is a separate plan that consumes the UI door as finalized here.

**Tech Stack:** Go 1.26, `golang.org/x/crypto/ssh`, `github.com/modelcontextprotocol/go-sdk v1.7.0`, `golang.org/x/sys` (peer credentials), `github.com/Microsoft/go-winio` (Windows pipes), testcontainers (integration tests, already in use). No new frameworks; JSON-RPC codec is ~100 lines of stdlib.

**Spec:** `docs/superpowers/specs/2026-09-24-desktop-app-design.md` (sections: Threat Model, Architecture → Components, Data Model, AI Command Flow, Timeouts and cancellation, Command Validation, Output Cap, Audit log, Errors returned to the AI, Terminal Sessions, Testing). `docs/superpowers/ROADMAP.md` for standing decisions.

## Global Constraints

- Go module `github.com/lang315/ssh-mcp`, Go `1.26.5` per `go.mod`. Tests use plain `testing`, no testify. Integration tests skip under `-short` and use the existing `startSSH(t)` testcontainers helper in `internal/sshx/manager_test.go` (image `lscr.io/linuxserver/openssh-server`, user `test`, password `testpass`, port `2222`).
- `--host` mode keeps its flags and behaviour, but no longer reads the vault: standalone means CLI credentials only.
- `SSH_MCP_MASTER_PASSWORD_FILE` is removed (spec §Breaking change).
- MCP door exposes exactly `listServers`, `exec`, `sudoExec`. No tool on it builds shell strings from arguments, touches the vault, or reads terminal buffers.
- Every exec through the MCP door goes through the broker. No auto-approval rules exist.
- Error text returned to the AI must match spec §Errors returned to the AI verbatim (copied into Task 9).
- Pending cap: 5. Approval expiry: 5 minutes. Exec timeout: `timeoutSec` clamped to [1, 600], default 60. Output cap: 64 KB per stream (32 KB head + 32 KB tail). Description cap: 500 (already enforced by `AppendDescription`).
- Audit: JSONL, mode 0600, next to `servers.json`, never contains output.
- Unix socket path: `$XDG_RUNTIME_DIR/ssh-mcp/hub.sock` (Linux), `$TMPDIR/ssh-mcp/hub.sock` (macOS), fallback `/tmp/ssh-mcp-<uid>/hub.sock`; directory 0700, socket 0600, peer UID checked. Windows: `\\.\pipe\ssh-mcp-hub-<SID>`, current-user DACL, first-instance flag, bridge verifies server SID.
- Commit after every task with a Conventional Commit message. Run `go vet ./... && go test -short ./...` before each commit.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/config/sanitize.go` (modify) | Reject all control and format runes in commands and descriptions |
| `internal/config/capout.go` (new) | `CapOutput`: head/tail truncation with marker |
| `internal/config/store.go` (modify) | `Server.AIVisible` |
| `internal/config/masterpw.go`, `masterpw_test.go` (delete) | Headless vault access removed |
| `internal/web/handlers.go`, `static/app.js` (modify) | `aiVisible` in DTO and form checkbox |
| `internal/sshx/manager.go` (modify) | `ExecResult`, split streams, ctx deadline, SIGKILL on cancel, `OpenSession`, keepalive |
| `internal/sshx/term.go` (new) | `TermSession`: PTY channel with resize, ack-based flow control, exit event |
| `internal/broker/broker.go` (new) | Pending queue, cap, expiry, decide, deny-all, events |
| `internal/broker/audit.go` (new) | JSONL audit writer |
| `internal/rpc/rpc.go` (new) | Newline JSON-RPC 2.0 server/client, notifications |
| `internal/hub/hub.go` (new) | Vault state, reload on revision change, `Exec` pipeline, server listing |
| `internal/hub/mcpdoor.go` (new) | MCP door service: method table restricted to three calls |
| `internal/hub/sockpath.go`, `listen_unix.go`, `listen_windows.go`, `peer_unix.go`, `peer_windows.go` (new) | Socket/pipe path, listeners, peer checks, bridge-side dial with SID check |
| `internal/hub/uidoor.go` (new) | UI door: unlock/lock/servers/pending/decide/denyAll/term.* and events |
| `internal/hub/cliui.go` (new) | Terminal approver used by `hub --cli` |
| `internal/mcpserver/bridge.go` (new) | MCP tools that forward to the hub over the MCP door, with progress |
| `internal/mcpserver/tools.go`, `resolve.go` (modify) | Adapt to `ExecResult`; `--host` no longer loads the vault |
| `cmd/ssh-mcp/main.go`, `hub.go` (modify/new) | Routing: `hub`, bridge, `--host` |
| `cmd/ssh-mcp/e2e_test.go` (new) | Bridge ↔ hub ↔ sshd end-to-end |
| `README.md`, `CLAUDE.md` (modify) | New modes, removed env var |

---

### Task 1: Reject every control and format rune in commands

**Files:**
- Modify: `internal/config/sanitize.go`
- Test: `internal/config/sanitize_test.go`

**Interfaces:**
- Produces: `func SanitizeCommand(cmd string, maxChars int) (string, error)` (same signature; stricter). `func AppendDescription(cmd, desc string) (string, error)` (same signature; uses the same rune check). Error text: `command contains forbidden character U+XXXX at position N` / `description contains forbidden character U+XXXX at position N`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/sanitize_test.go`:

```go
func TestSanitizeCommandRejectsControlAndFormatRunes(t *testing.T) {
	cases := []struct {
		name, in string
	}{
		{"esc", "ls\x1b[2Jrm -rf /"},
		{"tab", "ls\t-la"},
		{"bidi override", "echo ‮gnp.txt"},
		{"zero width space", "rm​ -rf /"},
		{"c1 control", "ls\u0085rm"},
		{"nul", "ls\x00rm"},
	}
	for _, c := range cases {
		if _, err := SanitizeCommand(c.in, 1000); err == nil {
			t.Errorf("%s: expected error for %q", c.name, c.in)
		} else if !strings.Contains(err.Error(), "forbidden character U+") {
			t.Errorf("%s: error should name the rune, got %v", c.name, err)
		}
	}
	// Ordinary non-ASCII text is allowed.
	if _, err := SanitizeCommand("echo 'xin chào'", 1000); err != nil {
		t.Fatalf("vietnamese should pass: %v", err)
	}
}

func TestAppendDescriptionRejectsControlRunes(t *testing.T) {
	if _, err := AppendDescription("ls", "safe\x1bdesc"); err == nil {
		t.Fatal("ESC in description should error")
	}
	if _, err := AppendDescription("ls", "tab\tdesc"); err == nil {
		t.Fatal("tab in description should error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config -run 'TestSanitizeCommandRejects|TestAppendDescriptionRejects' -v`
Expected: FAIL (tab, ESC, bidi cases pass through today).

- [ ] **Step 3: Implement**

Replace `hasControl` and its callers in `internal/config/sanitize.go`:

```go
package config

import (
	"fmt"
	"strings"
	"unicode"
)

// forbiddenRune returns the first rune that must not appear in a command or
// description, with its byte position. Controls (including \t) can change
// what a shell or terminal does with the text; format runes (bidi
// overrides, zero-width characters) can make the approval UI display a
// different command than the one that runs.
func forbiddenRune(s string) (rune, int, bool) {
	for i, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return r, i, true
		}
	}
	return 0, 0, false
}

func SanitizeCommand(cmd string, maxChars int) (string, error) {
	t := strings.TrimSpace(cmd)
	if t == "" {
		return "", fmt.Errorf("Command cannot be empty")
	}
	if r, pos, bad := forbiddenRune(t); bad {
		return "", fmt.Errorf("command contains forbidden character U+%04X at position %d", r, pos)
	}
	if maxChars >= 0 && len(t) > maxChars {
		return "", fmt.Errorf("Command is too long (max %d characters)", maxChars)
	}
	return t, nil
}

func EscapeShellSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

func AppendDescription(cmd, desc string) (string, error) {
	if desc == "" {
		return cmd, nil
	}
	if r, pos, bad := forbiddenRune(desc); bad {
		return "", fmt.Errorf("description contains forbidden character U+%04X at position %d", r, pos)
	}
	if len(desc) > 500 {
		return "", fmt.Errorf("description too long (max 500)")
	}
	return cmd + " # " + strings.ReplaceAll(desc, "#", `\#`), nil
}
```

Delete the old `hasControl` function. The existing test `"ls\nrm -rf /"` still fails as required because `\n` is a control.

- [ ] **Step 4: Run all config tests**

Run: `go test ./internal/config -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/sanitize.go internal/config/sanitize_test.go
git commit -m "fix(config): reject all control and format runes in commands and descriptions"
```

---

### Task 2: Output cap helper

**Files:**
- Create: `internal/config/capout.go`
- Test: `internal/config/capout_test.go`

**Interfaces:**
- Produces: `func CapOutput(s string, max int) string`. If `len(s) <= max`, returns `s`. Otherwise returns first `max/2` bytes + `\n… [truncated N bytes] …\n` + last `max/2` bytes, where N = `len(s) - max`. `const DefaultOutputCap = 64 * 1024`.

- [ ] **Step 1: Write the failing test**

```go
package config

import (
	"strings"
	"testing"
)

func TestCapOutputPassThrough(t *testing.T) {
	if got := CapOutput("hello", 10); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestCapOutputKeepsHeadAndTail(t *testing.T) {
	s := strings.Repeat("a", 50) + strings.Repeat("b", 50) + strings.Repeat("c", 50)
	got := CapOutput(s, 40)
	if !strings.HasPrefix(got, strings.Repeat("a", 20)) {
		t.Fatalf("head missing: %q", got)
	}
	if !strings.HasSuffix(got, strings.Repeat("c", 20)) {
		t.Fatalf("tail missing: %q", got)
	}
	if !strings.Contains(got, "[truncated 110 bytes]") {
		t.Fatalf("marker wrong: %q", got)
	}
}

func TestDefaultOutputCapIs64K(t *testing.T) {
	if DefaultOutputCap != 65536 {
		t.Fatal("spec says 64 KB")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run TestCapOutput -v`
Expected: FAIL, `CapOutput` undefined.

- [ ] **Step 3: Implement**

```go
package config

import "fmt"

const DefaultOutputCap = 64 * 1024

// CapOutput keeps the first and last max/2 bytes of s and replaces the
// middle with a marker naming how many bytes were dropped. Applied to
// stdout and stderr independently so one stream cannot evict the other.
func CapOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	half := max / 2
	dropped := len(s) - max
	return s[:half] + fmt.Sprintf("\n… [truncated %d bytes] …\n", dropped) + s[len(s)-half:]
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config -run TestCapOutput -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/capout.go internal/config/capout_test.go
git commit -m "feat(config): head/tail output cap"
```

---

### Task 3: `AIVisible` on servers, editable in the web UI

**Files:**
- Modify: `internal/config/store.go:14-26`
- Modify: `internal/web/handlers.go:16-33` (DTO), `:47-70` (GET mapping), `:175-177` (`dtoToServer`)
- Modify: `internal/web/static/app.js:92-125`
- Test: `internal/config/store_test.go`, `internal/web/handlers_test.go`

**Interfaces:**
- Produces: `config.Server.AIVisible bool` with JSON tag `aiVisible,omitempty`. `web.ServerDTO.AIVisible bool` with JSON tag `aiVisible`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/store_test.go`:

```go
func TestAIVisibleRoundtripsAndDefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	f := &File{Version: 1, Servers: []Server{
		{Name: "a", Host: "h", Port: 22, User: "u", Auth: "key", AIVisible: true},
		{Name: "b", Host: "h", Port: 22, User: "u", Auth: "key"},
	}}
	if err := Save(path, f, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Servers[0].AIVisible || loaded.Servers[1].AIVisible {
		t.Fatalf("aiVisible not preserved: %+v", loaded.Servers)
	}
}
```

Append to `internal/web/handlers_test.go` (look at the existing helper that builds an unlocked `App` and posts a server; reuse its name. If the helper is `newUnlockedApp(t)` returning `(*App, *Session)`, write):

```go
func TestServerDTOCarriesAIVisible(t *testing.T) {
	s := config.Server{Name: "a", Host: "h", Port: 22, User: "u", Auth: "key", AIVisible: true}
	d := serverToDTO(s)
	if !d.AIVisible {
		t.Fatal("DTO lost aiVisible")
	}
	back := dtoToServer(d)
	if !back.AIVisible {
		t.Fatal("dtoToServer lost aiVisible")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config ./internal/web -run 'AIVisible' -v`
Expected: FAIL, field undefined.

- [ ] **Step 3: Implement**

`internal/config/store.go`, add the field after `HostKey`:

```go
	HostKey          string `json:"hostKey,omitempty"`
	AIVisible        bool   `json:"aiVisible,omitempty"`
```

`internal/web/handlers.go`: add `AIVisible bool \`json:"aiVisible"\`` to `ServerDTO` after `HostKey`. Extract the GET mapping at lines 58–66 into a function and use it in both GET handlers:

```go
func serverToDTO(s config.Server) ServerDTO {
	return ServerDTO{
		Name: s.Name, Host: s.Host, Port: s.Port, User: s.User, Auth: s.Auth,
		KeyPath: s.KeyPath, HostKey: s.HostKey, AIVisible: s.AIVisible,
		HasPassword: s.EncPassword != "", HasSuPassword: s.EncSuPassword != "", HasSudoPassword: s.EncSudoPassword != "",
	}
}
```

(Keep whatever `Has*` fields the current literal sets; copy them exactly.) Then:

```go
func dtoToServer(d ServerDTO) config.Server {
	return config.Server{Name: d.Name, Host: d.Host, Port: d.Port, User: d.User, Auth: d.Auth, KeyPath: d.KeyPath, HostKey: d.HostKey, AIVisible: d.AIVisible}
}
```

`internal/web/static/app.js`, in `showForm`, after the `hostKey` field:

```js
  const ai = document.createElement('label');
  const aiBox = document.createElement('input');
  aiBox.type = 'checkbox';
  aiBox.checked = !!(s && s.aiVisible);
  ai.append(aiBox, ' Visible to AI (MCP) — off by default');
```

In the `save.onclick` DTO: `dto.aiVisible = aiBox.checked;`. In the list of appended fields add `ai` after `hostKey.l`: `[name, host, port, user, auth, keyPath, pw, su, sudo, hostKey].forEach(x => f.append(x.l)); f.append(ai);`. In the table row builder (line 64) append a cell: `el('td', s.aiVisible ? 'AI' : '')`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config ./internal/web -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/store.go internal/config/store_test.go internal/web/handlers.go internal/web/handlers_test.go internal/web/static/app.js
git commit -m "feat(config,web): per-server aiVisible flag, off by default"
```

---

### Task 4: `ExecResult` with split streams, ctx deadline, SIGKILL on cancel

**Files:**
- Modify: `internal/sshx/manager.go:106-157` (`runOnce`, `Exec`, `ExecSudo`), `:226-244` (`execElevated`)
- Modify: `internal/mcpserver/tools.go:26-51` (`runExec`)
- Test: `internal/sshx/manager_test.go`, `internal/sshx/elevation_parse_test.go`

**Interfaces:**
- Produces:
  ```go
  type ExecResult struct { Stdout, Stderr string; ExitCode int }
  func (m *Manager) Exec(ctx context.Context, cmd string) (ExecResult, error)
  func (m *Manager) ExecSudo(ctx context.Context, cmd string) (ExecResult, error)
  var ErrCancelled = errors.New("cancelled; the remote process may still be running")
  ```
  A non-zero exit is **not** an error; it is `ExitCode`. Errors are transport, timeout, or cancellation. If `ctx` has a deadline it wins; otherwise `m.cfg.TimeoutMs` applies. On `ctx.Done()` the session receives `SIGKILL` then is closed, and the error is `ErrCancelled` (wrapping `ctx.Err()`).
- Consumes: nothing new.

- [ ] **Step 1: Write the failing tests**

Add to `internal/sshx/manager_test.go`:

```go
func TestExecSplitsStreamsAndExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSH(t)
	defer cleanup()
	m := NewManager(DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", Insecure: true, TimeoutMs: 30000})
	defer m.Close()
	res, err := m.Exec(context.Background(), "echo out; echo err 1>&2; exit 3")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "out" || strings.TrimSpace(res.Stderr) != "err" || res.ExitCode != 3 {
		t.Fatalf("got %+v", res)
	}
}

func TestExecCancelReturnsErrCancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSH(t)
	defer cleanup()
	m := NewManager(DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", Insecure: true, TimeoutMs: 30000})
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(500 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := m.Exec(ctx, "sleep 30")
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("want ErrCancelled, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancel did not return promptly")
	}
}

func TestExecCtxDeadlineBeatsConfigTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSH(t)
	defer cleanup()
	m := NewManager(DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", Insecure: true, TimeoutMs: 60000})
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := m.Exec(ctx, "sleep 30")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want timeout error, got %v", err)
	}
}
```

Add imports `errors`, `strings`, `time` as needed.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sshx -run 'TestExecSplits|TestExecCancel|TestExecCtxDeadline' -v`
Expected: compile FAIL (`ExecResult`, `ErrCancelled` undefined). Requires Docker; if unavailable the tests skip, so also confirm the build error with `go vet ./internal/sshx`.

- [ ] **Step 3: Implement**

In `internal/sshx/manager.go`:

```go
type ExecResult struct {
	Stdout, Stderr string
	ExitCode       int
}

var ErrCancelled = errors.New("cancelled; the remote process may still be running")

func (m *Manager) runOnce(ctx context.Context, cmd string, stdin string) (ExecResult, error) {
	sess, err := m.client.NewSession()
	if err != nil {
		return ExecResult{}, err
	}
	defer sess.Close()
	var out, errb bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &errb
	if stdin != "" {
		sess.Stdin = strings.NewReader(stdin)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	// ctx deadline wins; otherwise fall back to the configured timeout.
	var timeout <-chan time.Time
	if _, has := ctx.Deadline(); !has {
		timeout = time.After(time.Duration(m.cfg.TimeoutMs) * time.Millisecond)
	}
	finish := func(runErr error) (ExecResult, error) {
		res := ExecResult{Stdout: out.String(), Stderr: errb.String()}
		var exit *ssh.ExitError
		if errors.As(runErr, &exit) {
			res.ExitCode = exit.ExitStatus()
			return res, nil
		}
		return res, runErr
	}
	select {
	case runErr := <-done:
		return finish(runErr)
	case <-timeout:
		_ = sess.Signal(ssh.SIGKILL)
		sess.Close()
		<-done
		return ExecResult{Stdout: out.String(), Stderr: errb.String()},
			fmt.Errorf("command timed out after %dms", m.cfg.TimeoutMs)
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		sess.Close()
		<-done
		res := ExecResult{Stdout: out.String(), Stderr: errb.String()}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return res, fmt.Errorf("command timed out: %w", ctx.Err())
		}
		return res, fmt.Errorf("%w: %v", ErrCancelled, ctx.Err())
	}
}

func (m *Manager) Exec(ctx context.Context, cmd string) (ExecResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(); err != nil {
		return ExecResult{}, err
	}
	if m.cfg.SuPassword != "" {
		out, code, err := m.execElevated(ctx, cmd)
		return ExecResult{Stdout: out, ExitCode: code}, err
	}
	return m.runOnce(ctx, cmd, "")
}

func (m *Manager) ExecSudo(ctx context.Context, cmd string) (ExecResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(); err != nil {
		return ExecResult{}, err
	}
	if m.cfg.SudoPassword == "" {
		return m.runOnce(ctx, WrapSudoNoPassword(cmd), "")
	}
	return m.runOnce(ctx, WrapSudoWithPassword(cmd), m.cfg.SudoPassword+"\n")
}
```

`execElevated` currently returns `(string, error)` and (per `elevation_parse_test.go`) parses an exit code from the sentinel line, turning non-zero into an error. Change its signature to `(string, int, error)`: return the parsed code as the int and only return an error for transport/timeout problems. Update `elevation_parse_test.go` expectations accordingly (a non-zero code is returned, not an error). Add `"errors"` to imports.

Update `internal/mcpserver/tools.go` `runExec`:

```go
	var res sshx.ExecResult
	if sudo {
		res, err = mgr.ExecSudo(ctx, cmd)
	} else {
		res, err = mgr.Exec(ctx, cmd)
	}
	if err != nil {
		return textErr(red.Redact(err.Error())), nil
	}
	return textOK(FormatExec(res, red)), nil
```

and add to `tools.go`:

```go
// FormatExec renders an exec result for the AI: exit code first, then the
// two streams, each redacted and capped independently.
func FormatExec(res sshx.ExecResult, red *config.Redactor) string {
	var b strings.Builder
	fmt.Fprintf(&b, "exit code: %d\n", res.ExitCode)
	if res.Stdout != "" {
		b.WriteString("stdout:\n")
		b.WriteString(config.CapOutput(red.Redact(res.Stdout), config.DefaultOutputCap))
		if !strings.HasSuffix(res.Stdout, "\n") {
			b.WriteString("\n")
		}
	}
	if res.Stderr != "" {
		b.WriteString("stderr:\n")
		b.WriteString(config.CapOutput(red.Redact(res.Stderr), config.DefaultOutputCap))
	}
	return b.String()
}
```

Add `"strings"` to the imports of `tools.go`.

- [ ] **Step 4: Run tests**

Run: `go vet ./... && go test -short ./... && go test ./internal/sshx -run 'TestExec' -v` (last one needs Docker).
Expected: PASS. If Docker is absent, the three new tests report SKIP; note that in the commit body.

- [ ] **Step 5: Commit**

```bash
git add internal/sshx/manager.go internal/sshx/manager_test.go internal/sshx/elevation_parse_test.go internal/mcpserver/tools.go
git commit -m "feat(sshx): split stdout/stderr, exit code as data, ctx deadline, SIGKILL on cancel"
```

---

### Task 5: Broker: pending queue with cap, expiry, decisions, events

**Files:**
- Create: `internal/broker/broker.go`
- Test: `internal/broker/broker_test.go`

**Interfaces:**
- Produces:
  ```go
  package broker

  type Outcome string
  const (
      Allowed             Outcome = "allowed"
      Denied              Outcome = "denied"
      Expired             Outcome = "expired"
      ApprovedButCancelled Outcome = "approved_but_cancelled"
      SentToTab           Outcome = "sent_to_tab"
  )
  type Request struct {
      ID, Client, Server, Command, Description string
      Sudo        bool
      TimeoutSec  int
      ReceivedAt  time.Time
  }
  type Decision struct { Outcome Outcome; Reason string }
  type Event struct { Kind string; Request Request; Decision Decision } // Kind: "pending" | "decided"
  type Options struct { MaxPending int; Expiry time.Duration; Now func() time.Time; OnEvent func(Event) }
  var ErrTooManyPending = errors.New("too many pending requests, try again later")
  var ErrNotFound = errors.New("no such pending request")
  func New(o Options) *Broker
  func (b *Broker) Submit(ctx context.Context, req Request) (Decision, error)
  func (b *Broker) Pending() []Request
  func (b *Broker) Decide(id string, d Decision) error
  func (b *Broker) DenyAll(reason string)
  ```
  `Submit` assigns `req.ID` (random hex) and `ReceivedAt` if zero, emits `pending`, blocks until decided, expired, or `ctx` cancelled. On `ctx` cancel while pending it removes the request and returns `ctx.Err()`. On expiry it returns `Decision{Outcome: Expired}`. `Decide` after ctx cancel returns `ErrNotFound`. Expiry uses `Options.Now` for the timestamp and a real `time.Timer` capped by `Expiry`; tests pass a short `Expiry`.

- [ ] **Step 1: Write the failing tests**

```go
package broker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func newTestBroker(expiry time.Duration) (*Broker, *[]Event, *sync.Mutex) {
	var events []Event
	var mu sync.Mutex
	b := New(Options{MaxPending: 5, Expiry: expiry, OnEvent: func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}})
	return b, &events, &mu
}

func TestSubmitBlocksUntilDecided(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	done := make(chan Decision, 1)
	go func() {
		d, err := b.Submit(context.Background(), Request{Server: "s", Command: "ls"})
		if err != nil {
			t.Error(err)
		}
		done <- d
	}()
	var pending []Request
	for i := 0; i < 50 && len(pending) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
		pending = b.Pending()
	}
	if len(pending) != 1 || pending[0].ID == "" {
		t.Fatalf("pending = %+v", pending)
	}
	if err := b.Decide(pending[0].ID, Decision{Outcome: Denied, Reason: "no"}); err != nil {
		t.Fatal(err)
	}
	d := <-done
	if d.Outcome != Denied || d.Reason != "no" {
		t.Fatalf("got %+v", d)
	}
	if len(b.Pending()) != 0 {
		t.Fatal("request should be removed after decision")
	}
}

func TestExpiryIsExpiredNotDenied(t *testing.T) {
	b, _, _ := newTestBroker(50 * time.Millisecond)
	d, err := b.Submit(context.Background(), Request{Server: "s", Command: "ls"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Outcome != Expired {
		t.Fatalf("got %+v", d)
	}
}

func TestCancelWhilePendingRemovesRequest(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := b.Submit(ctx, Request{Server: "s", Command: "ls"}); errc <- err }()
	for len(b.Pending()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	id := b.Pending()[0].ID
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if len(b.Pending()) != 0 {
		t.Fatal("cancelled request still pending")
	}
	if err := b.Decide(id, Decision{Outcome: Allowed}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("approve after cancel must be ErrNotFound, got %v", err)
	}
}

func TestSixthRequestRefusedUntilOneDecided(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	for i := 0; i < 5; i++ {
		go b.Submit(context.Background(), Request{Server: "s", Command: "ls"})
	}
	for len(b.Pending()) < 5 {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := b.Submit(context.Background(), Request{Server: "s", Command: "ls"}); !errors.Is(err, ErrTooManyPending) {
		t.Fatalf("want ErrTooManyPending, got %v", err)
	}
	b.Decide(b.Pending()[0].ID, Decision{Outcome: Denied})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := b.Submit(ctx, Request{Server: "s", Command: "ls"}); errors.Is(err, ErrTooManyPending) {
		t.Fatal("slot freed but still refused")
	}
}

func TestDenyAllResolvesEverything(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	results := make(chan Decision, 3)
	for i := 0; i < 3; i++ {
		go func() { d, _ := b.Submit(context.Background(), Request{Server: "s", Command: "ls"}); results <- d }()
	}
	for len(b.Pending()) < 3 {
		time.Sleep(5 * time.Millisecond)
	}
	b.DenyAll("cleared")
	for i := 0; i < 3; i++ {
		d := <-results
		if d.Outcome != Denied || d.Reason != "cleared" {
			t.Fatalf("got %+v", d)
		}
	}
}

func TestEventsEmitted(t *testing.T) {
	b, events, mu := newTestBroker(time.Minute)
	go b.Submit(context.Background(), Request{Server: "s", Command: "ls"})
	for len(b.Pending()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	b.Decide(b.Pending()[0].ID, Decision{Outcome: Allowed})
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(*events) != 2 || (*events)[0].Kind != "pending" || (*events)[1].Kind != "decided" {
		t.Fatalf("events = %+v", *events)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/broker -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

```go
package broker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

type Outcome string

const (
	Allowed              Outcome = "allowed"
	Denied               Outcome = "denied"
	Expired              Outcome = "expired"
	ApprovedButCancelled Outcome = "approved_but_cancelled"
	SentToTab            Outcome = "sent_to_tab"
)

type Request struct {
	ID, Client, Server, Command, Description string
	Sudo                                     bool
	TimeoutSec                               int
	ReceivedAt                               time.Time
}

type Decision struct {
	Outcome Outcome
	Reason  string
}

type Event struct {
	Kind     string // "pending" | "decided"
	Request  Request
	Decision Decision
}

type Options struct {
	MaxPending int
	Expiry     time.Duration
	Now        func() time.Time
	OnEvent    func(Event)
}

var (
	ErrTooManyPending = errors.New("too many pending requests, try again later")
	ErrNotFound       = errors.New("no such pending request")
)

type pending struct {
	req  Request
	done chan Decision
}

type Broker struct {
	o  Options
	mu sync.Mutex
	q  []*pending // insertion order
}

func New(o Options) *Broker {
	if o.MaxPending <= 0 {
		o.MaxPending = 5
	}
	if o.Expiry <= 0 {
		o.Expiry = 5 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.OnEvent == nil {
		o.OnEvent = func(Event) {}
	}
	return &Broker{o: o}
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (b *Broker) Submit(ctx context.Context, req Request) (Decision, error) {
	if req.ID == "" {
		req.ID = newID()
	}
	if req.ReceivedAt.IsZero() {
		req.ReceivedAt = b.o.Now()
	}
	p := &pending{req: req, done: make(chan Decision, 1)}

	b.mu.Lock()
	if len(b.q) >= b.o.MaxPending {
		b.mu.Unlock()
		return Decision{}, ErrTooManyPending
	}
	b.q = append(b.q, p)
	b.mu.Unlock()
	b.o.OnEvent(Event{Kind: "pending", Request: req})

	timer := time.NewTimer(b.o.Expiry)
	defer timer.Stop()
	select {
	case d := <-p.done:
		return d, nil
	case <-timer.C:
		if b.remove(p.req.ID) != nil {
			d := Decision{Outcome: Expired, Reason: "approval timed out"}
			b.o.OnEvent(Event{Kind: "decided", Request: req, Decision: d})
			return d, nil
		}
		return <-p.done, nil // decided in the same instant
	case <-ctx.Done():
		if b.remove(p.req.ID) != nil {
			return Decision{}, ctx.Err()
		}
		return <-p.done, nil
	}
}

// remove takes a request out of the queue; nil if it was not there.
func (b *Broker) remove(id string) *pending {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, p := range b.q {
		if p.req.ID == id {
			b.q = append(b.q[:i], b.q[i+1:]...)
			return p
		}
	}
	return nil
}

func (b *Broker) Pending() []Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Request, 0, len(b.q))
	for _, p := range b.q {
		out = append(out, p.req)
	}
	return out
}

func (b *Broker) Decide(id string, d Decision) error {
	p := b.remove(id)
	if p == nil {
		return ErrNotFound
	}
	p.done <- d
	b.o.OnEvent(Event{Kind: "decided", Request: p.req, Decision: d})
	return nil
}

func (b *Broker) DenyAll(reason string) {
	b.mu.Lock()
	all := b.q
	b.q = nil
	b.mu.Unlock()
	for _, p := range all {
		d := Decision{Outcome: Denied, Reason: reason}
		p.done <- d
		b.o.OnEvent(Event{Kind: "decided", Request: p.req, Decision: d})
	}
}
```

- [ ] **Step 4: Run tests with the race detector**

Run: `go test -race ./internal/broker -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/broker/broker.go internal/broker/broker_test.go
git commit -m "feat(broker): pending approval queue with cap, expiry, deny-all, events"
```

---

### Task 6: Audit log writer

**Files:**
- Create: `internal/broker/audit.go`
- Test: `internal/broker/audit_test.go`

**Interfaces:**
- Produces:
  ```go
  type AuditRecord struct {
      Time        time.Time `json:"time"`
      Client      string    `json:"client"`
      Server      string    `json:"server"`
      Command     string    `json:"command"`     // already redacted by caller
      Description string    `json:"description,omitempty"`
      Sudo        bool      `json:"sudo,omitempty"`
      TimeoutSec  int       `json:"timeoutSec"`
      Outcome     string    `json:"outcome"`     // Outcome values plus "cancelled_running"
      Reason      string    `json:"reason,omitempty"`
      ExitCode    *int      `json:"exitCode,omitempty"`
      DurationMs  int64     `json:"durationMs,omitempty"`
      StdoutBytes int       `json:"stdoutBytes,omitempty"`
      StderrBytes int       `json:"stderrBytes,omitempty"`
  }
  type Audit struct
  func OpenAudit(path string) (*Audit, error) // O_APPEND|O_CREATE, 0600
  func (a *Audit) Write(r AuditRecord) error
  func (a *Audit) Close() error
  ```

- [ ] **Step 1: Write the failing test**

```go
package broker

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditAppendsJSONLWithMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	code := 0
	if err := a.Write(AuditRecord{Time: time.Now(), Client: "c", Server: "s", Command: "ls", Outcome: "allowed", ExitCode: &code}); err != nil {
		t.Fatal(err)
	}
	if err := a.Write(AuditRecord{Time: time.Now(), Client: "c", Server: "s", Command: "rm", Outcome: "denied", Reason: "no"}); err != nil {
		t.Fatal(err)
	}
	a.Close()
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v", info.Mode().Perm())
	}
	f, _ := os.Open(path)
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		var r AuditRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("line %d not JSON: %v", n, err)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("want 2 lines, got %d", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/broker -run TestAudit -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

```go
package broker

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

type AuditRecord struct {
	Time        time.Time `json:"time"`
	Client      string    `json:"client"`
	Server      string    `json:"server"`
	Command     string    `json:"command"`
	Description string    `json:"description,omitempty"`
	Sudo        bool      `json:"sudo,omitempty"`
	TimeoutSec  int       `json:"timeoutSec"`
	Outcome     string    `json:"outcome"`
	Reason      string    `json:"reason,omitempty"`
	ExitCode    *int      `json:"exitCode,omitempty"`
	DurationMs  int64     `json:"durationMs,omitempty"`
	StdoutBytes int       `json:"stdoutBytes,omitempty"`
	StderrBytes int       `json:"stderrBytes,omitempty"`
}

// Audit appends one JSON object per line. Output content is never part of a
// record; only byte counts are.
type Audit struct {
	mu sync.Mutex
	f  *os.File
}

func OpenAudit(path string) (*Audit, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Audit{f: f}, nil
}

func (a *Audit) Write(r AuditRecord) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err = a.f.Write(append(line, '\n'))
	return err
}

func (a *Audit) Close() error { return a.f.Close() }
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/broker -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/broker/audit.go internal/broker/audit_test.go
git commit -m "feat(broker): JSONL audit writer, 0600, no output content"
```

---

### Task 7: Newline JSON-RPC 2.0 codec (server and client)

**Files:**
- Create: `internal/rpc/rpc.go`
- Test: `internal/rpc/rpc_test.go`

**Interfaces:**
- Produces:
  ```go
  package rpc

  type Handler func(ctx context.Context, params json.RawMessage) (any, error)
  type Server struct
  func NewServer() *Server
  func (s *Server) Handle(method string, h Handler)
  func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error // returns when r hits EOF or ctx done
  func (s *Server) Notify(method string, params any) error                    // safe from any goroutine after Serve started

  type Client struct
  func NewClient(r io.Reader, w io.Writer, onNotify func(method string, params json.RawMessage)) *Client
  func (c *Client) Call(ctx context.Context, method string, params any, result any) error
  func (c *Client) Close() error

  type Error struct { Code int; Message string; Data any }  // implements error
  ```
  Wire format: one JSON object per line. Request `{"jsonrpc":"2.0","id":N,"method":"m","params":{}}`, response `{"jsonrpc":"2.0","id":N,"result":{}}` or `{"jsonrpc":"2.0","id":N,"error":{"code":-32000,"message":"..."}}`, notification has no `id`. Handler errors become `code -32000` with the error string as message, unless the handler returns a `*rpc.Error`. Each request runs in its own goroutine; the handler's ctx is derived from Serve's ctx. Writes are serialized by a mutex.

- [ ] **Step 1: Write the failing test**

```go
package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

func pipePair() (srvR io.Reader, srvW io.Writer, cliR io.Reader, cliW io.Writer) {
	a, b := io.Pipe() // client -> server
	c, d := io.Pipe() // server -> client
	return a, d, c, b
}

func TestCallAndNotify(t *testing.T) {
	srvR, srvW, cliR, cliW := pipePair()
	s := NewServer()
	s.Handle("add", func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct{ A, B int }
		json.Unmarshal(p, &in)
		return map[string]int{"sum": in.A + in.B}, nil
	})
	s.Handle("boom", func(ctx context.Context, p json.RawMessage) (any, error) {
		return nil, &Error{Code: 42, Message: "custom"}
	})
	go s.Serve(context.Background(), srvR, srvW)

	notes := make(chan string, 1)
	c := NewClient(cliR, cliW, func(m string, _ json.RawMessage) { notes <- m })
	var out struct{ Sum int }
	if err := c.Call(context.Background(), "add", map[string]int{"A": 2, "B": 3}, &out); err != nil || out.Sum != 5 {
		t.Fatalf("add: %v %+v", err, out)
	}
	err := c.Call(context.Background(), "boom", nil, nil)
	var re *Error
	if !errors.As(err, &re) || re.Code != 42 || re.Message != "custom" {
		t.Fatalf("want custom error, got %v", err)
	}
	if err := c.Call(context.Background(), "missing", nil, nil); err == nil {
		t.Fatal("unknown method should error")
	}
	s.Notify("ping", map[string]int{"x": 1})
	select {
	case m := <-notes:
		if m != "ping" {
			t.Fatalf("got %q", m)
		}
	case <-time.After(time.Second):
		t.Fatal("notification not received")
	}
}

func TestCallHonoursContext(t *testing.T) {
	srvR, srvW, cliR, cliW := pipePair()
	s := NewServer()
	s.Handle("slow", func(ctx context.Context, p json.RawMessage) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	go s.Serve(context.Background(), srvR, srvW)
	c := NewClient(cliR, cliW, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/rpc -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

```go
package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Message }

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Handler func(ctx context.Context, params json.RawMessage) (any, error)

type Server struct {
	mu       sync.Mutex // guards handlers before Serve, and writes after
	handlers map[string]Handler
	w        io.Writer
}

func NewServer() *Server { return &Server{handlers: map[string]Handler{}} }

func (s *Server) Handle(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

func (s *Server) write(m message) error {
	m.JSONRPC = "2.0"
	line, err := json.Marshal(m)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil {
		return fmt.Errorf("rpc: not serving")
	}
	_, err = s.w.Write(append(line, '\n'))
	return err
}

func (s *Server) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return s.write(message{Method: method, Params: raw})
}

func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	s.mu.Lock()
	s.w = w
	s.mu.Unlock()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil || m.Method == "" {
			continue // ignore garbage and responses; the server never calls out
		}
		s.mu.Lock()
		h := s.handlers[m.Method]
		s.mu.Unlock()
		if m.ID == nil { // notification: nothing to answer
			if h != nil {
				go h(ctx, m.Params)
			}
			continue
		}
		go func(m message) {
			if h == nil {
				s.write(message{ID: m.ID, Error: &Error{Code: -32601, Message: "method not found: " + m.Method}})
				return
			}
			res, err := h(ctx, m.Params)
			if err != nil {
				re, ok := err.(*Error)
				if !ok {
					re = &Error{Code: -32000, Message: err.Error()}
				}
				s.write(message{ID: m.ID, Error: re})
				return
			}
			raw, err := json.Marshal(res)
			if err != nil {
				s.write(message{ID: m.ID, Error: &Error{Code: -32000, Message: err.Error()}})
				return
			}
			s.write(message{ID: m.ID, Result: raw})
		}(m)
	}
	return sc.Err()
}

type Client struct {
	w        io.Writer
	wmu      sync.Mutex
	next     atomic.Int64
	mu       sync.Mutex
	waiting  map[int64]chan message
	onNotify func(string, json.RawMessage)
	closed   chan struct{}
	closer   io.Closer
}

func NewClient(r io.Reader, w io.Writer, onNotify func(method string, params json.RawMessage)) *Client {
	c := &Client{w: w, waiting: map[int64]chan message{}, onNotify: onNotify, closed: make(chan struct{})}
	if cl, ok := w.(io.Closer); ok {
		c.closer = cl
	}
	go c.readLoop(r)
	return c
}

func (c *Client) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		if m.ID == nil {
			if c.onNotify != nil && m.Method != "" {
				c.onNotify(m.Method, m.Params)
			}
			continue
		}
		c.mu.Lock()
		ch := c.waiting[*m.ID]
		delete(c.waiting, *m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
	close(c.closed)
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	id := c.next.Add(1)
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan message, 1)
	c.mu.Lock()
	c.waiting[id] = ch
	c.mu.Unlock()
	line, _ := json.Marshal(message{JSONRPC: "2.0", ID: &id, Method: method, Params: raw})
	c.wmu.Lock()
	_, err = c.w.Write(append(line, '\n'))
	c.wmu.Unlock()
	if err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if result != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.closed:
		return io.ErrUnexpectedEOF
	}
}

func (c *Client) Close() error {
	if c.closer != nil {
		return c.closer.Close()
	}
	return nil
}
```

Note: `Call` with a cancelled ctx returns immediately but does not tell the server; the server-side handler keeps its own ctx from `Serve`. Hub methods that must react to client cancellation (`exec`) receive an explicit `cancel` request instead (Task 9).

- [ ] **Step 4: Run tests with race detector**

Run: `go test -race ./internal/rpc -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/rpc/rpc.go internal/rpc/rpc_test.go
git commit -m "feat(rpc): newline JSON-RPC 2.0 server and client"
```

---

### Task 8: Hub core: vault state, reload, listing, and the exec pipeline

**Files:**
- Create: `internal/hub/hub.go`
- Modify: `internal/mcpserver/resolve.go:22-28` (export a constructor-free path: `Deps` is reused as-is)
- Test: `internal/hub/hub_test.go`

**Interfaces:**
- Consumes: `broker.New/Submit/Pending/Decide/DenyAll`, `broker.Audit`, `mcpserver.Deps.Resolve`, `sshx.Registry.Get`, `sshx.Manager.Exec/ExecSudo`, `config.Load`, `config.SanitizeCommand`, `config.AppendDescription`, `config.NewRedactor`, `config.CapOutput`.
- Produces:
  ```go
  package hub

  type Options struct {
      StorePath string
      Audit     *broker.Audit
      Broker    *broker.Broker           // if nil, New creates one with defaults and forwards events to OnEvent
      OnEvent   func(broker.Event)
      Insecure  bool
      Dialer    func(sshx.DialConfig) Executor // test seam; nil = real Registry
  }
  type Executor interface {
      Exec(ctx context.Context, cmd string) (sshx.ExecResult, error)
      ExecSudo(ctx context.Context, cmd string) (sshx.ExecResult, error)
  }
  type Hub struct
  func New(o Options) (*Hub, error)          // loads store if present; missing store is not an error
  func (h *Hub) Unlock(pw string) error       // derives key, verifies, checks MAC
  func (h *Hub) Lock()
  func (h *Hub) Locked() bool
  func (h *Hub) Reload() error                // re-reads store if Revision on disk changed; keeps master key
  type ServerInfo struct { Name string `json:"name"`; Locked bool `json:"locked"` }
  func (h *Hub) ServersForMCP() []ServerInfo  // only AIVisible
  type ExecRequest struct { Client, Server, Command, Description string; Sudo bool; TimeoutSec int }
  type ExecResponse struct { ExitCode int `json:"exitCode"`; Stdout string `json:"stdout"`; Stderr string `json:"stderr"` }
  func (h *Hub) Exec(ctx context.Context, r ExecRequest) (ExecResponse, error)
  func (h *Hub) Broker() *broker.Broker
  func (h *Hub) Registry() *sshx.Registry
  func (h *Hub) Deps() *mcpserver.Deps        // for TermSession resolution in Task 11

  // Errors with the exact AI-facing text from the spec:
  var ErrServerNotFound = errors.New(`server %q not found`) — constructed via ServerNotFound(name) error
  var ErrLocked      = errors.New("Vault is locked; unlock it in the app")
  var ErrNoHostKey   = errors.New("connect to this server from the app once first")
  type DeniedError struct{ Reason string } // Error(): "Denied by user" + (": " + Reason if set)
  var ErrExpired     = errors.New("Approval timed out after 5 minutes; you may retry")
  ```
  `Exec` order: server exists and AIVisible (else `server "x" not found`, byte-identical for hidden and missing); locked → `ErrLocked`; `HostKey == ""` → `ErrNoHostKey`; sanitize + description (returns the sanitize error verbatim); clamp `TimeoutSec` to [1,600] default 60; `Broker.Submit`; on `Expired` → `ErrExpired`; on `Denied` → `DeniedError`; on `SentToTab` → return `ExecResponse` with `Stdout: "User chose to run this in their terminal; no output captured"` and `ExitCode: 0`; on `Allowed`: re-check `ctx.Err()` (audit `approved_but_cancelled`, return `ctx.Err()`), resolve, exec with `context.WithTimeout(ctx, TimeoutSec)`, redact and cap each stream, audit, return. `ErrCancelled` from sshx is audited as `cancelled_running` and returned as-is.

- [ ] **Step 1: Write the failing tests**

```go
package hub

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

type fakeExec struct {
	calls []string
	res   sshx.ExecResult
	err   error
}

func (f *fakeExec) Exec(ctx context.Context, cmd string) (sshx.ExecResult, error) {
	f.calls = append(f.calls, cmd)
	return f.res, f.err
}
func (f *fakeExec) ExecSudo(ctx context.Context, cmd string) (sshx.ExecResult, error) {
	f.calls = append(f.calls, "sudo:"+cmd)
	return f.res, f.err
}

// newHub writes a vault with two key-auth servers: "vis" (AIVisible, pinned
// host key) and "hid" (hidden). Key-auth servers have no encrypted fields,
// so the vault has no KDF and is never "locked".
func newHub(t *testing.T, fe *fakeExec, decide func(b *broker.Broker)) (*Hub, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	f := &config.File{Version: 1, Servers: []config.Server{
		{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true},
		{Name: "nokey", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true},
		{Name: "hid", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc"},
	}}
	if err := config.Save(path, f, nil); err != nil {
		t.Fatal(err)
	}
	audit, _ := broker.OpenAudit(filepath.Join(dir, "audit.jsonl"))
	b := broker.New(broker.Options{Expiry: 200 * time.Millisecond, OnEvent: func(e broker.Event) {
		if e.Kind == "pending" && decide != nil {
			go decide(nil)
		}
	}})
	h, err := New(Options{StorePath: path, Audit: audit, Broker: b, Dialer: func(sshx.DialConfig) Executor { return fe }})
	if err != nil {
		t.Fatal(err)
	}
	if decide != nil {
		// give the callback access to the broker
		orig := decide
		decide = func(*broker.Broker) { orig(b) }
	}
	return h, path
}

func allowFirst(b *broker.Broker) {
	for len(b.Pending()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	b.Decide(b.Pending()[0].ID, broker.Decision{Outcome: broker.Allowed})
}

func TestServersForMCPOnlyVisible(t *testing.T) {
	h, _ := newHub(t, &fakeExec{}, nil)
	names := []string{}
	for _, s := range h.ServersForMCP() {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "vis,nokey" {
		t.Fatalf("got %v", names)
	}
}

func TestHiddenAndMissingAreByteIdentical(t *testing.T) {
	h, _ := newHub(t, &fakeExec{}, nil)
	_, e1 := h.Exec(context.Background(), ExecRequest{Server: "hid", Command: "ls"})
	_, e2 := h.Exec(context.Background(), ExecRequest{Server: "nope", Command: "ls"})
	if e1 == nil || e2 == nil {
		t.Fatal("both must error")
	}
	want1, want2 := `server "hid" not found`, `server "nope" not found`
	if e1.Error() != want1 || e2.Error() != want2 {
		t.Fatalf("got %q / %q", e1, e2)
	}
}

func TestNoHostKeyRefusedWithoutDialing(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHub(t, fe, nil)
	_, err := h.Exec(context.Background(), ExecRequest{Server: "nokey", Command: "ls"})
	if !errors.Is(err, ErrNoHostKey) {
		t.Fatalf("want ErrNoHostKey, got %v", err)
	}
	if len(fe.calls) != 0 {
		t.Fatal("must not dial")
	}
}

func TestAllowedRunsAndReturnsStreams(t *testing.T) {
	fe := &fakeExec{res: sshx.ExecResult{Stdout: "out\n", Stderr: "err\n", ExitCode: 2}}
	var h *Hub
	h, _ = newHub(t, fe, func(*broker.Broker) {})
	go allowFirst(h.Broker())
	res, err := h.Exec(context.Background(), ExecRequest{Client: "t", Server: "vis", Command: "ls", Description: "list"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 2 || res.Stdout != "out\n" || res.Stderr != "err\n" {
		t.Fatalf("got %+v", res)
	}
	if len(fe.calls) != 1 || fe.calls[0] != "ls # list" {
		t.Fatalf("calls = %v", fe.calls)
	}
}

func TestDeniedAndExpiredAreDistinct(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHub(t, fe, nil)
	go func() {
		b := h.Broker()
		for len(b.Pending()) == 0 {
			time.Sleep(2 * time.Millisecond)
		}
		b.Decide(b.Pending()[0].ID, broker.Decision{Outcome: broker.Denied, Reason: "nope"})
	}()
	_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	var de *DeniedError
	if !errors.As(err, &de) || de.Reason != "nope" || err.Error() != "Denied by user: nope" {
		t.Fatalf("got %v", err)
	}
	_, err = h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}) // nobody decides → expiry 200ms
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
	if len(fe.calls) != 0 {
		t.Fatal("nothing should have executed")
	}
}

func TestApprovedAfterCancelDoesNotExecute(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHub(t, fe, nil)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := h.Exec(ctx, ExecRequest{Server: "vis", Command: "ls"}); errc <- err }()
	b := h.Broker()
	for len(b.Pending()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	id := b.Pending()[0].ID
	cancel()
	<-errc
	if err := b.Decide(id, broker.Decision{Outcome: broker.Allowed}); !errors.Is(err, broker.ErrNotFound) {
		t.Fatalf("decide after cancel: %v", err)
	}
	if len(fe.calls) != 0 {
		t.Fatal("executed after cancel")
	}
}

func TestTimeoutSecClamped(t *testing.T) {
	if clampTimeout(0) != 60 || clampTimeout(-5) != 1 || clampTimeout(9999) != 600 || clampTimeout(120) != 120 {
		t.Fatal("clamp wrong")
	}
}

func TestReloadPicksUpRevisionChange(t *testing.T) {
	h, path := newHub(t, &fakeExec{}, nil)
	f, _ := config.Load(path)
	f.Servers[2].AIVisible = true // "hid" becomes visible
	config.Save(path, f, nil)
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(h.ServersForMCP()) != 3 {
		t.Fatalf("reload missed change: %+v", h.ServersForMCP())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/hub -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

```go
package hub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/mcpserver"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

var (
	ErrLocked    = errors.New("Vault is locked; unlock it in the app")
	ErrNoHostKey = errors.New("connect to this server from the app once first")
	ErrExpired   = errors.New("Approval timed out after 5 minutes; you may retry")
)

func serverNotFound(name string) error { return fmt.Errorf("server %q not found", name) }

type DeniedError struct{ Reason string }

func (e *DeniedError) Error() string {
	if e.Reason == "" {
		return "Denied by user"
	}
	return "Denied by user: " + e.Reason
}

type Executor interface {
	Exec(ctx context.Context, cmd string) (sshx.ExecResult, error)
	ExecSudo(ctx context.Context, cmd string) (sshx.ExecResult, error)
}

type Options struct {
	StorePath string
	Audit     *broker.Audit
	Broker    *broker.Broker
	OnEvent   func(broker.Event)
	Insecure  bool
	Dialer    func(sshx.DialConfig) Executor
}

type ServerInfo struct {
	Name   string `json:"name"`
	Locked bool   `json:"locked"`
}

type ExecRequest struct {
	Client, Server, Command, Description string
	Sudo                                 bool
	TimeoutSec                           int
}

type ExecResponse struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type Hub struct {
	o      Options
	mu     sync.Mutex
	deps   *mcpserver.Deps
	reg    *sshx.Registry
	broker *broker.Broker
	audit  *broker.Audit
}

func New(o Options) (*Hub, error) {
	h := &Hub{o: o, reg: sshx.NewRegistry(), audit: o.Audit}
	h.deps = &mcpserver.Deps{Path: o.StorePath, Insecure: o.Insecure}
	if err := h.loadStore(); err != nil {
		return nil, err
	}
	if o.Broker != nil {
		h.broker = o.Broker
	} else {
		h.broker = broker.New(broker.Options{OnEvent: o.OnEvent})
	}
	return h, nil
}

func (h *Hub) loadStore() error {
	f, err := config.Load(h.o.StorePath)
	if err != nil {
		if os.IsNotExist(err) {
			h.deps.File = nil
			return nil
		}
		return fmt.Errorf("cannot read config store: %w", err)
	}
	h.deps.File = f
	return nil
}

func (h *Hub) Broker() *broker.Broker     { return h.broker }
func (h *Hub) Registry() *sshx.Registry   { return h.reg }
func (h *Hub) Deps() *mcpserver.Deps      { return h.deps }

func (h *Hub) Unlock(pw string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := h.deps.File
	if f == nil || f.KDF == nil {
		return nil // nothing encrypted; key/agent-only vault
	}
	mk, err := f.KDF.DeriveKey(pw)
	if err != nil {
		return err
	}
	if !f.KDF.Verify(mk) {
		return errors.New("wrong master password")
	}
	if err := f.VerifyMAC(mk); err != nil {
		return err
	}
	h.deps.MasterKey = mk
	return nil
}

func (h *Hub) Lock() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.deps.MasterKey {
		h.deps.MasterKey[i] = 0
	}
	h.deps.MasterKey = nil
}

func (h *Hub) Locked() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := h.deps.File
	return f != nil && f.KDF != nil && h.deps.MasterKey == nil
}

// Reload re-reads the store when its on-disk Revision differs. The master
// key is kept: the KDF params do not change on an ordinary save.
func (h *Hub) Reload() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, err := config.Load(h.o.StorePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if h.deps.File != nil && f.Revision == h.deps.File.Revision {
		return nil
	}
	if h.deps.MasterKey != nil {
		if err := f.VerifyMAC(h.deps.MasterKey); err != nil {
			return err
		}
	}
	h.deps.File = f
	return nil
}

func (h *Hub) ServersForMCP() []ServerInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []ServerInfo
	if h.deps.File == nil {
		return out
	}
	locked := h.deps.File.KDF != nil && h.deps.MasterKey == nil
	for _, s := range h.deps.File.Servers {
		if s.AIVisible {
			out = append(out, ServerInfo{Name: s.Name, Locked: locked && h.deps.IsLocked(s.Name)})
		}
	}
	return out
}

func clampTimeout(sec int) int {
	switch {
	case sec == 0:
		return 60
	case sec < 1:
		return 1
	case sec > 600:
		return 600
	}
	return sec
}

func (h *Hub) executor(name string, dc sshx.DialConfig) Executor {
	if h.o.Dialer != nil {
		return h.o.Dialer(dc)
	}
	return h.reg.Get(name, dc)
}

func (h *Hub) record(r broker.AuditRecord) {
	if h.audit != nil {
		_ = h.audit.Write(r)
	}
}

func (h *Hub) Exec(ctx context.Context, r ExecRequest) (ExecResponse, error) {
	h.mu.Lock()
	var srv config.Server
	found := false
	if h.deps.File != nil {
		if s, ok := h.deps.File.FindServer(r.Server); ok && s.AIVisible {
			srv, found = s, true
		}
	}
	locked := h.deps.File != nil && h.deps.File.KDF != nil && h.deps.MasterKey == nil
	h.mu.Unlock()

	if !found {
		return ExecResponse{}, serverNotFound(r.Server)
	}
	if locked && h.deps.IsLocked(r.Server) {
		return ExecResponse{}, ErrLocked
	}
	if srv.HostKey == "" {
		return ExecResponse{}, ErrNoHostKey
	}
	cmd, err := config.SanitizeCommand(r.Command, -1)
	if err != nil {
		return ExecResponse{}, err
	}
	cmd, err = config.AppendDescription(cmd, r.Description)
	if err != nil {
		return ExecResponse{}, err
	}
	timeout := clampTimeout(r.TimeoutSec)

	req := broker.Request{Client: r.Client, Server: r.Server, Command: cmd, Description: r.Description, Sudo: r.Sudo, TimeoutSec: timeout}
	base := broker.AuditRecord{Time: time.Now(), Client: r.Client, Server: r.Server, Command: cmd, Description: r.Description, Sudo: r.Sudo, TimeoutSec: timeout}

	d, err := h.broker.Submit(ctx, req)
	if err != nil {
		return ExecResponse{}, err // ErrTooManyPending or ctx.Err()
	}
	switch d.Outcome {
	case broker.Expired:
		base.Outcome = string(broker.Expired)
		h.record(base)
		return ExecResponse{}, ErrExpired
	case broker.Denied:
		base.Outcome, base.Reason = string(broker.Denied), d.Reason
		h.record(base)
		return ExecResponse{}, &DeniedError{Reason: d.Reason}
	case broker.SentToTab:
		base.Outcome = string(broker.SentToTab)
		h.record(base)
		return ExecResponse{Stdout: "User chose to run this in their terminal; no output captured"}, nil
	}

	if ctx.Err() != nil {
		base.Outcome = string(broker.ApprovedButCancelled)
		h.record(base)
		return ExecResponse{}, ctx.Err()
	}
	dc, err := h.deps.Resolve(r.Server)
	if err != nil {
		return ExecResponse{}, err
	}
	ex := h.executor(r.Server, dc)
	red := config.NewRedactor(dc.Password, dc.SuPassword, dc.SudoPassword, dc.Passphrase)
	base.Command = red.Redact(cmd)

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	start := time.Now()
	var res sshx.ExecResult
	if r.Sudo {
		res, err = ex.ExecSudo(runCtx, cmd)
	} else {
		res, err = ex.Exec(runCtx, cmd)
	}
	base.DurationMs = time.Since(start).Milliseconds()
	base.StdoutBytes, base.StderrBytes = len(res.Stdout), len(res.Stderr)
	if err != nil {
		if errors.Is(err, sshx.ErrCancelled) {
			base.Outcome = "cancelled_running"
		} else {
			base.Outcome = "error"
		}
		base.Reason = red.Redact(err.Error())
		h.record(base)
		return ExecResponse{}, errors.New(red.Redact(err.Error()))
	}
	code := res.ExitCode
	base.Outcome, base.ExitCode = string(broker.Allowed), &code
	h.record(base)
	return ExecResponse{
		ExitCode: res.ExitCode,
		Stdout:   config.CapOutput(red.Redact(res.Stdout), config.DefaultOutputCap),
		Stderr:   config.CapOutput(red.Redact(res.Stderr), config.DefaultOutputCap),
	}, nil
}
```

Note on `ErrLocked`: `Deps.IsLocked` is true only for servers with encrypted fields, so key/agent servers work while the vault is locked, matching the spec.

- [ ] **Step 4: Run tests with race detector**

Run: `go test -race ./internal/hub -v`
Expected: PASS. Adjust `newHub`'s awkward `decide` plumbing if the compiler complains about unused variables; the tests only need `h.Broker()`.

- [ ] **Step 5: Commit**

```bash
git add internal/hub/hub.go internal/hub/hub_test.go
git commit -m "feat(hub): vault state, reload, AI-visible listing, approval-gated exec pipeline"
```

---

### Task 9: MCP door: socket path, listener, peer check, restricted method table

**Files:**
- Create: `internal/hub/sockpath.go`, `internal/hub/listen_unix.go`, `internal/hub/listen_windows.go`, `internal/hub/peer_unix.go`, `internal/hub/peer_windows.go`, `internal/hub/mcpdoor.go`
- Test: `internal/hub/mcpdoor_test.go`, `internal/hub/sockpath_test.go`
- Modify: `go.mod` (`go get golang.org/x/sys@latest github.com/Microsoft/go-winio@latest` become direct)

**Interfaces:**
- Produces:
  ```go
  func SocketPath() (string, error)             // per-OS path from spec; creates parent dir 0700 on unix
  func ListenMCPDoor() (net.Listener, error)    // unix: removes stale socket, listens, chmod 0600; windows: winio.ListenPipe with current-user DACL
  func DialMCPDoor(ctx context.Context) (net.Conn, error) // bridge side; windows verifies server SID first
  func checkPeer(conn net.Conn) error           // unix: SO_PEERCRED/LOCAL_PEERCRED uid == os.Getuid(); windows: client pid token SID == ours
  func ServeMCPDoor(ctx context.Context, ln net.Listener, h *Hub) error // accept loop; per conn: checkPeer then rpc.Server with exactly listServers, exec, sudoExec, cancel
  ```
  MCP-door RPC methods:
  - `listServers` → `[]ServerInfo`
  - `exec` params `{"client":"","server":"","command":"","description":"","timeoutSec":0}` → `ExecResponse`; sets `Sudo:false`
  - `sudoExec` same params → `Sudo:true`
  - `cancel` params `{"requestId":""}`: notification-style; the door tracks in-flight execs per connection by a client-chosen `requestId` field (added to exec params) and cancels that ctx. Connection close cancels all in-flight execs.

- [ ] **Step 1: Write the failing tests**

`internal/hub/sockpath_test.go`:

```go
//go:build unix

package hub

import (
	"os"
	"strings"
	"testing"
)

func TestSocketPathIsShortAndPerUser(t *testing.T) {
	p, err := SocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if len(p) > 100 {
		t.Fatalf("path too long for sun_path: %d %q", len(p), p)
	}
	if !strings.HasSuffix(p, "/ssh-mcp/hub.sock") && !strings.Contains(p, "/ssh-mcp-") {
		t.Fatalf("unexpected path %q", p)
	}
	info, err := os.Stat(strings.TrimSuffix(p, "/hub.sock"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("parent dir must exist with 0700: %v %v", err, info)
	}
}
```

`internal/hub/mcpdoor_test.go`:

```go
package hub

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

func startDoor(t *testing.T, h *Hub) *rpc.Client {
	t.Helper()
	ln, err := ListenMCPDoor()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go ServeMCPDoor(context.Background(), ln, h)
	conn, err := DialMCPDoor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return rpc.NewClient(conn, conn, nil)
}

func TestMCPDoorRejectsUIOnlyMethods(t *testing.T) {
	h, _ := newHub(t, &fakeExec{}, nil)
	c := startDoor(t, h)
	for _, m := range []string{"unlock", "lock", "decide", "denyAll", "pending", "term.open", "servers"} {
		err := c.Call(context.Background(), m, map[string]any{"password": "x"}, nil)
		if err == nil || !strings.Contains(err.Error(), "method not found") {
			t.Fatalf("%s: want method not found, got %v", m, err)
		}
	}
}

func TestMCPDoorListAndExec(t *testing.T) {
	fe := &fakeExec{res: sshx.ExecResult{Stdout: "hi\n"}}
	h, _ := newHub(t, fe, nil)
	c := startDoor(t, h)
	var list []ServerInfo
	if err := c.Call(context.Background(), "listServers", nil, &list); err != nil || len(list) != 2 {
		t.Fatalf("list: %v %+v", err, list)
	}
	go allowFirst(h.Broker())
	var res ExecResponse
	err := c.Call(context.Background(), "exec", map[string]any{"client": "t", "server": "vis", "command": "echo hi"}, &res)
	if err != nil || res.Stdout != "hi\n" {
		t.Fatalf("exec: %v %+v", err, res)
	}
}

func TestMCPDoorCancelWithdrawsPending(t *testing.T) {
	h, _ := newHub(t, &fakeExec{}, nil)
	c := startDoor(t, h)
	errc := make(chan error, 1)
	go func() {
		errc <- c.Call(context.Background(), "exec", map[string]any{"requestId": "r1", "server": "vis", "command": "ls"}, nil)
	}()
	b := h.Broker()
	for len(b.Pending()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if err := c.Call(context.Background(), "cancel", map[string]any{"requestId": "r1"}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("cancelled exec should error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exec did not return after cancel")
	}
	if len(b.Pending()) != 0 {
		t.Fatal("pending not withdrawn")
	}
}

func TestMCPDoorConnectionCloseWithdrawsPending(t *testing.T) {
	h, _ := newHub(t, &fakeExec{}, nil)
	ln, _ := ListenMCPDoor()
	defer ln.Close()
	go ServeMCPDoor(context.Background(), ln, h)
	conn, _ := DialMCPDoor(context.Background())
	c := rpc.NewClient(conn, conn, nil)
	go c.Call(context.Background(), "exec", map[string]any{"server": "vis", "command": "ls"}, nil)
	b := h.Broker()
	for len(b.Pending()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for len(b.Pending()) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(b.Pending()) != 0 {
		t.Fatal("pending survived connection close")
	}
	_ = json.Marshal
	_ = net.Dial
	_ = broker.Allowed
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/hub -run 'TestSocketPath|TestMCPDoor' -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

`internal/hub/sockpath.go`:

```go
package hub

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// SocketPath returns the per-user MCP door endpoint. On Unix it is a socket
// in a short runtime directory (macOS caps sun_path at 104 bytes, so the
// config dir under ~/Library is unusable). On Windows it is a pipe name.
func SocketPath() (string, error) {
	if runtime.GOOS == "windows" {
		sid, err := currentUserSID()
		if err != nil {
			return "", err
		}
		return `\\.\pipe\ssh-mcp-hub-` + sid, nil
	}
	var base string
	switch runtime.GOOS {
	case "darwin":
		base = os.Getenv("TMPDIR")
	default:
		base = os.Getenv("XDG_RUNTIME_DIR")
	}
	dir := filepath.Join(base, "ssh-mcp")
	if base == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("ssh-mcp-%d", os.Getuid()))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "hub.sock"), nil
}
```

`internal/hub/listen_unix.go`:

```go
//go:build unix

package hub

import (
	"context"
	"net"
	"os"
)

func ListenMCPDoor() (net.Listener, error) {
	p, err := SocketPath()
	if err != nil {
		return nil, err
	}
	_ = os.Remove(p) // stale socket from a crashed hub
	ln, err := net.Listen("unix", p)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(p, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

func DialMCPDoor(ctx context.Context) (net.Conn, error) {
	p, err := SocketPath()
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	return d.DialContext(ctx, "unix", p)
}

func currentUserSID() (string, error) { return "", nil } // windows only
```

`internal/hub/peer_unix.go` (Linux and macOS differ in the getsockopt call):

```go
//go:build linux

package hub

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func checkPeer(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Ucred
	var gerr error
	if err := raw.Control(func(fd uintptr) {
		cred, gerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if gerr != nil {
		return gerr
	}
	if int(cred.Uid) != os.Getuid() {
		return fmt.Errorf("peer uid %d is not %d", cred.Uid, os.Getuid())
	}
	return nil
}
```

`internal/hub/peer_darwin.go`:

```go
//go:build darwin

package hub

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func checkPeer(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Xucred
	var gerr error
	if err := raw.Control(func(fd uintptr) {
		cred, gerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return err
	}
	if gerr != nil {
		return gerr
	}
	if int(cred.Uid) != os.Getuid() {
		return fmt.Errorf("peer uid %d is not %d", cred.Uid, os.Getuid())
	}
	return nil
}
```

(Delete the `peer_unix.go` name idea; use `peer_linux.go` and `peer_darwin.go`. Other Unixes are out of scope.)

`internal/hub/listen_windows.go` and `peer_windows.go`:

```go
//go:build windows

package hub

import (
	"context"
	"fmt"
	"net"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func currentUserSID() (string, error) {
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

func ListenMCPDoor() (net.Listener, error) {
	p, err := SocketPath()
	if err != nil {
		return nil, err
	}
	sid, _ := currentUserSID()
	// Owner-only DACL. go-winio opens the first instance with
	// FILE_FLAG_FIRST_PIPE_INSTANCE (verify in vendor source when upgrading).
	return winio.ListenPipe(p, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")"})
}

var (
	kernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	procGetNamedPipeServerProcId = kernel32.NewProc("GetNamedPipeServerProcessId")
	procGetNamedPipeClientProcId = kernel32.NewProc("GetNamedPipeClientProcessId")
)

func pipePeerPID(conn net.Conn, proc *windows.LazyProc) (uint32, error) {
	type fder interface{ Fd() uintptr }
	f, ok := conn.(fder)
	if !ok {
		return 0, fmt.Errorf("pipe conn has no Fd")
	}
	var pid uint32
	r, _, e := proc.Call(f.Fd(), uintptr(unsafe.Pointer(&pid)))
	if r == 0 {
		return 0, e
	}
	return pid, nil
}

func pidUserSID(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return "", err
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

func DialMCPDoor(ctx context.Context) (net.Conn, error) {
	p, err := SocketPath()
	if err != nil {
		return nil, err
	}
	conn, err := winio.DialPipeContext(ctx, p)
	if err != nil {
		return nil, err
	}
	pid, err := pipePeerPID(conn, procGetNamedPipeServerProcId)
	if err != nil {
		conn.Close()
		return nil, err
	}
	theirs, err := pidUserSID(pid)
	mine, _ := currentUserSID()
	if err != nil || theirs != mine {
		conn.Close()
		return nil, fmt.Errorf("pipe server is not owned by the current user")
	}
	return conn, nil
}

func checkPeer(conn net.Conn) error {
	pid, err := pipePeerPID(conn, procGetNamedPipeClientProcId)
	if err != nil {
		return err
	}
	theirs, err := pidUserSID(pid)
	if err != nil {
		return err
	}
	mine, _ := currentUserSID()
	if theirs != mine {
		return fmt.Errorf("peer SID %s is not %s", theirs, mine)
	}
	return nil
}
```

`internal/hub/mcpdoor.go`:

```go
package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/lang315/ssh-mcp/internal/rpc"
)

type execParams struct {
	RequestID   string `json:"requestId,omitempty"`
	Client      string `json:"client"`
	Server      string `json:"server"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	TimeoutSec  int    `json:"timeoutSec,omitempty"`
}

// ServeMCPDoor accepts connections from the bridge. Every connection is
// peer-checked, then served a method table that contains only the three
// AI-facing calls plus cancel. Nothing here can unlock, decide, or read
// secrets: those methods are simply not registered.
func ServeMCPDoor(ctx context.Context, ln net.Listener, h *Hub) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := checkPeer(conn); err != nil {
			fmt.Fprintln(os.Stderr, "mcp door: rejected peer:", err)
			conn.Close()
			continue
		}
		go serveMCPConn(ctx, conn, h)
	}
}

func serveMCPConn(ctx context.Context, conn net.Conn, h *Hub) {
	defer conn.Close()
	connCtx, cancelAll := context.WithCancel(ctx)
	defer cancelAll() // closing the connection withdraws every in-flight exec

	var mu sync.Mutex
	inflight := map[string]context.CancelFunc{}

	s := rpc.NewServer()
	s.Handle("listServers", func(context.Context, json.RawMessage) (any, error) {
		_ = h.Reload()
		list := h.ServersForMCP()
		if list == nil {
			list = []ServerInfo{}
		}
		return list, nil
	})
	exec := func(sudo bool) rpc.Handler {
		return func(_ context.Context, raw json.RawMessage) (any, error) {
			var p execParams
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
			}
			_ = h.Reload()
			reqCtx, cancel := context.WithCancel(connCtx)
			defer cancel()
			if p.RequestID != "" {
				mu.Lock()
				inflight[p.RequestID] = cancel
				mu.Unlock()
				defer func() { mu.Lock(); delete(inflight, p.RequestID); mu.Unlock() }()
			}
			return h.Exec(reqCtx, ExecRequest{Client: p.Client, Server: p.Server, Command: p.Command, Description: p.Description, Sudo: sudo, TimeoutSec: p.TimeoutSec})
		}
	}
	s.Handle("exec", exec(false))
	s.Handle("sudoExec", exec(true))
	s.Handle("cancel", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			RequestID string `json:"requestId"`
		}
		json.Unmarshal(raw, &p)
		mu.Lock()
		if c, ok := inflight[p.RequestID]; ok {
			c()
		}
		mu.Unlock()
		return map[string]bool{"ok": true}, nil
	})
	_ = s.Serve(connCtx, conn, conn)
}
```

Run `go get golang.org/x/sys@v0.47.0 github.com/Microsoft/go-winio@v0.6.2 && go mod tidy` so both are direct dependencies.

- [ ] **Step 4: Run tests**

Run: `go vet ./... && go test -race ./internal/hub -v && GOOS=windows go vet ./internal/hub`
Expected: PASS on the host; the Windows cross-vet must compile (no Windows runtime test here; spec lists the SID check as a manual check).

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/hub/
git commit -m "feat(hub): MCP door over unix socket / named pipe with peer check and restricted methods"
```

---

### Task 10: UI door: full-privilege JSON-RPC on stdio, events

**Files:**
- Create: `internal/hub/uidoor.go`
- Test: `internal/hub/uidoor_test.go`

**Interfaces:**
- Consumes: `rpc.Server`, `Hub`, `broker.Broker`.
- Produces: `func ServeUIDoor(ctx context.Context, h *Hub, r io.Reader, w io.Writer) error` and the method table below. Terminal methods (`term.*`) are added in Task 11; this task registers everything else.

  | Method | Params | Result |
  |---|---|---|
  | `status` | — | `{"locked":bool,"hasStore":bool,"pending":int}` |
  | `unlock` | `{"password":""}` | `{}` or error |
  | `lock` | — | `{}` |
  | `servers` | — | `[{"name","host","port","user","auth","hostKey","aiVisible","locked"}]` (no secrets) |
  | `pending` | — | `[]broker.Request` |
  | `decide` | `{"id":"","outcome":"allowed"\|"denied"\|"sent_to_tab","reason":""}` | `{}` or `ErrNotFound` |
  | `denyAll` | `{"reason":""}` | `{}` |

  Notifications from hub to UI: `pending` `{request}`, `decided` `{request, decision}`. The broker's `OnEvent` is wired to `s.Notify` for the lifetime of `ServeUIDoor`.

- [ ] **Step 1: Write the failing test**

```go
package hub

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/rpc"
)

func startUI(t *testing.T, h *Hub) (*rpc.Client, chan string) {
	t.Helper()
	uiR, hubW := io.Pipe()
	hubR, uiW := io.Pipe()
	go ServeUIDoor(context.Background(), h, hubR, hubW)
	notes := make(chan string, 16)
	c := rpc.NewClient(uiR, uiW, func(m string, _ json.RawMessage) { notes <- m })
	t.Cleanup(func() { uiW.Close(); hubW.Close() })
	return c, notes
}

func TestUIDoorStatusServersAndDecide(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHub(t, fe, nil)
	c, notes := startUI(t, h)

	var st struct {
		Locked   bool `json:"locked"`
		HasStore bool `json:"hasStore"`
	}
	if err := c.Call(context.Background(), "status", nil, &st); err != nil || st.Locked || !st.HasStore {
		t.Fatalf("status: %v %+v", err, st)
	}
	var servers []map[string]any
	if err := c.Call(context.Background(), "servers", nil, &servers); err != nil || len(servers) != 3 {
		t.Fatalf("servers: %v %+v", err, servers)
	}
	for _, s := range servers {
		for _, k := range []string{"encPassword", "password", "encSuPassword"} {
			if _, has := s[k]; has {
				t.Fatalf("servers leaked %s", k)
			}
		}
	}

	errc := make(chan error, 1)
	go func() { _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); errc <- err }()
	select {
	case m := <-notes:
		if m != "pending" {
			t.Fatalf("want pending, got %s", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no pending notification")
	}
	var pend []map[string]any
	c.Call(context.Background(), "pending", nil, &pend)
	if len(pend) != 1 {
		t.Fatalf("pending = %v", pend)
	}
	if err := c.Call(context.Background(), "decide", map[string]any{"id": pend[0]["ID"], "outcome": "denied", "reason": "r"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err == nil || err.Error() != "Denied by user: r" {
		t.Fatalf("got %v", err)
	}
	if m := <-notes; m != "decided" {
		t.Fatalf("want decided, got %s", m)
	}
}

func TestUIDoorDenyAll(t *testing.T) {
	h, _ := newHub(t, &fakeExec{}, nil)
	c, _ := startUI(t, h)
	for i := 0; i < 3; i++ {
		go h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	}
	for len(h.Broker().Pending()) < 3 {
		time.Sleep(2 * time.Millisecond)
	}
	if err := c.Call(context.Background(), "denyAll", map[string]string{"reason": "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(h.Broker().Pending()) != 0 {
		t.Fatal("not cleared")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hub -run TestUIDoor -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

```go
package hub

import (
	"context"
	"encoding/json"
	"io"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/rpc"
)

type uiServer struct {
	Name      string `json:"name"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Auth      string `json:"auth"`
	HostKey   string `json:"hostKey"`
	AIVisible bool   `json:"aiVisible"`
	Locked    bool   `json:"locked"`
}

// ServeUIDoor serves the full-privilege door on r/w (stdio for Electron, an
// in-process pipe for tests and the CLI approver). Broker events are pushed
// as notifications while it runs.
func ServeUIDoor(ctx context.Context, h *Hub, r io.Reader, w io.Writer) error {
	s := rpc.NewServer()
	h.setEventSink(func(e broker.Event) {
		switch e.Kind {
		case "pending":
			s.Notify("pending", map[string]any{"request": e.Request})
		case "decided":
			s.Notify("decided", map[string]any{"request": e.Request, "decision": e.Decision})
		}
	})
	defer h.setEventSink(nil)

	empty := map[string]any{}
	s.Handle("status", func(context.Context, json.RawMessage) (any, error) {
		_ = h.Reload()
		return map[string]any{"locked": h.Locked(), "hasStore": h.deps.File != nil, "pending": len(h.broker.Pending())}, nil
	})
	s.Handle("unlock", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Password string `json:"password"`
		}
		json.Unmarshal(raw, &p)
		return empty, h.Unlock(p.Password)
	})
	s.Handle("lock", func(context.Context, json.RawMessage) (any, error) {
		h.Lock()
		return empty, nil
	})
	s.Handle("servers", func(context.Context, json.RawMessage) (any, error) {
		_ = h.Reload()
		h.mu.Lock()
		defer h.mu.Unlock()
		out := []uiServer{}
		if h.deps.File == nil {
			return out, nil
		}
		for _, sv := range h.deps.File.Servers {
			out = append(out, uiServer{Name: sv.Name, Host: sv.Host, Port: sv.Port, User: sv.User, Auth: sv.Auth,
				HostKey: sv.HostKey, AIVisible: sv.AIVisible, Locked: h.deps.IsLocked(sv.Name)})
		}
		return out, nil
	})
	s.Handle("pending", func(context.Context, json.RawMessage) (any, error) {
		p := h.broker.Pending()
		if p == nil {
			p = []broker.Request{}
		}
		return p, nil
	})
	s.Handle("decide", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID      string `json:"id"`
			Outcome string `json:"outcome"`
			Reason  string `json:"reason"`
		}
		json.Unmarshal(raw, &p)
		switch broker.Outcome(p.Outcome) {
		case broker.Allowed, broker.Denied, broker.SentToTab:
		default:
			return nil, &rpc.Error{Code: -32602, Message: "outcome must be allowed, denied, or sent_to_tab"}
		}
		return empty, h.broker.Decide(p.ID, broker.Decision{Outcome: broker.Outcome(p.Outcome), Reason: p.Reason})
	})
	s.Handle("denyAll", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Reason string `json:"reason"`
		}
		json.Unmarshal(raw, &p)
		h.broker.DenyAll(p.Reason)
		return empty, nil
	})
	registerTermMethods(s, h) // Task 11; a no-op stub until then
	return s.Serve(ctx, r, w)
}
```

Add to `hub.go`:

```go
// setEventSink routes broker events to the active UI door. The broker is
// constructed once with a forwarding closure so the sink can change.
func (h *Hub) setEventSink(f func(broker.Event)) {
	h.mu.Lock()
	h.sink = f
	h.mu.Unlock()
}

func (h *Hub) emit(e broker.Event) {
	h.mu.Lock()
	f := h.sink
	h.mu.Unlock()
	if f != nil {
		f(e)
	}
	if h.o.OnEvent != nil {
		h.o.OnEvent(e)
	}
}
```

Add the field `sink func(broker.Event)` to `Hub`, and in `New` change the default broker construction to `broker.New(broker.Options{OnEvent: h.emit})`. When `Options.Broker` is supplied (tests), wrap it: tests in Task 8 pass their own broker with their own `OnEvent`; for the UI door test, `newHub` must construct the broker with `OnEvent: func(e) { h.emit(e) }`. Simplest: change `newHub` in `hub_test.go` to create the hub first with `Broker: nil` and read `h.Broker()` afterwards, dropping the unused `decide` plumbing. Also set the broker expiry via a new `Options.ApprovalExpiry time.Duration` (default 5 minutes) so tests can shorten it.

Create `internal/hub/term.go` with the stub for now:

```go
package hub

import "github.com/lang315/ssh-mcp/internal/rpc"

func registerTermMethods(s *rpc.Server, h *Hub) {}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/hub -v`
Expected: PASS, including Task 8 and 9 tests after the `newHub` cleanup.

- [ ] **Step 5: Commit**

```bash
git add internal/hub/
git commit -m "feat(hub): UI door with unlock, servers, pending, decide, denyAll, and events"
```

---

### Task 11: `TermSession` and `term.*` methods

**Files:**
- Create: `internal/sshx/term.go`
- Modify: `internal/sshx/manager.go` (add `OpenSession`, `StartKeepalive`)
- Modify: `internal/hub/term.go`
- Test: `internal/sshx/term_test.go` (integration), `internal/hub/term_test.go`

**Interfaces:**
- Produces in `sshx`:
  ```go
  func (m *Manager) OpenSession() (*ssh.Session, error)   // locks only while ensuring the client
  func (m *Manager) StartKeepalive(interval time.Duration, onDead func(reason string)) // idempotent
  type TermSession struct
  func (m *Manager) OpenTerm(rows, cols int, onData func([]byte), onExit func(code int, reason string)) (*TermSession, error)
  func (t *TermSession) Write(p []byte) error
  func (t *TermSession) Resize(rows, cols int) error
  func (t *TermSession) Ack(n int)   // consumer processed n bytes
  func (t *TermSession) Close()
  const HighWater = 1 << 20; const LowWater = 256 << 10
  ```
  Reader goroutine: reads 32 KB chunks from the PTY, calls `onData`, adds to `unacked`. When `unacked > HighWater` it blocks on a cond var until `Ack` brings it under `LowWater`. On EOF/exit it calls `onExit(code, reason)` once.
- Produces in `hub` (UI door methods):
  | Method | Params | Result / Notes |
  |---|---|---|
  | `term.open` | `{"server":"","rows":24,"cols":80}` | `{"id":""}`; resolves via `Deps.Resolve`, `Registry.Get`, `OpenTerm` |
  | `term.write` | `{"id":"","data":"<base64>"}` | `{}` |
  | `term.resize` | `{"id":"","rows":0,"cols":0}` | `{}` |
  | `term.ack` | `{"id":"","n":0}` | `{}` |
  | `term.close` | `{"id":""}` | `{}` |
  Notifications: `term.data` `{"id","data":"<base64>"}`, `term.exit` `{"id","code","reason"}`.

- [ ] **Step 1: Write the failing tests**

`internal/sshx/term_test.go`:

```go
package sshx

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func termManager(t *testing.T) *Manager {
	host, port, cleanup := startSSH(t)
	t.Cleanup(cleanup)
	m := NewManager(DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", Insecure: true, TimeoutMs: 30000})
	t.Cleanup(m.Close)
	return m
}

func TestTermEchoAndResize(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	m := termManager(t)
	var mu sync.Mutex
	var buf bytes.Buffer
	exited := make(chan int, 1)
	ts, err := m.OpenTerm(24, 80, func(b []byte) { mu.Lock(); buf.Write(b); mu.Unlock() }, func(code int, _ string) { exited <- code })
	if err != nil {
		t.Fatal(err)
	}
	ts.Write([]byte("echo TERM-OK\n"))
	waitFor(t, &mu, &buf, "TERM-OK")
	if err := ts.Resize(50, 132); err != nil {
		t.Fatal(err)
	}
	ts.Write([]byte("stty size\n"))
	waitFor(t, &mu, &buf, "50 132")
	ts.Write([]byte("exit 7\n"))
	select {
	case code := <-exited:
		if code != 7 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no exit event")
	}
}

func TestTermFlowControlBoundsUnacked(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	m := termManager(t)
	var mu sync.Mutex
	total := 0
	ts, err := m.OpenTerm(24, 80, func(b []byte) { mu.Lock(); total += len(b); mu.Unlock() }, func(int, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()
	ts.Write([]byte("yes | head -c 20000000\n")) // 20 MB, nobody acks
	time.Sleep(3 * time.Second)
	mu.Lock()
	got := total
	mu.Unlock()
	if got > HighWater+64*1024 {
		t.Fatalf("delivered %d bytes without acks; high water is %d", got, HighWater)
	}
	ts.Ack(got) // drain; must resume
	time.Sleep(2 * time.Second)
	mu.Lock()
	after := total
	mu.Unlock()
	if after <= got {
		t.Fatal("reader did not resume after ack")
	}
}

func waitFor(t *testing.T, mu *sync.Mutex, buf *bytes.Buffer, needle string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := strings.Contains(buf.String(), needle)
		mu.Unlock()
		if ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("did not see %q in %q", needle, buf.String())
}

func TestKeepaliveReportsDeadConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSH(t)
	m := NewManager(DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", Insecure: true, TimeoutMs: 30000})
	defer m.Close()
	if _, err := m.Exec(context.Background(), "true"); err != nil {
		t.Fatal(err)
	}
	dead := make(chan string, 1)
	m.StartKeepalive(500*time.Millisecond, func(r string) { dead <- r })
	cleanup() // stop the container
	select {
	case <-dead:
	case <-time.After(30 * time.Second):
		t.Fatal("keepalive did not report a dead connection")
	}
}
```

`internal/hub/term_test.go` (unit, no Docker): only checks that `term.open` on an unknown server errors and that `term.write` on an unknown id errors — the real path is covered by the E2E test in Task 14.

```go
package hub

import (
	"context"
	"testing"
)

func TestTermMethodsRejectUnknownIDs(t *testing.T) {
	h, _ := newHub(t, &fakeExec{}, nil)
	c, _ := startUI(t, h)
	if err := c.Call(context.Background(), "term.open", map[string]any{"server": "nope", "rows": 24, "cols": 80}, nil); err == nil {
		t.Fatal("unknown server should error")
	}
	if err := c.Call(context.Background(), "term.write", map[string]any{"id": "zzz", "data": ""}, nil); err == nil {
		t.Fatal("unknown id should error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go vet ./internal/sshx ./internal/hub`
Expected: compile errors for the new symbols.

- [ ] **Step 3: Implement**

`internal/sshx/manager.go` additions:

```go
// OpenSession returns a new channel on the shared client. The mutex is held
// only while ensuring the client exists; the caller owns the session.
func (m *Manager) OpenSession() (*ssh.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(); err != nil {
		return nil, err
	}
	return m.client.NewSession()
}

// StartKeepalive pings the server every interval; on failure the client is
// closed, onDead is called once, and the next Exec/OpenSession redials.
func (m *Manager) StartKeepalive(interval time.Duration, onDead func(reason string)) {
	m.mu.Lock()
	if m.keepalive {
		m.mu.Unlock()
		return
	}
	m.keepalive = true
	m.mu.Unlock()
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			m.mu.Lock()
			c := m.client
			m.mu.Unlock()
			if c == nil {
				continue
			}
			if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				m.mu.Lock()
				if m.client == c {
					c.Close()
					m.client = nil
				}
				m.mu.Unlock()
				if onDead != nil {
					onDead("keepalive failed: " + err.Error())
				}
			}
		}
	}()
}
```

Add `keepalive bool` to the `Manager` struct.

`internal/sshx/term.go`:

```go
package sshx

import (
	"errors"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

const (
	HighWater = 1 << 20
	LowWater  = 256 << 10
)

// TermSession is one interactive PTY on a shared client. Output is pushed
// through onData; the consumer must Ack bytes it has rendered, and the
// reader stalls above HighWater until acks bring it under LowWater. While
// stalled the SSH window fills and the remote side blocks: backpressure
// reaches the source instead of buffering anywhere.
type TermSession struct {
	sess    *ssh.Session
	stdin   io.WriteCloser
	mu      sync.Mutex
	cond    *sync.Cond
	unacked int
	closed  bool
}

func (m *Manager) OpenTerm(rows, cols int, onData func([]byte), onExit func(code int, reason string)) (*TermSession, error) {
	sess, err := m.OpenSession()
	if err != nil {
		return nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		sess.Close()
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	if err := sess.Shell(); err != nil {
		sess.Close()
		return nil, err
	}
	t := &TermSession{sess: sess, stdin: stdin}
	t.cond = sync.NewCond(&t.mu)
	var exitOnce sync.Once
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				onData(chunk)
				t.mu.Lock()
				t.unacked += n
				for t.unacked > HighWater && !t.closed {
					t.cond.Wait()
				}
				t.mu.Unlock()
			}
			if err != nil {
				break
			}
		}
		werr := sess.Wait()
		code, reason := 0, ""
		var ee *ssh.ExitError
		if errors.As(werr, &ee) {
			code = ee.ExitStatus()
		} else if werr != nil {
			code, reason = -1, werr.Error()
		}
		exitOnce.Do(func() { onExit(code, reason) })
	}()
	return t, nil
}

func (t *TermSession) Write(p []byte) error {
	_, err := t.stdin.Write(p)
	return err
}

func (t *TermSession) Resize(rows, cols int) error {
	return t.sess.WindowChange(rows, cols)
}

func (t *TermSession) Ack(n int) {
	t.mu.Lock()
	t.unacked -= n
	if t.unacked < 0 {
		t.unacked = 0
	}
	if t.unacked <= LowWater {
		t.cond.Broadcast()
	}
	t.mu.Unlock()
}

func (t *TermSession) Close() {
	t.mu.Lock()
	t.closed = true
	t.cond.Broadcast()
	t.mu.Unlock()
	t.stdin.Close()
	t.sess.Close()
}
```

`internal/hub/term.go`:

```go
package hub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

type termID struct {
	ID string `json:"id"`
}

func registerTermMethods(s *rpc.Server, h *Hub) {
	var mu sync.Mutex
	terms := map[string]*sshx.TermSession{}
	get := func(id string) (*sshx.TermSession, error) {
		mu.Lock()
		defer mu.Unlock()
		t, ok := terms[id]
		if !ok {
			return nil, fmt.Errorf("no terminal %q", id)
		}
		return t, nil
	}
	s.Handle("term.open", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Server     string `json:"server"`
			Rows, Cols int
		}
		json.Unmarshal(raw, &p)
		_ = h.Reload()
		dc, err := h.deps.Resolve(p.Server)
		if err != nil {
			return nil, err
		}
		mgr := h.reg.Get(p.Server, dc)
		id := newTermID()
		mgr.StartKeepalive(30*time.Second, func(reason string) {
			s.Notify("term.exit", map[string]any{"id": id, "code": -1, "reason": reason})
		})
		t, err := mgr.OpenTerm(p.Rows, p.Cols,
			func(b []byte) {
				s.Notify("term.data", map[string]any{"id": id, "data": base64.StdEncoding.EncodeToString(b)})
			},
			func(code int, reason string) {
				mu.Lock()
				delete(terms, id)
				mu.Unlock()
				s.Notify("term.exit", map[string]any{"id": id, "code": code, "reason": reason})
			})
		if err != nil {
			return nil, err
		}
		mu.Lock()
		terms[id] = t
		mu.Unlock()
		return termID{ID: id}, nil
	})
	s.Handle("term.write", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID   string `json:"id"`
			Data string `json:"data"`
		}
		json.Unmarshal(raw, &p)
		t, err := get(p.ID)
		if err != nil {
			return nil, err
		}
		b, err := base64.StdEncoding.DecodeString(p.Data)
		if err != nil {
			return nil, err
		}
		return map[string]any{}, t.Write(b)
	})
	s.Handle("term.resize", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID         string `json:"id"`
			Rows, Cols int
		}
		json.Unmarshal(raw, &p)
		t, err := get(p.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{}, t.Resize(p.Rows, p.Cols)
	})
	s.Handle("term.ack", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID string `json:"id"`
			N  int    `json:"n"`
		}
		json.Unmarshal(raw, &p)
		t, err := get(p.ID)
		if err != nil {
			return nil, err
		}
		t.Ack(p.N)
		return map[string]any{}, nil
	})
	s.Handle("term.close", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p termID
		json.Unmarshal(raw, &p)
		t, err := get(p.ID)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		delete(terms, p.ID)
		mu.Unlock()
		t.Close()
		return map[string]any{}, nil
	})
	_ = broker.Allowed
}

func newTermID() string {
	return fmt.Sprintf("t%d", time.Now().UnixNano())
}
```

- [ ] **Step 4: Run tests**

Run: `go vet ./... && go test -short ./... && go test -race ./internal/sshx -run 'TestTerm|TestKeepalive' -v`
Expected: PASS (integration part needs Docker).

- [ ] **Step 5: Commit**

```bash
git add internal/sshx/manager.go internal/sshx/term.go internal/sshx/term_test.go internal/hub/term.go internal/hub/term_test.go
git commit -m "feat(sshx,hub): PTY terminal sessions with ack flow control, resize, keepalive"
```

---

### Task 12: CLI approver (`hub --cli`) and the `hub` subcommand

**Files:**
- Create: `internal/hub/cliui.go`, `cmd/ssh-mcp/hub.go`
- Modify: `cmd/ssh-mcp/main.go:17-22` (route), `:88-102` (main)
- Test: `cmd/ssh-mcp/main_test.go`

**Interfaces:**
- Produces: `func RunCLIApprover(ctx context.Context, h *Hub, in io.Reader, out io.Writer) error` in `hub`. It prints each pending request and reads `a`/`d [reason]`/`D` (deny all)/`u` (unlock prompt)/`q`. `route()` recognizes `hub` → mode `"hub"`. `runHub(args []string) error` in `cmd/ssh-mcp/hub.go`: flags `--cli`, `--store=<path>` (default `~/.config/ssh-mcp/servers.json`), `--insecureIgnoreHostKey`. It opens the audit at `<storeDir>/audit.jsonl`, listens on the MCP door, and serves either the UI door on stdio or the CLI approver.

- [ ] **Step 1: Write the failing test**

Add to `cmd/ssh-mcp/main_test.go` cases: `{[]string{"hub", "--cli"}, "hub"}` and `{[]string{"hub"}, "hub"}`.

And `internal/hub/cliui_test.go`:

```go
package hub

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestCLIApproverAllowsAndDenies(t *testing.T) {
	h, _ := newHub(t, &fakeExec{}, nil)
	pr, pw := io.Pipe()
	var out bytes.Buffer
	go RunCLIApprover(context.Background(), h, pr, &out)

	errc := make(chan error, 1)
	go func() { _, err := h.Exec(context.Background(), ExecRequest{Client: "claude", Server: "vis", Command: "ls -la", Description: "list"}); errc <- err }()
	for len(h.Broker().Pending()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if !strings.Contains(out.String(), "ls -la") || !strings.Contains(out.String(), "(unverified)") {
		t.Fatalf("prompt missing command or unverified label: %q", out.String())
	}
	pw.Write([]byte("d not now\n"))
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "not now") {
		t.Fatalf("got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/ssh-mcp ./internal/hub -run 'TestRoute|TestCLIApprover' -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/hub/cliui.go`:

```go
package hub

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/lang315/ssh-mcp/internal/broker"
)

// RunCLIApprover is the terminal stand-in for the Electron approval panel.
// It exercises the same broker the UI door will use.
func RunCLIApprover(ctx context.Context, h *Hub, in io.Reader, out io.Writer) error {
	h.setEventSink(func(e broker.Event) {
		if e.Kind == "pending" {
			printRequest(out, e.Request)
		}
	})
	defer h.setEventSink(nil)
	fmt.Fprintln(out, "ssh-mcp hub (CLI approver). Commands: a=allow  d [reason]=deny  D=deny all  s=send to tab  u=unlock  p=list pending  q=quit")
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		cmd, rest, _ := strings.Cut(line, " ")
		pend := h.broker.Pending()
		first := func() (broker.Request, bool) {
			if len(pend) == 0 {
				fmt.Fprintln(out, "nothing pending")
				return broker.Request{}, false
			}
			return pend[0], true
		}
		switch cmd {
		case "a":
			if r, ok := first(); ok {
				h.broker.Decide(r.ID, broker.Decision{Outcome: broker.Allowed})
			}
		case "d":
			if r, ok := first(); ok {
				h.broker.Decide(r.ID, broker.Decision{Outcome: broker.Denied, Reason: rest})
			}
		case "s":
			if r, ok := first(); ok {
				fmt.Fprintf(out, "paste into your terminal (not run here):\n  %s\n", r.Command)
				h.broker.Decide(r.ID, broker.Decision{Outcome: broker.SentToTab})
			}
		case "D":
			h.broker.DenyAll(rest)
		case "u":
			fmt.Fprint(out, "master password: ")
			if sc.Scan() {
				if err := h.Unlock(strings.TrimSpace(sc.Text())); err != nil {
					fmt.Fprintln(out, "unlock failed:", err)
				} else {
					fmt.Fprintln(out, "unlocked")
				}
			}
		case "p":
			for _, r := range pend {
				printRequest(out, r)
			}
		case "q":
			return nil
		default:
			fmt.Fprintln(out, "unknown command")
		}
	}
	return sc.Err()
}

func printRequest(out io.Writer, r broker.Request) {
	sudo := ""
	if r.Sudo {
		sudo = " [SUDO]"
	}
	fmt.Fprintf(out, "\n=== pending %s from %q (unverified)%s\n", r.ID, r.Client, sudo)
	fmt.Fprintf(out, "server : %s\n", r.Server)
	fmt.Fprintf(out, "command: %s\n", r.Command)
	fmt.Fprintf(out, "timeout: %ds\n", r.TimeoutSec)
	if r.Description != "" {
		fmt.Fprintf(out, "AI says (unverified): %s\n", r.Description)
	}
	fmt.Fprint(out, "> ")
}
```

`cmd/ssh-mcp/hub.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/hub"
)

func runHub(args []string) error {
	m := config.ParseArgv(args)
	_, cli := m["cli"]
	_, insecure := m["insecureIgnoreHostKey"]
	store := storePath()
	if p := m["store"]; p != nil && *p != "" {
		store = *p
	}
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		return err
	}
	audit, err := broker.OpenAudit(filepath.Join(filepath.Dir(store), "audit.jsonl"))
	if err != nil {
		return err
	}
	defer audit.Close()

	h, err := hub.New(hub.Options{StorePath: store, Audit: audit, Insecure: insecure})
	if err != nil {
		return err
	}
	defer h.Registry().CloseAll()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ln, err := hub.ListenMCPDoor()
	if err != nil {
		return fmt.Errorf("mcp door: %w", err)
	}
	defer ln.Close()
	go hub.ServeMCPDoor(ctx, ln, h)
	fmt.Fprintf(os.Stderr, "ssh-mcp hub: MCP door at %s\n", ln.Addr())

	if cli {
		return hub.RunCLIApprover(ctx, h, os.Stdin, os.Stdout)
	}
	return hub.ServeUIDoor(ctx, h, os.Stdin, os.Stdout)
}
```

`cmd/ssh-mcp/main.go`: extend `route`:

```go
func route(args []string) (string, []string) {
	if len(args) > 0 {
		switch args[0] {
		case "web":
			return "web", args[1:]
		case "hub":
			return "hub", args[1:]
		}
	}
	return "mcp", args
}
```

and in `main`: `case "hub": err = runHub(rest)`.

- [ ] **Step 4: Run tests and a manual smoke**

Run: `go vet ./... && go test -short ./...`
Expected: PASS.

Manual: `go run ./cmd/ssh-mcp hub --cli --store=/tmp/x.json` prints the door path and the command help; `q` exits cleanly.

- [ ] **Step 5: Commit**

```bash
git add internal/hub/cliui.go internal/hub/cliui_test.go cmd/ssh-mcp/hub.go cmd/ssh-mcp/main.go cmd/ssh-mcp/main_test.go
git commit -m "feat(cmd): hub subcommand with MCP door, UI door on stdio, and --cli approver"
```

---

### Task 13: Bridge mode and removal of headless vault access

**Files:**
- Create: `internal/mcpserver/bridge.go`
- Modify: `cmd/ssh-mcp/main.go:29-75` (`buildDeps`), `:77-90` (`runMCP`)
- Delete: `internal/config/masterpw.go`, `internal/config/masterpw_test.go`
- Test: `internal/mcpserver/bridge_test.go`, `cmd/ssh-mcp/mcp_wire_test.go`

**Interfaces:**
- Produces: `func BuildBridgeServer(dial func(ctx context.Context) (net.Conn, error)) *mcp.Server` with tools `exec`, `sudo-exec`, `list-servers`. Each call dials the hub (one connection per tool call keeps the bridge stateless), sends `requestId` = a random hex, forwards `clientInfo.Name` from `req.Session.InitializeParams().ClientInfo.Name`, sends `notifications/progress` every 5 s while waiting if the request carries a progress token, and on ctx cancel sends `cancel` before closing. Dial failure → `IsError` text `Open the app to approve commands`. Hub error → `IsError` with the hub's message verbatim. Success → `FormatExec`-style text: `exit code: N\nstdout:\n...\nstderr:\n...`.
- `buildDeps` no longer loads the store or the master password: `--host` means CLI only; without `--host` the binary runs in bridge mode.

- [ ] **Step 1: Write the failing tests**

`internal/mcpserver/bridge_test.go` uses an in-process fake hub over a `net.Pipe`-backed listener:

```go
package mcpserver

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeHubListener serves a minimal MCP-door method table for the bridge.
func fakeHub(t *testing.T, execResp map[string]any, execErr string) func(context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		s := rpc.NewServer()
		s.Handle("listServers", func(context.Context, json.RawMessage) (any, error) {
			return []map[string]any{{"name": "vis", "locked": false}}, nil
		})
		s.Handle("exec", func(_ context.Context, raw json.RawMessage) (any, error) {
			if execErr != "" {
				return nil, &rpc.Error{Code: -32000, Message: execErr}
			}
			return execResp, nil
		})
		go s.Serve(context.Background(), server, server)
		return client, nil
	}
}

func callTool(t *testing.T, srv *mcp.Server, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	go srv.Run(context.Background(), st)
	cl := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := cl.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func text(r *mcp.CallToolResult) string { return r.Content[0].(*mcp.TextContent).Text }

func TestBridgeExecSuccess(t *testing.T) {
	srv := BuildBridgeServer(fakeHub(t, map[string]any{"exitCode": 0, "stdout": "hi\n", "stderr": ""}, ""))
	res := callTool(t, srv, "exec", map[string]any{"server": "vis", "command": "echo hi"})
	if res.IsError || !strings.Contains(text(res), "exit code: 0") || !strings.Contains(text(res), "hi") {
		t.Fatalf("got %+v", text(res))
	}
}

func TestBridgeHubErrorVerbatim(t *testing.T) {
	srv := BuildBridgeServer(fakeHub(t, nil, "Denied by user: nope"))
	res := callTool(t, srv, "exec", map[string]any{"server": "vis", "command": "ls"})
	if !res.IsError || text(res) != "Denied by user: nope" {
		t.Fatalf("got %v %q", res.IsError, text(res))
	}
}

func TestBridgeHubDownMessage(t *testing.T) {
	srv := BuildBridgeServer(func(context.Context) (net.Conn, error) { return nil, net.ErrClosed })
	res := callTool(t, srv, "exec", map[string]any{"server": "vis", "command": "ls"})
	if !res.IsError || text(res) != "Open the app to approve commands" {
		t.Fatalf("got %q", text(res))
	}
	res = callTool(t, srv, "list-servers", nil)
	if !res.IsError || text(res) != "Open the app to approve commands" {
		t.Fatalf("got %q", text(res))
	}
}

func TestBridgeListServers(t *testing.T) {
	srv := BuildBridgeServer(fakeHub(t, nil, ""))
	res := callTool(t, srv, "list-servers", nil)
	if res.IsError || !strings.Contains(text(res), "vis") {
		t.Fatalf("got %q", text(res))
	}
}
```

Replace `cmd/ssh-mcp/mcp_wire_test.go` with:

```go
package main

import "testing"

func TestBuildDepsHostOnlyIgnoresStore(t *testing.T) {
	d, disableSudo, maxChars, err := buildDeps([]string{"--host=h", "--user=u", "--disableSudo", "--maxChars=50"})
	if err != nil {
		t.Fatal(err)
	}
	if !disableSudo || maxChars != 50 || d.CLI == nil || !d.CLI.HasHost {
		t.Fatalf("got %+v %v %d", d, disableSudo, maxChars)
	}
	if d.File != nil {
		t.Fatal("--host mode must not load the vault")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpserver ./cmd/ssh-mcp -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

`internal/mcpserver/bridge.go`:

```go
package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const hubDownMsg = "Open the app to approve commands"

type bridgeExecInput struct {
	Server      string `json:"server" jsonschema:"connection name from list-servers"`
	Command     string `json:"command" jsonschema:"shell command to execute; requires human approval in the app"`
	Description string `json:"description,omitempty" jsonschema:"what this command does; shown to the human as unverified"`
	TimeoutSec  int    `json:"timeoutSec,omitempty" jsonschema:"execution timeout in seconds, 1-600, default 60"`
}

type hubExecResp struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

func clientName(req *mcp.CallToolRequest) string {
	if req != nil && req.Session != nil {
		if ip := req.Session.InitializeParams(); ip != nil && ip.ClientInfo != nil {
			return ip.ClientInfo.Name
		}
	}
	return "unknown"
}

func randID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// BuildBridgeServer forwards the three AI tools to the hub's MCP door. The
// bridge validates nothing and holds no secrets; the hub is the policy point.
func BuildBridgeServer(dial func(ctx context.Context) (net.Conn, error)) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "SSH MCP Server", Version: "3.0.0"}, nil)

	withHub := func(ctx context.Context, fn func(c *rpc.Client) (*mcp.CallToolResult, error)) (*mcp.CallToolResult, error) {
		conn, err := dial(ctx)
		if err != nil {
			return textErr(hubDownMsg), nil
		}
		defer conn.Close()
		return fn(rpc.NewClient(conn, conn, nil))
	}

	exec := func(method string) func(context.Context, *mcp.CallToolRequest, bridgeExecInput) (*mcp.CallToolResult, any, error) {
		return func(ctx context.Context, req *mcp.CallToolRequest, in bridgeExecInput) (*mcp.CallToolResult, any, error) {
			res, err := withHub(ctx, func(c *rpc.Client) (*mcp.CallToolResult, error) {
				id := randID()
				params := map[string]any{
					"requestId": id, "client": clientName(req), "server": in.Server,
					"command": in.Command, "description": in.Description, "timeoutSec": in.TimeoutSec,
				}
				// Progress keeps clients that reset their timeout on progress alive
				// while the human decides.
				stop := make(chan struct{})
				defer close(stop)
				if tok := req.Params.GetProgressToken(); tok != nil {
					go func() {
						t := time.NewTicker(5 * time.Second)
						defer t.Stop()
						for {
							select {
							case <-t.C:
								_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: tok, Message: "waiting for approval in the app"})
							case <-stop:
								return
							}
						}
					}()
				}
				var out hubExecResp
				callErr := c.Call(ctx, method, params, &out)
				if ctx.Err() != nil {
					// Client cancelled: tell the hub so the pending request is withdrawn.
					cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					_ = c.Call(cctx, "cancel", map[string]string{"requestId": id}, nil)
					return textErr("cancelled by client"), nil
				}
				if callErr != nil {
					return textErr(callErr.Error()), nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "exit code: %d\n", out.ExitCode)
				if out.Stdout != "" {
					b.WriteString("stdout:\n" + out.Stdout)
					if !strings.HasSuffix(out.Stdout, "\n") {
						b.WriteString("\n")
					}
				}
				if out.Stderr != "" {
					b.WriteString("stderr:\n" + out.Stderr)
				}
				return textOK(b.String()), nil
			})
			return res, nil, err
		}
	}

	mcp.AddTool(s, &mcp.Tool{Name: "exec", Description: "Run a shell command on a saved SSH server. A human must approve it in the ssh-mcp app first."}, exec("exec"))
	mcp.AddTool(s, &mcp.Tool{Name: "sudo-exec", Description: "Run a shell command with sudo on a saved SSH server. A human must approve it in the ssh-mcp app first."}, exec("sudoExec"))
	mcp.AddTool(s, &mcp.Tool{Name: "list-servers", Description: "List the SSH servers the user has made visible to AI, with lock status."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, any, error) {
			res, err := withHub(ctx, func(c *rpc.Client) (*mcp.CallToolResult, error) {
				var list []struct {
					Name   string `json:"name"`
					Locked bool   `json:"locked"`
				}
				if err := c.Call(ctx, "listServers", nil, &list); err != nil {
					return textErr(err.Error()), nil
				}
				if len(list) == 0 {
					return textOK("(no servers are visible to AI; enable 'Visible to AI' on a server in the app)"), nil
				}
				var b strings.Builder
				for _, s := range list {
					lock := ""
					if s.Locked {
						lock = " [locked: unlock the app]"
					}
					fmt.Fprintf(&b, "- %s%s\n", s.Name, lock)
				}
				return textOK(b.String()), nil
			})
			return res, nil, err
		})
	return s
}
```

`cmd/ssh-mcp/main.go`: replace `buildDeps` and `runMCP`:

```go
func buildDeps(args []string) (*mcpserver.Deps, bool, int, error) {
	m := config.ParseArgv(args)
	_, insecure := m["insecureIgnoreHostKey"]
	if insecure {
		fmt.Fprintln(os.Stderr, "WARNING: --insecureIgnoreHostKey disables SSH host key verification (MITM risk)")
	}
	_, disableSudo := m["disableSudo"]
	maxChars := config.ParseMaxChars(m["maxChars"])
	cli, err := config.BuildCLIConfig(m)
	if err != nil {
		return nil, false, 0, err
	}
	cli.MaxChars = maxChars
	return &mcpserver.Deps{CLI: &cli, Insecure: insecure}, disableSudo, maxChars, nil
}

func runMCP(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	m := config.ParseArgv(args)
	if _, hasHost := m["host"]; !hasHost {
		fmt.Fprintln(os.Stderr, "SSH MCP bridge on stdio (forwarding to ssh-mcp hub)")
		return mcpserver.BuildBridgeServer(hub.DialMCPDoor).Run(ctx, &mcp.StdioTransport{})
	}
	d, disableSudo, maxChars, err := buildDeps(args)
	if err != nil {
		return err
	}
	reg := sshx.NewRegistry()
	defer reg.CloseAll()
	srv := mcpserver.BuildServer(d, reg, disableSudo, maxChars)
	fmt.Fprintln(os.Stderr, "SSH MCP Server running on stdio (standalone --host mode)")
	return srv.Run(ctx, &mcp.StdioTransport{})
}
```

Import `github.com/lang315/ssh-mcp/internal/hub`. Delete `internal/config/masterpw.go` and `masterpw_test.go`; `git grep ResolveMasterPassword` must return nothing.

- [ ] **Step 4: Run tests**

Run: `go vet ./... && go test -race ./internal/mcpserver ./cmd/ssh-mcp -v && go test -short ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/bridge.go internal/mcpserver/bridge_test.go cmd/ssh-mcp/main.go cmd/ssh-mcp/mcp_wire_test.go
git rm internal/config/masterpw.go internal/config/masterpw_test.go
git commit -m "feat(cmd): bridge mode forwards MCP tools to the hub; remove headless vault access"
```

---

### Task 14: End-to-end test: Claude-like client → bridge → hub → sshd

**Files:**
- Create: `cmd/ssh-mcp/e2e_test.go`

**Interfaces:**
- Consumes: everything above. Builds the binary into `t.TempDir()`, starts `hub` in-process (so the test can auto-approve through `h.Broker()`), spawns the bridge via `mcp.NewCommandTransport(exec.Command(bin))`, and runs the tool calls the spec's success criteria describe.

- [ ] **Step 1: Write the test**

```go
package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/hub"
	"github.com/lang315/ssh-mcp/internal/sshx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startSSHD(t *testing.T) (string, int) {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		Image:        "lscr.io/linuxserver/openssh-server:latest",
		ExposedPorts: []string{"2222/tcp"},
		Env:          map[string]string{"PASSWORD_ACCESS": "true", "USER_NAME": "test", "USER_PASSWORD": "testpass"},
		WaitingFor:   wait.ForListeningPort("2222/tcp").WithStartupTimeout(60 * time.Second),
	}, Started: true})
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { c.Terminate(ctx) })
	h, _ := c.Host(ctx)
	p, _ := c.MappedPort(ctx, "2222")
	return h, p.Int()
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ssh-mcp")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestEndToEndApprovalFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port := startSSHD(t)
	bin := buildBinary(t)

	// Vault with a password server: the host key must be pinned first, so
	// connect once directly (as the app would) to learn it.
	dir := t.TempDir()
	store := filepath.Join(dir, "servers.json")
	k, mk, _ := config.NewKDF("pw")
	f := &config.File{Version: 1, KDF: &k, Servers: []config.Server{{Name: "box", Host: host, Port: port, User: "test", Auth: "password", AIVisible: true}}}
	enc, _ := config.Encrypt(mk, "box/encPassword", config.AADFor(f, f.Servers[0], "encPassword"), "testpass")
	f.Servers[0].EncPassword = enc
	if err := config.Save(store, f, mk); err != nil {
		t.Fatal(err)
	}
	m := sshx.NewManager(sshx.DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", TimeoutMs: 30000,
		OnLearnHostKey: func(fp string) { _ = config.RecordHostKey(store, "box", fp, mk) }})
	if _, err := m.Exec(context.Background(), "true"); err != nil {
		t.Fatal(err)
	}
	m.Close()

	audit, _ := broker.OpenAudit(filepath.Join(dir, "audit.jsonl"))
	h, err := hub.New(hub.Options{StorePath: store, Audit: audit})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	ln, err := hub.ListenMCPDoor()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.ServeMCPDoor(ctx, ln, h)

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code-test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(bin)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// 1. list-servers shows the visible server.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list-servers"})
	if err != nil || res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "box") {
		t.Fatalf("list: %v %+v", err, res)
	}

	// 2. exec is approved → output returned, redacted.
	go func() {
		b := h.Broker()
		for len(b.Pending()) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		r := b.Pending()[0]
		if r.Client != "claude-code-test" {
			t.Errorf("client name not forwarded: %q", r.Client)
		}
		b.Decide(r.ID, broker.Decision{Outcome: broker.Allowed})
	}()
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "exec", Arguments: map[string]any{"server": "box", "command": "echo hello; echo testpass"}})
	if err != nil || res.IsError {
		t.Fatalf("exec: %v %+v", err, res)
	}
	txt := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(txt, "hello") || strings.Contains(txt, "testpass") || !strings.Contains(txt, "exit code: 0") {
		t.Fatalf("output wrong or leaked secret: %q", txt)
	}

	// 3. denied → reason returned.
	go func() {
		b := h.Broker()
		for len(b.Pending()) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		b.Decide(b.Pending()[0].ID, broker.Decision{Outcome: broker.Denied, Reason: "not today"})
	}()
	res, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "exec", Arguments: map[string]any{"server": "box", "command": "rm -rf /"}})
	if !res.IsError || res.Content[0].(*mcp.TextContent).Text != "Denied by user: not today" {
		t.Fatalf("deny: %+v", res)
	}

	// 4. hub down → the fixed message.
	cancel()
	ln.Close()
	cs2, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: exec.Command(bin)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs2.Close()
	res, _ = cs2.CallTool(context.Background(), &mcp.CallToolParams{Name: "exec", Arguments: map[string]any{"server": "box", "command": "ls"}})
	if !res.IsError || res.Content[0].(*mcp.TextContent).Text != "Open the app to approve commands" {
		t.Fatalf("hub down: %+v", res)
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./cmd/ssh-mcp -run TestEndToEnd -v` (needs Docker, ~1 minute).
Expected: PASS. If `CommandTransport` field names differ in go-sdk v1.7.0, check `mcp/cmd.go:20` and adapt the literal.

- [ ] **Step 3: Commit**

```bash
git add cmd/ssh-mcp/e2e_test.go
git commit -m "test(cmd): end-to-end bridge → hub → sshd approval flow"
```

---

### Task 15: Docs and CI

**Files:**
- Modify: `README.md` (§Multi-server, §Client Setup), `CLAUDE.md` (§What this is, §Commands, §Architecture)
- Modify: `.github/workflows/ci.yml` (add `-race`)

- [ ] **Step 1: README**

Replace the "Multi-server + web config" section's headless paragraph (the `SSH_MCP_MASTER_PASSWORD_FILE` block) with:

```markdown
### Using saved servers from an AI client

Saved servers are only reachable through the hub, which asks you to approve
every command:

    ssh-mcp hub --cli          # terminal approver (the desktop app replaces this)

Then register the bridge with your MCP client with no flags:

    claude mcp add --transport stdio ssh-mcp -- ssh-mcp

The AI sees only servers with "Visible to AI" enabled in `ssh-mcp web`, and
only after you have connected to them once so their host key is pinned.
With the hub closed, every tool call returns "Open the app to approve
commands". There is no headless mode for the vault; `--host` mode is the
standalone option and never reads the vault.
```

Update the tools list: `exec`/`sudo-exec` gain `timeoutSec` (1–600, default 60); results carry `exit code`, `stdout`, `stderr`; `list-servers` shows only AI-visible servers.

- [ ] **Step 2: CLAUDE.md**

In "What this is", add the third mode. In "Commands", add `go test -race ./internal/broker ./internal/hub ./internal/rpc` and `go run ./cmd/ssh-mcp hub --cli`. In "Architecture", add a bullet group:

```markdown
**Hub and bridge** (`internal/hub`, `internal/broker`, `internal/rpc`, `internal/mcpserver/bridge.go`):
- `ssh-mcp hub` owns the unlocked vault, the `sshx.Registry`, and the approval `broker`. It serves a full-privilege JSON-RPC "UI door" on stdio (Electron, or `--cli`) and a restricted "MCP door" on a per-user Unix socket / named pipe (`listServers`, `exec`, `sudoExec`, `cancel` only; peer UID/SID checked).
- Without `--host`, `ssh-mcp` is a stateless bridge: MCP stdio in, one hub connection per tool call. It validates nothing; the hub is the single policy point.
- Every MCP exec goes through `broker.Submit` and blocks for a human decision (cap 5 pending, 5-minute expiry). No auto-approval rules exist; do not add any without a spec change.
- `Hub.Exec` order matters: visible → unlocked → host key pinned → sanitize → approve → re-check ctx → run with per-request timeout → redact and cap each stream → audit. Hidden and nonexistent servers return byte-identical errors.
```

- [ ] **Step 3: CI**

In `.github/workflows/ci.yml` change the test step to `go test -race ./...`.

- [ ] **Step 4: Verify and commit**

Run: `go vet ./... && go test -short -race ./...`
Expected: PASS.

```bash
git add README.md CLAUDE.md .github/workflows/ci.yml
git commit -m "docs: hub, bridge, and approval flow; CI runs tests with -race"
```

---

## Self-Review

**Spec coverage (slice 1a scope):**
- Threat model controls: approval on every exec (T8), display-safe commands (T1), unverified labels (T12 CLI; Electron in 1b), MCP door cannot unlock/approve/read (T9 method table + test), AIVisible gating (T3, T8), egress cap (T2, T8). ✔
- Components: `cmd/ssh-mcp` three modes (T12, T13), `internal/broker` (T5, T6), `internal/hub` two doors (T9, T10), `TermSession` (T11), `Manager.OpenSession` (T11). `desktop/` → plan 1b. ✔
- Data model `AIVisible` and web checkbox (T3). ✔
- AI command flow steps 1–8 (T8, T9, T13). Non-modal panel, 500 ms Allow lock, Deny default → plan 1b (UI). "Send to tab" outcome exists (T5, T8, T12 prints the command). ✔
- Timeouts: expiry vs deny distinct (T5, T8), `timeoutSec` clamp (T8), progress every 5 s (T13), cancel → SIGKILL + honest message (T4, T8), `cancel` RPC and connection-close withdrawal (T9). ✔
- Command validation incl. `\t` (T1). Output cap per stream (T2, T8). Audit fields (T6, T8). Error table strings (T8, T13). `listServers` while locked (T8 `ServersForMCP`, T9). ✔
- Terminal sessions: shared client, PTY, flow control watermarks, resize, keepalive, exit events (T11). Throughput criteria and hub crash/backoff → plan 1b (Electron main owns restart). ✔
- Socket path rules and Windows pipe requirements (T9). ✔
- Breaking change: `SSH_MCP_MASTER_PASSWORD_FILE` removed (T13). ✔
- Testing list: sanitize table, cap, broker (expiry, cancel, approve-after-cancel, cap 6th, deny all, clamp), hub security (UI-only rejected, foreign UID rejected — covered by `checkPeer`; a cross-UID test is manual), hidden == missing byte-identical, no-host-key refusal without dial, listServers locked, term echo/resize/flow/keepalive, E2E bridge. ✔ Foreign-UID and Windows SID checks are manual per spec.

**Not in this plan (deferred to 1b):** Electron main/preload/renderer, notifications, tray badge, non-modal panel, hub restart backoff, throughput measurement, Playwright smoke.

**Placeholder scan:** none.

**Type consistency:** `ExecResult{Stdout, Stderr string; ExitCode int}` used in T4, T8, T13 fake. `broker.Request` field names used in T10 JSON as Go field names (`ID`); the UI door test reads `pend[0]["ID"]` accordingly. `hub.ExecRequest`/`ExecResponse` JSON tags match `execParams`/`hubExecResp` in T9/T13. `rpc.Error` code `-32601` for unknown method is what T9's test greps ("method not found"). `newHub` helper is simplified in T10 Step 3; T8/T9 tests rely only on `h.Broker()` and `allowFirst`.
