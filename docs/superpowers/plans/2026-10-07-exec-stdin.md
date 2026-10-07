# `exec` stdin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The MCP `exec` tool takes an optional `stdin` string that is piped to the command, so an AI agent can write multi-line files (`cat > f`, `git apply`) through sshgate's approval, grant, audit and redaction paths.

**Architecture:** `stdin` travels the existing exec path: bridge → MCP door → `Hub.Exec` (validated, refused for sudo and su hosts) → `broker.Request` (shown to the approver) → `Executor.ExecStdin` → `sshx.Manager.runOnce`, which already pipes stdin. The audit record carries it vault-redacted. The UI-door protocol goes to 11 so an app that cannot show stdin is refused.

**Tech Stack:** Go 1.x (`golang.org/x/crypto/ssh`, `github.com/modelcontextprotocol/go-sdk/mcp`), Electron + React + TypeScript, vitest, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-07-exec-stdin-design.md`

## Global Constraints

- Stdin limit: 256 KiB (`config.MaxStdin = 256 << 10`), counted in bytes.
- Stdin runes: `\n`, `\t`, `\r` allowed; every other control character and every `unicode.Cf` rune rejected; the substring `[REDACTED:` rejected.
- Error texts, verbatim: `stdin is not supported with sudo-exec`, `stdin is not supported on this server`, `send to tab is not available for a command with stdin`.
- No new auto-approval rule: a stdin exec is an `exec`.
- `sudo-exec`'s MCP input schema has no `stdin` field, in both modes.
- UI-door `ProtocolVersion` 11 (Go) and `PROTOCOL_VERSION` 11 (TS).
- The audit `stdin` field is `red.Redact(stdin)`; the no-vault audit path caps it at 4096 bytes.
- `autoAllow.ran` carries `stdinBytes` (an int), never the content.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

Notes against the spec, found while planning:
- `TestRedactCorpus` runs `RedactPatterns` only, so counting vault masks does not change the corpus goldens. No `-update` is needed; Task 2 runs the corpus test to confirm.
- `TestExecWithoutPatternSecretsHasNoRedacted` (`internal/hub/redact_test.go:72`) pins today's "vault mask is not counted" rule. Task 2 rewrites it to the new rule.
- The protocol bump is checked by the existing literal in `internal/hub/uidoor_test.go:208-209`, updated in Task 5. No new constant test.

---

### Task 1: `config.ValidateStdin` and the stdin errors

**Files:**
- Modify: `internal/config/sanitize.go`
- Test: `internal/config/sanitize_test.go`

**Interfaces:**
- Produces: `config.MaxStdin` (const int, `256 << 10`), `config.ValidateStdin(s string) error`, `config.ErrStdinSudo`, `config.ErrStdinSu` (both `error`, texts in Global Constraints). Used by Tasks 5 and 7.

- [ ] **Step 1: Write the failing test** — append to `internal/config/sanitize_test.go`:

```go
func TestValidateStdin(t *testing.T) {
	ok := []string{"", "a\nb\n", "col1\tcol2\n", "crlf\r\n", "xin chào\n", strings.Repeat("x", MaxStdin)}
	for _, s := range ok {
		if err := ValidateStdin(s); err != nil {
			t.Errorf("%q: %v", s[:min(len(s), 20)], err)
		}
	}
	bad := []struct{ name, in, want string }{
		{"esc", "a\x1b[2Jb", "forbidden character U+001B"},
		{"nul", "a\x00b", "forbidden character U+0000"},
		{"bidi override", "echo ‮gnp.txt", "forbidden character U+202E"},
		{"zero width space", "rm​ -rf", "forbidden character U+200B"},
		{"c1 control", "a\u0085b", "forbidden character U+0085"},
		{"too long", strings.Repeat("x", MaxStdin+1), "stdin too long"},
		{"masked output", "PASS=[REDACTED:password]\n", "[REDACTED:"},
	}
	for _, c := range bad {
		if err := ValidateStdin(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", c.name, err, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run TestValidateStdin`
Expected: FAIL, `undefined: MaxStdin` / `undefined: ValidateStdin`.

- [ ] **Step 3: Write minimal implementation** — in `internal/config/sanitize.go`, add `"errors"` to the imports and append:

```go
// MaxStdin bounds the stdin of one exec: it is shown in full to the
// approver and stored in full in the audit record.
const MaxStdin = 256 << 10

var (
	ErrStdinSudo = errors.New("stdin is not supported with sudo-exec")
	ErrStdinSu   = errors.New("stdin is not supported on this server")
)

// ValidateStdin applies the command's rune rules to an exec's stdin, except
// that newlines, tabs and carriage returns are allowed: stdin is file
// content. It also refuses a "[REDACTED:" marker, which only appears in
// masked output: writing that back would corrupt the file.
func ValidateStdin(s string) error {
	if len(s) > MaxStdin {
		return fmt.Errorf("stdin too long (max %d bytes)", MaxStdin)
	}
	for i, r := range s {
		if r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return fmt.Errorf("stdin contains forbidden character U+%04X at position %d", r, i)
		}
	}
	if strings.Contains(s, "[REDACTED:") {
		return errors.New("stdin contains a [REDACTED:…] marker: it is masked output, and writing it back would corrupt the file")
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config -run 'TestValidateStdin|TestSanitize'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/sanitize.go internal/config/sanitize_test.go
git commit -m "feat(config): ValidateStdin for exec stdin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Count vault-secret masks as kind `secret`

**Files:**
- Modify: `internal/config/redact.go`, `internal/config/patterns.go:492-523`
- Modify (rewrite one test): `internal/hub/redact_test.go:72-95`
- Test: `internal/config/redact_test.go`, `internal/config/redactcap_test.go`

**Interfaces:**
- Produces: `(*Redactor).RedactCount(s string) (string, int)`. `RedactCap` and `RedactCapStreams` now add `counts["secret"]` for vault masks. Seen by the AI's `note:` line and by the audit `redacted` field.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/redact_test.go`:

```go
func TestRedactorCount(t *testing.T) {
	r := NewRedactor("hunter2", "root#pw")
	out, n := r.RedactCount("hunter2 hunter2 root#pw none")
	if out != "*** *** *** none" || n != 3 {
		t.Fatalf("got %q %d", out, n)
	}
	if _, n := NewRedactor().RedactCount("hunter2"); n != 0 {
		t.Fatalf("empty redactor counted %d", n)
	}
}
```

Append to `internal/config/redactcap_test.go`:

```go
// A vault secret is masked as "***" and counted as kind "secret", so the AI
// learns the output was masked and does not write it back whole.
func TestRedactCapCountsVaultSecrets(t *testing.T) {
	out, c := RedactCap(NewRedactor("s3cr3t-pw"), "DB_PASSWORD=s3cr3t-pw\nAPI=s3cr3t-pw\n", capMax)
	if out != "DB_PASSWORD=***\nAPI=***\n" || !maps.Equal(c, map[string]int{"secret": 2}) {
		t.Fatalf("got %q %v", out, c)
	}
	// A cut output still counts the masks in the part the cap drops.
	big := "x=s3cr3t-pw\n" + filler(1<<20) + "y=s3cr3t-pw\n" + filler(1<<20) + "z=s3cr3t-pw\n"
	if _, c := RedactCap(NewRedactor("s3cr3t-pw"), big, capMax); c["secret"] != 3 {
		t.Fatalf("big: counts %v", c)
	}
}
```

Rewrite `TestExecWithoutPatternSecretsHasNoRedacted` in `internal/hub/redact_test.go` (lines 72-95 plus the rest of the function body, which checks the audit record) so it pins the new rule. Replace its comment, name and assertions:

```go
// A vault secret is masked first, as "***", and counted as kind "secret":
// in the response, in its JSON on the MCP door, and in the audit record.
func TestExecVaultSecretIsCounted(t *testing.T) {
	h, path, _ := newEncHub(t, &fakeExec{res: sshx.ExecResult{Stdout: "DB_PASSWORD=s3cr3t-pw\nhello\n"}})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	go allowFirst(t, h.Broker())
	res, err := h.Exec(context.Background(), ExecRequest{Server: "enc", Command: "cat app.env"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "DB_PASSWORD=***\nhello\n" || !maps.Equal(res.Redacted, map[string]int{"secret": 1}) {
		t.Fatalf("got %+v", res)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"redacted":{"secret":1}`) {
		t.Fatalf("MCP door JSON: %s", b)
	}
	_, recs := readAudit(t, path)
	if r, ok := recs[len(recs)-1]["redacted"].(map[string]any); !ok || r["secret"] != float64(1) {
		t.Fatalf("audit redacted = %v", recs[len(recs)-1]["redacted"])
	}
}
```

Before replacing, read the original function to the end (it continues past line 95) so nothing it checks besides the "no redacted" rule is lost. Add `"maps"` to that file's imports if it is not there.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config -run 'TestRedactorCount|TestRedactCapCountsVaultSecrets' && go test ./internal/hub -run TestExecVaultSecretIsCounted`
Expected: FAIL, `RedactCount undefined`, then counts `map[]` instead of `map[secret:…]`.

- [ ] **Step 3: Write minimal implementation**

`internal/config/redact.go`, replace `Redact` with:

```go
func (r *Redactor) Redact(s string) string {
	s, _ = r.RedactCount(s)
	return s
}

// RedactCount masks every vault secret as "***" and reports how many
// occurrences it replaced.
func (r *Redactor) RedactCount(s string) (string, int) {
	n := 0
	for _, sec := range r.secrets {
		n += strings.Count(s, sec)
		s = strings.ReplaceAll(s, sec, "***")
	}
	return s, n
}
```

`internal/config/patterns.go`: rename the current `RedactCap` body to `capPatterns(s string, max int) (string, map[string]int)`, deleting its first line `s = r.Redact(s)`, and add above it:

```go
func RedactCap(r *Redactor, s string, max int) (string, map[string]int) {
	s, n := r.RedactCount(s)
	out, c := capPatterns(s, max)
	if n > 0 {
		if c == nil {
			c = map[string]int{}
		}
		c["secret"] += n
	}
	return out, c
}
```

Keep the existing doc comment on `RedactCap`, and add this line at its end: `Vault secrets masked as "***" count as kind "secret".`

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config ./internal/hub ./internal/mcpserver && go test ./cmd/sshgate -run TestEndToEnd`
Expected: PASS. `TestRedactCorpus` passes unchanged (it uses `RedactPatterns` only). If another test now fails on a `redacted` map that gained `secret`, update its expectation: the new count is the intended behaviour.

- [ ] **Step 5: Commit**

```bash
git add internal/config/redact.go internal/config/patterns.go internal/config/redact_test.go internal/config/redactcap_test.go internal/hub/redact_test.go
git commit -m "feat(redact): count vault-secret masks as kind secret

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: `sshx.Manager.ExecStdin` and the `stdin-echo` test server command

**Files:**
- Modify: `internal/sshx/manager.go:208-218`
- Modify: `internal/sshx/sshtest/sshtest.go:1-6` (doc), `:200-215` (exec case)
- Create: `internal/sshx/stdin_test.go`
- Modify: `internal/sshx/manager_test.go` (one Docker test)

**Interfaces:**
- Produces: `func (m *Manager) ExecStdin(ctx context.Context, cmd, stdin string) (ExecResult, error)`, `sshx.ErrStdinUnsupported`. The sshtest server answers an exec of exactly `stdin-echo` by writing back its stdin. Used by Tasks 5, 7, 8 and 10.

- [ ] **Step 1: Write the failing tests** — create `internal/sshx/stdin_test.go`:

```go
package sshx

import (
	"context"
	"errors"
	"testing"

	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

func TestExecStdinPipesInput(t *testing.T) {
	srv := sshtest.Start(t)
	close(srv.Release)
	m := fakeManager(t, srv)
	res, err := m.ExecStdin(context.Background(), "stdin-echo", "line1\nline2\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "line1\nline2\r\n" || res.ExitCode != 0 {
		t.Fatalf("got %+v", res)
	}
}

// The su shell frames commands over its own stdin; stdin cannot share it.
func TestExecStdinRefusedWithSuPassword(t *testing.T) {
	m := NewManager(DialConfig{Host: "127.0.0.1", Port: 1, User: "u", Password: "p", Auth: "password", SuPassword: "su", Insecure: true, TimeoutMs: 1000})
	defer m.Close()
	if _, err := m.ExecStdin(context.Background(), "cat", "x"); !errors.Is(err, ErrStdinUnsupported) {
		t.Fatalf("got %v", err)
	}
}
```

Append to `internal/sshx/manager_test.go`:

```go
func TestExecStdinAgainstOpenSSH(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSH(t)
	defer cleanup()
	m := NewManager(DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", Insecure: true, TimeoutMs: 30000})
	defer m.Close()
	res, err := m.ExecStdin(context.Background(), "cat > /tmp/stdin.txt && cat /tmp/stdin.txt", "one\ntwo\n")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "one\ntwo\n" || res.ExitCode != 0 {
		t.Fatalf("got %+v", res)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -short ./internal/sshx -run TestExecStdin`
Expected: FAIL, `m.ExecStdin undefined`.

- [ ] **Step 3: Write minimal implementation**

`internal/sshx/manager.go`, after `Exec`:

```go
// ErrStdinUnsupported: with a su password, plain exec runs inside a su
// shell that frames each command over its own stdin.
var ErrStdinUnsupported = errors.New("stdin is not supported on a server with a su password")

// ExecStdin runs cmd with stdin piped to it, in a fresh session like Exec.
func (m *Manager) ExecStdin(ctx context.Context, cmd, stdin string) (ExecResult, error) {
	if m.cfg.SuPassword != "" {
		return ExecResult{}, ErrStdinUnsupported
	}
	sess, err := m.OpenSession()
	if err != nil {
		return ExecResult{}, err
	}
	return m.runOnce(ctx, sess, cmd, stdin)
}
```

(`errors` is already imported: `manager.go` uses `errors.Is`.)

`internal/sshx/sshtest/sshtest.go`, in the `"exec"` case, replace `io.WriteString(ch, p.Cmd)` with:

```go
				if p.Cmd == "stdin-echo" {
					b, _ := io.ReadAll(ch) // until the client closes stdin
					ch.Write(b)
				} else {
					io.WriteString(ch, p.Cmd)
				}
```

Change the package doc sentence to: `An "exec" reports its command on Execs, waits for Release to be closed, writes the command to stdout (or, for the command "stdin-echo", its stdin), and exits 0.` Keep the rest of the comment as it is.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sshx/...` (the Docker test skips without Docker)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sshx/manager.go internal/sshx/sshtest/sshtest.go internal/sshx/stdin_test.go internal/sshx/manager_test.go
git commit -m "feat(sshx): ExecStdin pipes stdin to a command

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Broker carries stdin and refuses Send to tab for it

**Files:**
- Modify: `internal/broker/broker.go:22-32` (Request), `:58-61` (errors), `:160-169` (Decide)
- Modify: `internal/broker/audit.go:16-33` (AuditRecord)
- Test: `internal/broker/broker_test.go`

**Interfaces:**
- Produces: `broker.Request.Stdin string` (JSON `stdin,omitempty`), `broker.AuditRecord.Stdin string` (JSON `stdin,omitempty`, placed after `Description`), `broker.ErrSendToTabStdin`. `Decide(id, Decision{Outcome: SentToTab})` returns it, and leaves the request pending, when the request has stdin.

- [ ] **Step 1: Write the failing test** — append to `internal/broker/broker_test.go`:

```go
// Pasting only the command into a terminal would run a different command
// than the one approved: Send to tab is refused, and the request stays.
func TestSendToTabRefusedWithStdin(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	done := make(chan Decision, 1)
	go func() {
		d, _ := b.Submit(context.Background(), Request{Server: "s", Command: "cat > f", Stdin: "a\n"})
		done <- d
	}()
	r := waitForPending(t, b, 1)[0]
	if r.Stdin != "a\n" {
		t.Fatalf("pending request lost stdin: %+v", r)
	}
	if err := b.Decide(r.ID, Decision{Outcome: SentToTab}); !errors.Is(err, ErrSendToTabStdin) {
		t.Fatalf("got %v", err)
	}
	if len(b.Pending()) != 1 {
		t.Fatal("refused send-to-tab removed the request")
	}
	if err := b.Decide(r.ID, Decision{Outcome: Denied}); err != nil {
		t.Fatal(err)
	}
	if d := <-done; d.Outcome != Denied {
		t.Fatalf("outcome %v", d.Outcome)
	}
	b2, _ := json.Marshal(Request{Command: "ls"})
	if strings.Contains(string(b2), "stdin") {
		t.Fatalf("empty stdin not omitted: %s", b2)
	}
}
```

Append to `internal/broker/audit_test.go` (a stdin record is the first audit line that routinely spans several of `linesBackward`'s 64 KiB chunks). Copy the open/close lines from the test around `audit_test.go:180-187` and use its temp-path pattern:

```go
func TestAuditReadsRecordLongerThanAChunk(t *testing.T) {
	a, err := OpenAudit(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	stdin := strings.Repeat("0123456789abcdef\n", 200<<10/17)
	for _, c := range []string{"before", "long", "after"} {
		r := AuditRecord{Command: c, Outcome: "allowed"}
		if c == "long" {
			r.Stdin = stdin
		}
		if err := a.Write(r); err != nil {
			t.Fatal(err)
		}
	}
	res, err := a.Read(ReadQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 0 || len(res.Records) != 3 {
		t.Fatalf("skipped %d, records %d", res.Skipped, len(res.Records))
	}
	var got AuditRecord
	if err := json.Unmarshal(res.Records[1].Record, &got); err != nil || got.Command != "long" || got.Stdin != stdin {
		t.Fatalf("long record: err %v, command %q, stdin %d bytes", err, got.Command, len(got.Stdin))
	}
}
```

Add `"path/filepath"` to that file's imports if it is missing.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/broker -run 'TestSendToTabRefusedWithStdin|TestAuditReadsRecordLongerThanAChunk'`
Expected: FAIL, `unknown field Stdin` / `undefined: ErrSendToTabStdin`.

- [ ] **Step 3: Write minimal implementation**

`internal/broker/broker.go`: add to `Request` after `Description`:

```go
	Stdin       string    `json:"stdin,omitempty"`
```

Add to the error `var` block:

```go
	ErrSendToTabStdin = errors.New("send to tab is not available for a command with stdin")
```

At the top of `Decide`, before `p := b.remove(id)`:

```go
	if d.Outcome == SentToTab {
		b.mu.Lock()
		for _, p := range b.q {
			if p.req.ID == id && p.req.Stdin != "" {
				b.mu.Unlock()
				return ErrSendToTabStdin
			}
		}
		b.mu.Unlock()
	}
```

`internal/broker/audit.go`: add to `AuditRecord` after `Description`:

```go
	Stdin       string         `json:"stdin,omitempty"` // already redacted by caller
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/broker`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/broker/broker.go internal/broker/audit.go internal/broker/broker_test.go internal/broker/audit_test.go
git commit -m "feat(broker): requests and audit records carry stdin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Hub runs stdin execs; MCP door forwards stdin; protocol 11

**Files:**
- Modify: `internal/hub/hub.go:67-70` (Executor), `:89-93` (ExecRequest), `:582-690` (Exec), `:693-704` (run)
- Modify: `internal/hub/autoallow.go:485-500` (autoExec)
- Modify: `internal/hub/mcpdoor.go:16-23`, `:103`
- Modify: `internal/hub/idle.go:5`
- Modify test fakes: `internal/hub/hub_test.go:22-35`, `internal/hub/autoallow_test.go:43`, `internal/hub/idle_test.go:144`
- Modify: `internal/hub/uidoor_test.go:208-209`
- Create: `internal/hub/stdin_test.go`

**Interfaces:**
- Consumes: `config.ValidateStdin`, `config.ErrStdinSudo`, `config.ErrStdinSu` (Task 1); `broker.Request.Stdin`, `broker.AuditRecord.Stdin` (Task 4); `sshx.Manager.ExecStdin` (Task 3; `*sshx.Manager` satisfies the widened `Executor`).
- Produces: `ExecRequest.Stdin string`; `Executor.ExecStdin(ctx context.Context, cmd, stdin string) (sshx.ExecResult, error)`; MCP-door `exec` param `stdin`; `autoAllow.ran` param `stdinBytes` (int, present only when stdin is non-empty); `ProtocolVersion = 11`.

- [ ] **Step 1: Widen the test fakes** so the package compiles once `Executor` grows.

`internal/hub/hub_test.go`, `fakeExec`: add a field `stdin string` and the method:

```go
func (f *fakeExec) ExecStdin(ctx context.Context, cmd, stdin string) (sshx.ExecResult, error) {
	f.calls = append(f.calls, "stdin:"+cmd)
	f.stdin = stdin
	return f.res, f.err
}
```

`internal/hub/autoallow_test.go` (`blockExec`) and `internal/hub/idle_test.go` (`blockingExec`): add a method that delegates to their own `Exec`. Read each type first and use its receiver name:

```go
func (b *blockExec) ExecStdin(ctx context.Context, cmd, _ string) (sshx.ExecResult, error) {
	return b.Exec(ctx, cmd)
}
```

```go
func (b *blockingExec) ExecStdin(ctx context.Context, cmd, _ string) (sshx.ExecResult, error) {
	return b.Exec(ctx, cmd)
}
```

- [ ] **Step 2: Write the failing tests** — create `internal/hub/stdin_test.go`:

```go
package hub

import (
	"context"
	"errors"
	"testing"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
)

func TestExecStdinApprovedPath(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	go func() {
		if !waitFor(t, "a pending request", func() bool { return len(h.Broker().Pending()) > 0 }) {
			return
		}
		r := h.Broker().Pending()[0]
		if r.Stdin != "a\nb\n" {
			t.Errorf("approver saw stdin %q", r.Stdin)
		}
		h.Broker().Decide(r.ID, broker.Decision{Outcome: broker.Allowed})
	}()
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "cat > f", Stdin: "a\nb\n"}); err != nil {
		t.Fatal(err)
	}
	if len(fe.calls) != 1 || fe.calls[0] != "stdin:cat > f" || fe.stdin != "a\nb\n" {
		t.Fatalf("calls = %v stdin = %q", fe.calls, fe.stdin)
	}
	_, recs := readAudit(t, path)
	if last := recs[len(recs)-1]; last["stdin"] != "a\nb\n" || last["outcome"] != "allowed" {
		t.Fatalf("audit: %v", last)
	}
}

func TestExecStdinAutoPath(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	type call struct {
		method string
		params map[string]any
	}
	calls := make(chan call, 1)
	release := h.setAutoSink(func(method string, params any) { calls <- call{method, params.(map[string]any)} })
	defer release()
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "cat > f", Stdin: "abcd"}); err != nil {
		t.Fatal(err)
	}
	if len(h.Broker().Pending()) != 0 || len(fe.calls) != 1 || fe.calls[0] != "stdin:cat > f" {
		t.Fatalf("pending %d calls %v", len(h.Broker().Pending()), fe.calls)
	}
	_, recs := readAudit(t, path)
	if last := findAutoRecord(t, recs); last["approval"] != "auto" || last["stdin"] != "abcd" {
		t.Fatalf("audit: %v", last)
	}
	c := <-calls
	if c.method != "autoAllow.ran" || c.params["stdinBytes"] != 4 {
		t.Fatalf("ran = %+v", c)
	}
	if _, ok := c.params["stdin"]; ok {
		t.Fatal("autoAllow.ran carries stdin content")
	}
}

// Refused before the broker and the auto path: no pending entry, no run,
// no audit record (like the other validation errors).
func TestExecStdinRefusals(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	addServer(t, h, path, encServer(t, "suPw", "encSuPassword", "su-pw"))
	raw0, _ := readAudit(t, path)
	cases := []struct {
		name string
		r    ExecRequest
		want error
	}{
		{"sudo", ExecRequest{Server: "vis", Command: "cat > f", Stdin: "x", Sudo: true}, config.ErrStdinSudo},
		{"su host", ExecRequest{Server: "suPw", Command: "cat > f", Stdin: "x"}, config.ErrStdinSu},
	}
	for _, c := range cases {
		if _, err := h.Exec(context.Background(), c.r); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "cat", Stdin: "a\x1bb"}); err == nil {
		t.Error("ESC in stdin accepted")
	}
	if len(h.Broker().Pending()) != 0 || len(fe.calls) != 0 {
		t.Fatalf("refused stdin reached the broker or ran: %v", fe.calls)
	}
	if raw, _ := readAudit(t, path); raw != raw0 {
		t.Fatalf("refusals were audited:\n%s", raw[len(raw0):])
	}
}

func TestExecStdinVaultSecretMaskedInAudit(t *testing.T) {
	fe := &fakeExec{}
	h, path, _ := newEncHub(t, fe)
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	go allowFirst(t, h.Broker())
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "enc", Command: "cat > .env", Stdin: "PASS=s3cr3t-pw\n"}); err != nil {
		t.Fatal(err)
	}
	if fe.stdin != "PASS=s3cr3t-pw\n" {
		t.Fatalf("command got %q", fe.stdin)
	}
	_, recs := readAudit(t, path)
	if got := recs[len(recs)-1]["stdin"]; got != "PASS=***\n" {
		t.Fatalf("audit stdin = %v", got)
	}
}
```

`encServer` (`autoallow_test.go:273`) builds an AI-visible, pinned server whose su password decrypts with `testMK`, so `dc.SuPassword` is set once it resolves.

Also in `stdin_test.go`, the UI door relays the Send-to-tab refusal text (a plain Go error becomes `-32000` with its message, `internal/rpc/rpc.go:196-198`). Use the UI-door helpers the file already uses elsewhere (`startUIRaw` in `uidoor_test.go:201`; read it for the exact return values):

```go
func TestUIDoorRefusesSendToTabWithStdin(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _ := startUIRaw(t, h)
	go h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "cat > f", Stdin: "x"})
	waitPending(t, h.Broker(), 1)
	id := h.Broker().Pending()[0].ID
	err := c.Call(context.Background(), "decide", map[string]string{"id": id, "outcome": "sent_to_tab"}, nil)
	if err == nil || !strings.Contains(err.Error(), "send to tab is not available for a command with stdin") {
		t.Fatalf("got %v", err)
	}
	h.Broker().Decide(id, broker.Decision{Outcome: broker.Denied})
}
```

(add `"strings"` to the file's imports).

Change `internal/hub/uidoor_test.go:208-209` to:

```go
	if hello.Protocol != 11 {
		t.Fatalf("protocol = %d, want 11", hello.Protocol)
	}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/hub -run 'TestExecStdin|TestUIDoor'`
Expected: compile errors first (`unknown field Stdin in struct literal of type ExecRequest`), then the protocol test fails with 10.

- [ ] **Step 4: Write minimal implementation**

`internal/hub/hub.go`:

1. `Executor` gains:

```go
	ExecStdin(ctx context.Context, cmd, stdin string) (sshx.ExecResult, error)
```

2. `ExecRequest`:

```go
type ExecRequest struct {
	Client, Server, Command, Description, Stdin string
	Sudo                                        bool
	TimeoutSec                                  int
}
```

3. In `Exec`, no-vault record: add `Stdin: c(r.Stdin, 4096),` after `Description: c(r.Description, 500),`.

4. In `Exec`, right after the `ValidateDescription` check:

```go
	if err := config.ValidateStdin(r.Stdin); err != nil {
		return ExecResponse{}, err
	}
	if r.Stdin != "" {
		if r.Sudo {
			return ExecResponse{}, config.ErrStdinSudo
		}
		if dc.SuPassword != "" {
			return ExecResponse{}, config.ErrStdinSu
		}
	}
```

5. `req := broker.Request{...}` gains `Stdin: r.Stdin,`. `base := broker.AuditRecord{...}` gains `Stdin: red.Redact(r.Stdin),`.

6. In the approved path, the line `base.Command, base.Description = red.Redact(cmd), red.Redact(r.Description)` becomes:

```go
	base.Command, base.Description, base.Stdin = red.Redact(cmd), red.Redact(r.Description), red.Redact(r.Stdin)
	return h.run(ctx, r.Server, dc2, cmd, r.Stdin, r.Sudo, timeout, base, red)
```

7. `run` takes stdin after cmd and uses it:

```go
func (h *Hub) run(ctx context.Context, name string, dc sshx.DialConfig, cmd, stdin string, sudo bool, timeout int, base broker.AuditRecord, red *config.Redactor) (ExecResponse, error) {
```

with the call block:

```go
	switch {
	case stdin != "":
		res, err = ex.ExecStdin(runCtx, cmd, stdin)
	case sudo:
		res, err = ex.ExecSudo(runCtx, cmd)
	default:
		res, err = ex.Exec(runCtx, cmd)
	}
```

`internal/hub/autoallow.go`, `autoExec`:

```go
	base.Command, base.Description, base.Stdin = red.Redact(cmd), red.Redact(r.Description), red.Redact(r.Stdin)
	resp, err := h.run(ar.ctx, r.Server, ar.dc, cmd, r.Stdin, r.Sudo, timeout, base, red)
```

and after the `if r.Sudo { ran["sudo"] = true }` block:

```go
	if r.Stdin != "" {
		ran["stdinBytes"] = len(r.Stdin)
	}
```

`internal/hub/mcpdoor.go`: `execParams` gains `Stdin string `json:"stdin,omitempty"``, and the `h.Exec` call passes `Stdin: p.Stdin`.

`internal/hub/idle.go`: `const ProtocolVersion = 11`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -race ./internal/hub`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/hub
git commit -m "feat(hub): exec runs with stdin; UI-door protocol 11

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: CLI approver shows stdin and refuses `s` for it

**Files:**
- Modify: `internal/hub/cliui.go:62-67` (`s` command), `:212-222` (printRequest)
- Test: `internal/hub/cliui_test.go`

**Interfaces:**
- Consumes: `broker.Request.Stdin`, `broker.ErrSendToTabStdin` (Task 4); `ExecRequest.Stdin` (Task 5).

- [ ] **Step 1: Write the failing test** — append to `internal/hub/cliui_test.go`:

```go
func TestCLIApproverShowsStdinAndRefusesSendToTab(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	pr, pw := io.Pipe()
	defer pw.Close()
	out := &safeBuf{}
	go RunCLIApprover(context.Background(), h, pr, out)
	waitOut(t, out, "Commands:")

	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "cat > f", Stdin: "a\r\nb\n"})
		errc <- err
	}()
	waitPending(t, h.Broker(), 1)
	waitOut(t, out, "stdin (5 bytes):\n| a␍\n| b\n")

	io.WriteString(pw, "s\n")
	waitOut(t, out, "send to tab is not available for a command with stdin")
	if strings.Contains(out.String(), "paste into your terminal") {
		t.Fatal("printed a paste hint for a stdin command")
	}
	if len(h.Broker().Pending()) != 1 {
		t.Fatal("s removed the request")
	}
	io.WriteString(pw, "d\n")
	if err := <-errc; err == nil {
		t.Fatal("want denied")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hub -run TestCLIApproverShowsStdin`
Expected: FAIL, timed out waiting for `stdin (5 bytes):`.

- [ ] **Step 3: Write minimal implementation** — `internal/hub/cliui.go`:

`case "s":` becomes:

```go
		case "s":
			if r, _, ok := resolveTarget(out, pend, rest, false); ok {
				if r.Stdin != "" {
					fmt.Fprintln(out, broker.ErrSendToTabStdin)
					break
				}
				fmt.Fprintf(out, "paste into your terminal (not run here):\n  %s\n", r.Command)
				h.broker.Decide(r.ID, broker.Decision{Outcome: broker.SentToTab})
			}
```

In `printRequest`, after the `command:` line:

```go
	if r.Stdin != "" {
		fmt.Fprintf(out, "stdin (%d bytes):\n", len(r.Stdin))
		for _, line := range strings.Split(strings.TrimSuffix(r.Stdin, "\n"), "\n") {
			fmt.Fprintf(out, "| %s\n", strings.ReplaceAll(line, "\r", "␍"))
		}
	}
```

`cliui.go` already imports `strings` (it uses `strings.Cut`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/hub -run TestCLIApprover`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/hub/cliui.go internal/hub/cliui_test.go
git commit -m "feat(cli): approver shows exec stdin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: MCP tools: `stdin` on `exec` in the bridge and in standalone mode

**Files:**
- Modify: `internal/mcpserver/bridge.go:24-29` (inputs), `:77-84` (params), `:147-155` (tool registration and descriptions)
- Modify: `internal/mcpserver/tools.go:13-17` (inputs), `:27-51` (runExec), `:118-137` (registration and descriptions)
- Test: `internal/mcpserver/bridge_test.go`, `internal/mcpserver/tools_test.go`

**Interfaces:**
- Consumes: `config.ValidateStdin`, `config.ErrStdinSudo`, `config.ErrStdinSu` (Task 1); `sshx.Manager.ExecStdin` (Task 3); MCP-door `exec` param `stdin` (Task 5).
- Produces: `exec` input field `stdin` (both modes). `sudo-exec` inputs are `bridgeSudoInput` / `SudoInput`, with no `stdin`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpserver/bridge_test.go`:

```go
func TestBridgeExecForwardsStdin(t *testing.T) {
	got := make(chan map[string]any, 1)
	dial := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		s := rpc.NewServer()
		s.HandleRequest("exec", func(_ context.Context, raw json.RawMessage) (any, error) {
			var p map[string]any
			json.Unmarshal(raw, &p)
			got <- p
			return map[string]any{"exitCode": 0, "stdout": "", "stderr": ""}, nil
		})
		go s.Serve(context.Background(), server, server)
		return client, nil
	}
	res := callTool(t, BuildBridgeServer(dial), "exec", map[string]any{"server": "vis", "command": "cat > f", "stdin": "a\nb\n"})
	if res.IsError {
		t.Fatalf("got %q", text(res))
	}
	if p := <-got; p["stdin"] != "a\nb\n" || p["command"] != "cat > f" {
		t.Fatalf("door params %v", p)
	}
}

// inputSchemas maps each tool name to its input schema as JSON.
func inputSchemas(t *testing.T, srv *mcp.Server) map[string]string {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	go srv.Run(context.Background(), st)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	lt, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, tool := range lt.Tools {
		b, _ := json.Marshal(tool.InputSchema)
		out[tool.Name] = string(b)
	}
	return out
}

func TestBridgeStdinOnlyOnExec(t *testing.T) {
	s := inputSchemas(t, BuildBridgeServer(fakeHub(t, nil, "")))
	if !strings.Contains(s["exec"], `"stdin"`) || strings.Contains(s["sudo-exec"], `"stdin"`) {
		t.Fatalf("exec: %s\nsudo-exec: %s", s["exec"], s["sudo-exec"])
	}
}
```

Append to `internal/mcpserver/tools_test.go` (add imports `"github.com/lang315/sshgate/internal/sshx/sshtest"`, `"errors"` if missing):

```go
func TestRunExecStdin(t *testing.T) {
	srv := sshtest.Start(t)
	close(srv.Release)
	d := &Deps{CLI: &config.CLIConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", HasHost: true, TimeoutMs: 30000, MaxChars: 1000}, Insecure: true}
	reg := sshx.NewRegistry()
	defer reg.CloseAll()
	res, _ := runExec(context.Background(), d, reg, 1000, false, ExecInput{Command: "stdin-echo", Stdin: "one\ntwo\n"})
	if res.IsError || !strings.Contains(text(res), "stdout:\none\ntwo\n") {
		t.Fatalf("got %q", text(res))
	}
	res, _ = runExec(context.Background(), d, reg, 1000, true, ExecInput{Command: "cat", Stdin: "x"})
	if !res.IsError || text(res) != config.ErrStdinSudo.Error() {
		t.Fatalf("sudo: %q", text(res))
	}
	su := &Deps{CLI: &config.CLIConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", SuPassword: "su", HasHost: true, HasSuPassword: true, TimeoutMs: 30000, MaxChars: 1000}, Insecure: true}
	res, _ = runExec(context.Background(), su, reg, 1000, false, ExecInput{Command: "cat", Stdin: "x"})
	if !res.IsError || text(res) != config.ErrStdinSu.Error() {
		t.Fatalf("su: %q", text(res))
	}
}

func TestStandaloneStdinOnlyOnExec(t *testing.T) {
	d := &Deps{CLI: &config.CLIConfig{Host: "h", User: "u", HasHost: true, TimeoutMs: 60000, MaxChars: 1000}}
	s := inputSchemas(t, BuildServer(d, sshx.NewRegistry(), false, 1000))
	if !strings.Contains(s["exec"], `"stdin"`) || strings.Contains(s["sudo-exec"], `"stdin"`) {
		t.Fatalf("exec: %s\nsudo-exec: %s", s["exec"], s["sudo-exec"])
	}
}
```

`Registry.CloseAll` exists (`internal/sshx/registry.go:58`), and `Deps.Resolve` copies `CLIConfig.SuPassword` into the `DialConfig` (`internal/mcpserver/resolve.go:67`). If the CLI branch needs an explicit auth for a password login, read `resolve.go:55-75` and set the field it reads.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpserver -run 'Stdin'`
Expected: FAIL, `unknown field Stdin in struct literal of type ExecInput`.

- [ ] **Step 3: Write minimal implementation**

`internal/mcpserver/bridge.go`:

```go
type bridgeExecInput struct {
	Server      string `json:"server" jsonschema:"connection name from list-servers"`
	Command     string `json:"command" jsonschema:"shell command to execute; requires human approval in the app"`
	Description string `json:"description,omitempty" jsonschema:"one sentence on what the command does and why; shown to the approver next to the command (marked unverified) and recorded in the audit log, never executed; at most 500 bytes, no control characters"`
	TimeoutSec  int    `json:"timeoutSec,omitempty" jsonschema:"execution timeout in seconds, 1-600, default 60"`
	Stdin       string `json:"stdin,omitempty" jsonschema:"text piped to the command's standard input, shown in full to the approver and recorded in the audit log; use it to write files (cat > path) or apply a unified diff (git apply); at most 256 KiB; newlines, tabs and carriage returns allowed, other control characters rejected"`
}

// bridgeSudoInput is sudo-exec's input: no stdin, since sudo -S reads the
// password from stdin.
type bridgeSudoInput struct {
	Server      string `json:"server" jsonschema:"connection name from list-servers"`
	Command     string `json:"command" jsonschema:"shell command to execute; requires human approval in the app"`
	Description string `json:"description,omitempty" jsonschema:"one sentence on what the command does and why; shown to the approver next to the command (marked unverified) and recorded in the audit log, never executed; at most 500 bytes, no control characters"`
	TimeoutSec  int    `json:"timeoutSec,omitempty" jsonschema:"execution timeout in seconds, 1-600, default 60"`
}
```

In `execTool`'s params map add `"stdin": in.Stdin,`.

Registration: append to the `exec` description, after its "Saved secrets are masked…" sentence:

```go
"To write a file, pass its content as `stdin` with `cat > path`; to edit one, pass a unified diff as `stdin` with `git apply` (or `patch -p1`). Never write back output that contained masked values (`***` or `[REDACTED:…]`): the file would be corrupted. `stdin` is not available on servers configured with a su password. "
```

Register `sudo-exec` through an adapter:

```go
	sudoExec := execTool("sudoExec")
	mcp.AddTool(s, &mcp.Tool{Name: "sudo-exec", Description: /* unchanged text */},
		func(ctx context.Context, req *mcp.CallToolRequest, in bridgeSudoInput) (*mcp.CallToolResult, any, error) {
			return sudoExec(ctx, req, bridgeExecInput{Server: in.Server, Command: in.Command, Description: in.Description, TimeoutSec: in.TimeoutSec})
		})
```

`internal/mcpserver/tools.go`:

```go
type ExecInput struct {
	Server      string `json:"server" jsonschema:"leave empty: this server exposes only the connection given on its command line"`
	Command     string `json:"command" jsonschema:"shell command to execute"`
	Description string `json:"description,omitempty" jsonschema:"optional one-line note on what the command does; appended to the command as a shell comment (# ...), at most 500 bytes, no control characters"`
	Stdin       string `json:"stdin,omitempty" jsonschema:"text piped to the command's standard input; use it to write files (cat > path) or apply a unified diff (git apply); at most 256 KiB; newlines, tabs and carriage returns allowed, other control characters rejected"`
}

// SudoInput is sudo-exec's input: no stdin, since sudo -S reads the
// password from stdin.
type SudoInput struct {
	Server      string `json:"server" jsonschema:"leave empty: this server exposes only the connection given on its command line"`
	Command     string `json:"command" jsonschema:"shell command to execute"`
	Description string `json:"description,omitempty" jsonschema:"optional one-line note on what the command does; appended to the command as a shell comment (# ...), at most 500 bytes, no control characters"`
}
```

`runExec`, after `AppendDescription`:

```go
	if err := config.ValidateStdin(in.Stdin); err != nil {
		return textErr(err.Error()), nil
	}
	if in.Stdin != "" && sudo {
		return textErr(config.ErrStdinSudo.Error()), nil
	}
```

after `d.Resolve`:

```go
	if in.Stdin != "" && dc.SuPassword != "" {
		return textErr(config.ErrStdinSu.Error()), nil
	}
```

and the run block becomes:

```go
	switch {
	case in.Stdin != "":
		res, err = mgr.ExecStdin(ctx, cmd, in.Stdin)
	case sudo:
		res, err = mgr.ExecSudo(ctx, cmd)
	default:
		res, err = mgr.Exec(ctx, cmd)
	}
```

`BuildServer`: append to the `exec` description the same stdin sentence as the bridge's. Register `sudo-exec` with `in SudoInput` and call `runExec(ctx, d, reg, maxChars, true, ExecInput{Server: in.Server, Command: in.Command, Description: in.Description})`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/mcpserver`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver
git commit -m "feat(mcp): exec takes stdin; sudo-exec does not

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: End to end through the real bridge binary

**Files:**
- Modify: `cmd/sshgate/e2e_test.go` (inside `TestEndToEndAutoAllow`, after the pattern-redaction call near line 335)

**Interfaces:**
- Consumes: everything from Tasks 3, 5 and 7.

- [ ] **Step 1: Write the test** — append inside `TestEndToEndAutoAllow`, before its closing brace:

```go
	// stdin end to end: the in-process sshd echoes it back for "stdin-echo".
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "exec", Arguments: map[string]any{"server": "box", "command": "stdin-echo", "stdin": "line1\nline2\n"}})
	if err != nil || res.IsError {
		t.Fatalf("stdin exec: %v %+v", err, res)
	}
	if txt = res.Content[0].(*mcp.TextContent).Text; !strings.Contains(txt, "stdout:\nline1\nline2\n") {
		t.Fatalf("stdin output: %q", txt)
	}
	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["command"] != "stdin-echo" || rec["stdin"] != "line1\nline2\n" || rec["approval"] != "auto" {
		t.Fatalf("audit: %v", rec)
	}
```

Add `"encoding/json"` to the imports if missing.

- [ ] **Step 2: Run it**

Run: `go test -race ./cmd/sshgate -run TestEndToEndAutoAllow -v`
Expected: PASS (Tasks 3–7 are in). If it fails, the failure points at a missing link in the chain; fix that link, not the test.

- [ ] **Step 3: Run the whole Go suite**

Run: `go vet ./... && go test -race ./...`
Expected: exit 0 (Docker tests skip without Docker).

- [ ] **Step 4: Commit**

```bash
git add cmd/sshgate/e2e_test.go
git commit -m "test(e2e): stdin exec through the bridge under a grant

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Desktop: show stdin to the approver, in the feed and in the audit row

**Files:**
- Modify: `desktop/src/shared/protocol.ts:9-12` (ApprovalRequest), `:25-28` (AutoAllowRan), `:99-109` (AuditRecord), `:141` (PROTOCOL_VERSION)
- Modify: `desktop/src/renderer/approvals.ts:83-109`
- Modify: `desktop/src/renderer/ApprovalPanel.tsx` (Item and AutoFeed)
- Modify: `desktop/src/renderer/App.tsx:285-288`
- Modify: `desktop/src/renderer/AuditTable.tsx:70`
- Modify: `desktop/src/renderer/styles.css` (after line 171)
- Test: `desktop/test/ApprovalPanel.test.ts`, `desktop/test/approvals.test.ts`

**Interfaces:**
- Consumes: `pending.request.stdin`, `autoAllow.ran.stdinBytes`, audit `stdin`, protocol 11 (Task 5).
- Produces: `highlightNonAscii(s, plain = '')`, `nonAsciiSummary(s, plain = '')`, `stdinView(s): Segment[]`, `stdinMeta(s): string` in `approvals.ts`.

- [ ] **Step 1: Write the failing tests**

Append to `desktop/test/approvals.test.ts` (extend its import from `'../src/renderer/approvals'` with `stdinMeta, stdinView`):

```ts
describe('stdin display', () => {
  it('counts bytes and lines, and shows CR as ␍ while newlines and tabs stay plain', () => {
    expect(stdinMeta('a\r\nb\n')).toBe('5 bytes, 2 lines')
    expect(stdinMeta('é')).toBe('2 bytes, 1 line')
    expect(stdinView('a\tb\r\n')).toEqual([{ text: 'a\tb', nonAscii: false }, { text: '␍', nonAscii: true }, { text: '\n', nonAscii: false }])
  })
})
```

Append inside `describe('Item', …)` in `desktop/test/ApprovalPanel.test.ts`:

```ts
  it('shows stdin with its size, marks CR, and disables Send to tab but not Allow', () => {
    const [item] = seed([{ ...req, command: 'cat > f', stdin: 'a\r\nb\n' }], 0)
    const html = renderToStaticMarkup(createElement(Item, { item, now: 10_000, onDecide: async () => {}, onSendToTab: async () => {}, onEscape: () => {} }))
    expect(html).toContain('stdin · 5 bytes, 2 lines')
    expect(html).toContain('<mark>␍</mark>')
    const buttons = [...html.matchAll(/<button([^>]*)>(.*?)<\/button>/g)].map((m) => ({ attrs: m[1], text: m[2].replace(/<[^>]+>/g, '') }))
    expect(buttons.find((b) => b.text === 'Send to tab')!.attrs).toMatch(/disabled=""/)
    expect(buttons.find((b) => b.text === 'Allow')!.attrs).not.toMatch(/disabled=""/)
  })
```

And inside `describe('ApprovalPanel', …)`:

```ts
  it('tags an auto-allowed run that had stdin with its size', () => {
    const html = renderToStaticMarkup(createElement(ApprovalPanel, {
      items: [], seedError: undefined,
      onDecide: async () => {}, onDenyAll: async () => {}, onSendToTab: async () => {},
      onClose: () => {}, onEscape: () => {},
      autoFeed: [{ server: 'box', command: 'cat > f', description: '', time: '2024-01-01T00:00:00Z', exitCode: 0, stdinBytes: 12 }],
      autoN: 1, paused: [], onStopAll: () => {}, onStopPaused: () => {}, onResume: () => {},
    }))
    expect(html).toContain('stdin 12 B')
  })
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd desktop && npx vitest run test/approvals.test.ts test/ApprovalPanel.test.ts`
Expected: FAIL, `stdinMeta is not exported` / missing `stdin ·` text.

- [ ] **Step 3: Write minimal implementation**

`desktop/src/shared/protocol.ts`:
- `ApprovalRequest`: add `stdin?: string` to the second line.
- `AutoAllowRan`: add `stdinBytes?: number`.
- `AuditRecord`, exec line: add `stdin?: string`.
- `export const PROTOCOL_VERSION = 11`.

`desktop/src/renderer/approvals.ts`, replace `highlightNonAscii` and `nonAsciiSummary`'s first lines so both take the characters to leave unmarked:

```ts
export function highlightNonAscii(s: string, plain = ''): Segment[] {
  const out: Segment[] = []
  for (const ch of s) {
    const nonAscii = isNonAscii(ch.codePointAt(0)!) && !plain.includes(ch)
```

```ts
export function nonAsciiSummary(s: string, plain = ''): string | undefined {
  const cps: number[] = []
  let n = 0
  for (const ch of s) {
    const cp = ch.codePointAt(0)!
    if (!isNonAscii(cp) || plain.includes(ch)) continue
```

(the rest of both bodies unchanged), and append:

```ts
// Stdin is file content: newlines and tabs are plain, a carriage return is
// shown as ␍ so it cannot hide text behind it.
export const stdinView = (s: string): Segment[] => highlightNonAscii(s.replaceAll('\r', '␍'), '\n\t')

export function stdinMeta(s: string): string {
  const bytes = new TextEncoder().encode(s).length
  const lines = s === '' ? 0 : s.split('\n').length - (s.endsWith('\n') ? 1 : 0)
  return `${bytes} byte${bytes === 1 ? '' : 's'}, ${lines} line${lines === 1 ? '' : 's'}`
}
```

`desktop/src/renderer/ApprovalPanel.tsx`: import `stdinMeta, stdinView` from `./approvals`. In `Item`, after `const summary = …`:

```tsx
  const stdinSummary = r.stdin ? nonAsciiSummary(r.stdin, '\n\t') : undefined
```

after `{summary && …}`:

```tsx
      {r.stdin && (
        <div className="stdin">
          <span className="stdin-label">{`stdin · ${stdinMeta(r.stdin)}`}</span>
          <pre className="cmd">{stdinView(r.stdin).map((s, i) => (s.nonAscii ? <mark key={i}>{s.text}</mark> : <span key={i}>{s.text}</span>))}</pre>
          {stdinSummary && <p className="nonascii"><WarningIcon />{stdinSummary}</p>}
        </div>
      )}
```

Send to tab button: `disabled={busy || !!r.stdin || !allowEnabled(item, now, changedAt)}` and add `title={r.stdin ? 'Not available: the command has stdin' : undefined}`.

In `AutoFeed`, after the sudo chip:

```tsx
              {r.stdinBytes ? <span className="chip">{`stdin ${r.stdinBytes} B`}</span> : null}
```

`desktop/src/renderer/App.tsx`, `onSendToTab` first line:

```tsx
                if (item.request.stdin) throw new Error('Send to tab is not available for a command with stdin')
```

`desktop/src/renderer/AuditTable.tsx`, after the command `<pre>` at line 70 (`displayText` escapes `\n`, so apply it per line, as the Raw JSON block does):

```tsx
      {!r.kind && typeof r.stdin === 'string' && r.stdin !== '' && (
        <pre className="cmd stdin">{r.stdin.split('\n').map(displayText).join('\n')}</pre>
      )}
```

`desktop/src/renderer/styles.css`, after `.cmd { … }`:

```css
.approval .stdin { display: flex; flex-direction: column; gap: 4px; }
.approval .stdin-label { color: var(--muted); font-size: 12px; }
.approval .stdin .cmd, .audit-detail .cmd.stdin { max-height: 40vh; overflow: auto; }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd desktop && npm run typecheck && npm test`
Expected: exit 0. A test that pinned `PROTOCOL_VERSION`'s value or the old `highlightNonAscii` arity: update it to the new value or signature.

- [ ] **Step 5: Commit**

```bash
git add desktop/src desktop/test
git commit -m "feat(desktop): show exec stdin to the approver, in the feed and audit

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Desktop e2e through the real MCP bridge

**Files:**
- Modify: `desktop/e2e/autoallow-mcp.spec.ts` (two tests: one after `baseline`, one after `timed grant from the editor`)

**Interfaces:**
- Consumes: Tasks 3, 5, 7, 9. The e2e host is `sshtestd`, which uses the sshtest package, so it answers `stdin-echo`.

- [ ] **Step 1: Write the tests**

After the `baseline` test:

```ts
test('stdin: the card shows it, Send to tab is off, and the command gets it', async () => {
  const win = await l.app.firstWindow()
  const out = mcp.tool('exec', { server: 'box', command: 'stdin-echo', stdin: 'line one\nline two\n', description: 'mcp e2e' })
  const row = win.locator('.approval').filter({ hasText: 'stdin-echo' })
  await expect(row.locator('.stdin')).toContainText('stdin · 18 bytes, 2 lines')
  await expect(row.locator('.stdin')).toContainText('line two')
  await expect(row.getByRole('button', { name: 'Send to tab', exact: true })).toBeDisabled()
  await waitThenDecide(win, 'stdin-echo', 'allow')
  expect(await out).toContain('line one\nline two')
  expect(execRec('stdin-echo')).toMatchObject({ outcome: 'allowed', stdin: 'line one\nline two\n' })
})
```

After `timed grant from the editor…` (the grant is still on there):

```ts
test('stdin under the grant runs with no decision and is audited', async () => {
  const win = await l.app.firstWindow()
  expect(await mcp.tool('exec', { server: 'box', command: 'stdin-echo', stdin: 'auto in\n', description: 'mcp e2e' })).toContain('auto in')
  expect(execRec('stdin-echo')).toMatchObject({ outcome: 'allowed', approval: 'auto', stdin: 'auto in\n' })
  await expect(win.getByRole('region', { name: 'Auto-allowed' })).toContainText('stdin 8 B')
})
```

- [ ] **Step 2: Run the spec**

Run: `cd desktop && npm run build && npx playwright test e2e/autoallow-mcp.spec.ts` (on Linux, prefix `xvfb-run -a`)
Expected: all tests pass, including the two new ones.

- [ ] **Step 3: Run the full desktop suite**

Run: `cd desktop && npm run typecheck && npm test && npm run e2e`
Expected: exit 0. Report the pass/skip counts.

- [ ] **Step 4: Commit**

```bash
git add desktop/e2e/autoallow-mcp.spec.ts
git commit -m "test(e2e): stdin exec approved and auto through the MCP bridge

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Docs

**Files:**
- Modify: `CLAUDE.md`, `README.md`

- [ ] **Step 1: Update CLAUDE.md**

- In the UI-door paragraph: `protocol 10` → `protocol 11`.
- MCP door sentence listing `exec`/`sudoExec`: note `exec` takes an optional `stdin`.
- `Hub.Exec` order: after "validate description (…)", add `→ validate stdin (config.ValidateStdin: \n, \t, \r allowed, other control and Cf runes, over 256 KiB, and "[REDACTED:" refused), then refuse stdin with sudo (ErrStdinSudo) or on a su-password host (ErrStdinSu), unaudited and before the broker`. Where the run is described, mention that a stdin exec runs through `Executor.ExecStdin`, that the approver sees `stdin` on the request, that the audit record carries it vault-redacted, that `autoAllow.ran` carries only `stdinBytes`, and that `decide sent_to_tab` is refused for it (`broker.ErrSendToTabStdin`).
- Redaction sentence (`config.RedactCapStreams`): vault secrets masked as `***` now count as kind `secret`.
- Commands section, `sshtestd` line: add that an exec of `stdin-echo` writes back its stdin.
- `MCP tools` paragraph (`--host` mode): `exec` takes `stdin`; `sudo-exec` does not.

- [ ] **Step 2: Update README.md**

In "Tools the AI gets" (line ~150), add `stdin` (optional, at most 256 KiB) to `exec`'s arguments, and for `sudo-exec` write "same as `exec`, without `stdin`". Add a short paragraph under the table:

```markdown
`stdin` lets an agent edit a project on the host: it writes a file with `cat > path` and the content as `stdin`, or edits one with `git apply` and a unified diff as `stdin`. You see the full stdin in the request before you allow it, and the audit log keeps it. Send to tab is not available for a command with stdin. Not available on a host with a su password, or with `sudo-exec`.
```

In the AI column bullet (line ~86), mention the `stdin N B` tag on auto-allowed runs.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md README.md
git commit -m "docs: exec stdin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Final verification (before any PR)

Run, and keep the outputs for the PR body:

```bash
go vet ./... && go test -race ./...
cd desktop && npm run typecheck && npm test && npm run e2e
```

Then do the spec's manual exit gate on a real host: with a grant, an agent creates a file with `cat > f` and edits it with `git apply` through the MCP bridge, and each call is in `audit.jsonl` with `approval: "auto"` and its `stdin`.
