# MCP `upload` and `download` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Two bridge MCP tools, `upload` and `download`, that copy files and folders between the AI's project folder and a host, through the hub's approval, grant, audit and redaction rules.

**Architecture:** The bridge forwards each call with its working directory as `root` to two new MCP-door methods. `Hub.Transfer` follows `Hub.Exec`'s order (resolve, validate, `autoStart`, else `broker.Submit` with `Request.Transfer`, re-resolve) and runs the existing `internal/files` planner and copier on its own SFTP channel, with local paths opened only through `os.Root`. Downloads pass each finished part file through a secret scan before commit. One `kind: "file"` audit record per AI transfer.

**Tech Stack:** Go 1.26, `github.com/pkg/sftp`, `github.com/modelcontextprotocol/go-sdk/mcp`, mygo (`app/`).

**Spec:** `docs/superpowers/specs/2026-11-02-mcp-file-transfer-design.md`

## Global Constraints

- Bridge only: `--host` mode (`BuildServer`, `internal/mcpserver/tools.go`) gets no new tool.
- No sudo, no su: a host with a su password refuses every transfer (`ErrTransferSu`, "file transfer is not supported on this server").
- `root` must be absolute, not `/`, not the user's home folder; local paths relative (`filepath.IsLocal`); remote paths absolute and clean (`files.CheckAbs`); 1 to 100 sources; exactly one destination.
- `timeoutSec`: 1–3600, default 600 (`clampTransferTimeout`).
- Overwrite defaults to false.
- Grant cap: the existing `maxAutoInflight` (2) per host, shared with exec.
- A download file with any scan hit is never committed; nothing is ever rewritten.
- The AI never sees host, port, or SSH error detail: `forAI` texts for connection and host-key failures.
- UI-door protocol 13 (`internal/hub/idle.go` `ProtocolVersion`, `app/hubproc/proc.go` `Protocol`).
- `app/view/term.go` keeps `NoClipboardWrite: true` (untouched here).
- Every commit ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`; never a session URL.

## Review Focus

1. A selected local source that is a symlink (even one pointing inside the project): refused with a per-path error, nothing read through it. Test in Task 2.
2. A download destination folder that is missing, or `..`: the call fails before anything is written. Test in Task 6 (`TestTransferRefusals`).
3. A PEM private key whose block straddles the 1 MiB window edge: refused, kind `private_key`. Test in Task 1.
4. The vault changes between approval and run (host edited): the transfer is not run, audited `error` with "server changed". Test in Task 6 (`TestTransferServerChangedDuringWait`).
5. A transfer running when the server is saved in the app: it stops with reason "server changed", within `cancelGrace`. Test in Task 7 (`TestTransferEndedByServerSave`).

---

## File map

- `internal/config/scan.go` (new): `ScanSecrets`.
- `internal/files/plan.go`: `PlanUploadIn`, `PlanDownloadIn`; `PlanDownload` calls `PlanDownloadIn`.
- `internal/files/copy.go`: `Plan.Check`, `Result.Refused`, `Result.RefusedCount`, `Refusal`.
- `internal/broker/broker.go`: `Transfer`, `Request.Transfer`, `ErrSendToTabTransfer`.
- `internal/broker/audit.go`: new `FileRecord` fields; `ReadQuery.match` for file outcomes.
- `internal/hub/transfer.go` (new): `TransferRequest`, `TransferResponse`, `Hub.Transfer`, `runTransfer`, `aiTransfer`.
- `internal/hub/filejobs.go`: `jobSet` holds an `ender` interface.
- `internal/hub/autoallow.go`: `reportAuto` split out of `autoExec`.
- `internal/hub/mcpdoor.go`: `upload`, `download` methods.
- `internal/hub/cliui.go`: transfer block, `s` refusal.
- `internal/hub/idle.go`: protocol 13.
- `internal/mcpserver/bridge.go`: the two tools, `layoutTransfer`.
- `cmd/sshgate/e2e_test.go`: upload and download end to end.
- `app/hubapi/types.go`, `app/hubproc/proc.go`, `app/view/approvals.go`, `app/model/audit.go`: card, feed, audit row.
- Docs: spec amendments (Task 0), CLAUDE.md, README.md, ROADMAP.md (Task 12).

---

### Task 0: Spec amendments from planning

**Files:** Modify `docs/superpowers/specs/2026-11-02-mcp-file-transfer-design.md`

- [ ] **Step 1: Apply four amendments** (found while reading the code for this plan):
  1. Hub path step 6: replace "A selected symlink inside the project is followed only when its target stays inside it." with "A selected local source that is a symlink is refused (`files.errSelectedLink`); symlinks inside a selected folder are skipped, as for the UI."
  2. Download secret scan: after "Any count above zero", add: "The scan stops at the first window with a hit; the kinds reported are that window's, so a count is a lower bound, never double-counted across the overlap."
  3. UI door: replace "`autoAllow.ran` carries `op` (`upload`/`download`) and no `command` for a transfer." with "`autoAllow.ran` carries `op` (`upload`/`download`) and, as `command`, the one-line summary."
  4. Testing, `app/view`: replace "a flow test" with "a view test (`approvals_test.go`, `colApp`)".
- [ ] **Step 2: Add an Amendment line under Status:** `Amended 2026-11-02 while planning (selected symlinks refused, scan stops at the first hit, autoAllow.ran summary, view test).`
- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/specs/2026-11-02-mcp-file-transfer-design.md
git commit -m "spec: transfer amendments from planning

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 1: `config.ScanSecrets`

**Files:**
- Create: `internal/config/scan.go`
- Test: `internal/config/scan_test.go`

**Interfaces:**
- Produces: `func ScanSecrets(r *Redactor, rd io.Reader) (map[string]int, error)` — nil map when nothing matched; kinds from `RedactKinds`, vault secrets as `secret`.

- [ ] **Step 1: Write the failing tests**

```go
package config

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"testing/iotest"
)

func TestScanSecretsClean(t *testing.T) {
	got, err := ScanSecrets(NewRedactor("hunter2x"), strings.NewReader("just text\n"))
	if err != nil || got != nil {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestScanSecretsVaultSecretAndPattern(t *testing.T) {
	got, err := ScanSecrets(NewRedactor("s3cr3tVal"), strings.NewReader("a s3cr3tVal b\npassword=hunter2x\n"))
	if err != nil || got["secret"] != 1 || got["password"] != 1 {
		t.Fatalf("got %v %v", got, err)
	}
}

// A PEM block across the 1 MiB window edge still matches: the overlap
// carries the start of the block into the next window.
func TestScanSecretsAcrossWindowEdge(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun\n-----END RSA PRIVATE KEY-----\n"
	var b bytes.Buffer
	b.WriteString(strings.Repeat("x", scanWindow-40) + "\n")
	b.WriteString(pem)
	got, err := ScanSecrets(NewRedactor(), iotest.HalfReader(&b))
	if err != nil || got["private_key"] != 1 {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestScanSecretsReadError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := ScanSecrets(NewRedactor(), iotest.ErrReader(boom)); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/config -run TestScanSecrets` → `undefined: ScanSecrets`.

- [ ] **Step 3: Implement**

```go
package config

import (
	"errors"
	"io"
)

const (
	scanWindow  = 1 << 20  // bytes read per window
	scanOverlap = 64 << 10 // carried into the next window, so a block across the edge matches
)

// ScanSecrets reports the redaction hits in rd: vault secrets (kind
// "secret") and RedactPatterns kinds. It reads windows of scanWindow bytes,
// each prefixed by the previous window's last scanOverlap bytes, and stops at
// the first window with a hit; the counts are that window's, a lower bound.
// nil: nothing matched.
func ScanSecrets(r *Redactor, rd io.Reader) (map[string]int, error) {
	buf := make([]byte, scanOverlap+scanWindow)
	carry := 0
	for {
		n, err := io.ReadFull(rd, buf[carry:carry+scanWindow])
		if n > 0 {
			s, sec := r.RedactCount(string(buf[:carry+n]))
			_, c := RedactPatterns(s)
			if sec > 0 {
				if c == nil {
					c = map[string]int{}
				}
				c["secret"] += sec
			}
			if len(c) > 0 {
				return c, nil
			}
			end := carry + n
			carry = min(scanOverlap, end)
			copy(buf, buf[end-carry:end])
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
}
```

- [ ] **Step 4: Run, expect PASS** — `go test ./internal/config -run TestScanSecrets -v` → 4 PASS.
- [ ] **Step 5: Commit** — `git add internal/config/scan.go internal/config/scan_test.go && git commit -m "config: ScanSecrets for whole-file redaction checks" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 2: Rooted planning in `internal/files`

**Files:**
- Modify: `internal/files/plan.go` (after `PlanDownload`, and `PlanUpload`)
- Test: `internal/files/plan_test.go`

**Interfaces:**
- Produces:
  - `func PlanUploadIn(ctx context.Context, c *sftp.Client, root *os.Root, sources []string, dest string) (*Plan, error)` — sources relative to `root`; the plan owns the sub-roots it opens.
  - `func PlanDownloadIn(ctx context.Context, c *sftp.Client, sources []string, dest *os.Root, label string) (*Plan, error)` — the plan takes ownership of `dest` (closed by `Plan.Close`, also on error); `label` is `Plan.Local[0]` for the audit.
  - `var errSelectedLink = errors.New("a selected symlink is not followed")`, `var errNotLocal = errors.New("local paths must be inside the project folder")`.

- [ ] **Step 1: Write the failing tests** (helpers from `helpers_test.go`: `remote(t)` returns an SFTP client, the server, and the root on disk whose `home` is the server's `/home`; `write(t, path, body, mode)`)

```go
func TestPlanUploadInStaysInRoot(t *testing.T) {
	c, _, _ := remote(t)
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, "dir"), 0o755)
	os.WriteFile(filepath.Join(proj, "dir", "a.txt"), []byte("a"), 0o644)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600)
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(proj, "link"))
	os.Symlink("dir/a.txt", filepath.Join(proj, "inlink"))
	root, err := os.OpenRoot(proj)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := PlanUploadIn(context.Background(), c, root, []string{"../x"}, "/home"); !errors.Is(err, errNotLocal) {
		t.Fatalf("..: %v", err)
	}
	if _, err := PlanUploadIn(context.Background(), c, root, []string{"."}, "/home"); !errors.Is(err, errNotLocal) {
		t.Fatalf(".: %v", err)
	}
	p, err := PlanUploadIn(context.Background(), c, root, []string{"dir", "link", "inlink"}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Files != 1 || p.Dirs != 1 || p.Errors.Count != 2 {
		t.Fatalf("files %d dirs %d errors %v", p.Files, p.Dirs, p.Errors.List)
	}
	for _, e := range p.Errors.List {
		if !strings.Contains(e, errSelectedLink.Error()) {
			t.Fatalf("error %q", e)
		}
	}
}

func TestPlanDownloadInOwnsRoot(t *testing.T) {
	c, _, _ := remote(t)
	dest, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanDownloadIn(context.Background(), c, []string{"rel"}, dest, "label"); err == nil {
		t.Fatal("relative remote source accepted")
	}
	if _, err := dest.Stat("."); err == nil {
		t.Fatal("dest root not closed on error")
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/files -run 'TestPlanUploadIn|TestPlanDownloadIn'` → undefined.

- [ ] **Step 3: Implement** in `plan.go`:

```go
var (
	errSelectedLink = errors.New("a selected symlink is not followed")
	errNotLocal     = errors.New("local paths must be inside the project folder")
)

// PlanDownloadIn is PlanDownload into an already open folder. The plan owns
// dest: Close releases it, and it is closed here on error. label names dest
// in the audit.
func PlanDownloadIn(ctx context.Context, c *sftp.Client, sources []string, dest *os.Root, label string) (*Plan, error) {
	for _, s := range sources {
		if err := CheckAbs(s); err != nil {
			dest.Close()
			return nil, err
		}
	}
	p := &Plan{Op: OpDownload, local: dest, Remote: sources, Local: []string{label}}
	w := &walker{ctx: ctx, p: p, seen: map[string]map[string]bool{}}
	for _, s := range sources {
		fi, err := c.Stat(s) // a selected symlink is followed once
		if err != nil {
			p.Errors.Add(s, err)
			continue
		}
		if err := w.down(c, path.Dir(s), "", path.Base(s), fi, 0); err != nil {
			p.Close()
			return nil, err
		}
	}
	return p, nil
}
```

and make `PlanDownload` keep its checks, then `return PlanDownloadIn(ctx, c, sources, root, dest)` after opening `root` (drop its now-duplicated loop and `p` construction).

```go
// PlanUploadIn is PlanUpload for sources relative to root (the AI's project
// folder). Every local read goes through root, so ".." and symlinks cannot
// leave it; a selected symlink is refused rather than followed.
func PlanUploadIn(ctx context.Context, c *sftp.Client, root *os.Root, sources []string, dest string) (*Plan, error) {
	for _, s := range sources {
		if !filepath.IsLocal(s) || filepath.Clean(s) == "." {
			return nil, errNotLocal
		}
	}
	if err := CheckAbs(dest); err != nil {
		return nil, err
	}
	if fi, err := c.Stat(dest); err != nil || !fi.IsDir() {
		return nil, errors.New("the upload folder is missing or not a folder")
	}
	p := &Plan{Op: OpUpload, dest: dest, Remote: []string{dest}, Local: sources}
	w := &walker{ctx: ctx, p: p, seen: map[string]map[string]bool{"": {}}}
	for _, s := range sources {
		base := filepath.Base(s)
		low := strings.ToLower(base)
		if w.seen[""][low] { // two sources collapsing onto one remote name
			p.Errors.Add(base, errDuplicateName)
			continue
		}
		w.seen[""][low] = true
		parent, err := root.OpenRoot(filepath.Dir(s))
		if err != nil {
			p.Errors.Add(s, bare(err))
			continue
		}
		p.roots = append(p.roots, parent)
		fi, err := parent.Lstat(base)
		if err != nil {
			p.Errors.Add(s, bare(err))
			continue
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			p.Errors.Add(s, errSelectedLink)
			continue
		}
		if err := w.up(c, parent, base, base, fi, 0); err != nil {
			p.Close()
			return nil, err
		}
	}
	return p, nil
}
```

- [ ] **Step 4: Run, expect PASS** — `go test -race ./internal/files` (whole package: `PlanDownload` was refactored).
- [ ] **Step 5: Commit** — `git commit -am "files: rooted upload and download planning" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"` (add the test file if new).

---

### Task 3: Download check hook

**Files:**
- Modify: `internal/files/copy.go` (`Result`, `runCopy`, `download`), `internal/files/plan.go` (`Plan` field)
- Test: `internal/files/copy_test.go`

**Interfaces:**
- Consumes: Task 2's `PlanDownloadIn`.
- Produces:
  - `Plan.Check func(io.Reader) (map[string]int, error)` — downloads only; nil means no check.
  - `type Refusal struct { Path string `json:"path"`; Kinds map[string]int `json:"kinds"` }`
  - `Result.Refused []Refusal` (first `MaxErrors`) and `Result.RefusedCount int`.

- [ ] **Step 1: Write the failing test** (helpers `remote`, `write`, `read` from `helpers_test.go`):

```go
func TestDownloadCheckRefusesAndKeepsOthers(t *testing.T) {
	c, _, rroot := remote(t)
	write(t, filepath.Join(rroot, "home", "bad"), "password=hunter2x\n", 0o644)
	write(t, filepath.Join(rroot, "home", "good"), "fine\n", 0o644)
	destDir := t.TempDir()
	dest, _ := os.OpenRoot(destDir)
	p, err := PlanDownloadIn(context.Background(), c, []string{"/home/bad", "/home/good"}, dest, destDir)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Check = func(r io.Reader) (map[string]int, error) {
		b, _ := io.ReadAll(r)
		if strings.Contains(string(b), "password=") {
			return map[string]int{"password": 1}, nil
		}
		return nil, nil
	}
	r := p.Run(context.Background(), c, false, nil)
	if r.Copied != 1 || r.RefusedCount != 1 || r.Refused[0].Path != "bad" || r.Refused[0].Kinds["password"] != 1 {
		t.Fatalf("result %+v", r)
	}
	if _, err := os.Stat(filepath.Join(destDir, "bad")); !os.IsNotExist(err) {
		t.Fatal("refused file committed")
	}
	if got := read(t, filepath.Join(destDir, "good")); got != "fine\n" {
		t.Fatalf("good = %q", got)
	}
	if des, _ := os.ReadDir(destDir); len(des) != 1 {
		t.Fatalf("left behind: %v", des) // only "good": no part file
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — undefined `Check`, `RefusedCount`.

- [ ] **Step 3: Implement**

In `Plan` (plan.go), after `Local`:

```go
	// Check, when set on a download, sees each finished part file before it
	// is committed; any count refuses the file (spec 2026-11-02, scan).
	Check func(io.Reader) (map[string]int, error)
```

In copy.go:

```go
// Refusal is a downloaded file Check refused, with its hit kinds.
type Refusal struct {
	Path  string         `json:"path"`
	Kinds map[string]int `json:"kinds"`
}

type refusedError struct{ kinds map[string]int }

func (e *refusedError) Error() string { return "holds a secret; not saved" }
```

`Result` gains:

```go
	Refused      []Refusal // first MaxErrors
	RefusedCount int
```

In `download`, between the `Chtimes` step and `commitLocal`:

```go
	if err == nil && p.Check != nil {
		err = p.check(part)
	}
```

with

```go
func (p *Plan) check(part string) error {
	f, err := p.local.Open(part)
	if err != nil {
		return err
	}
	defer f.Close()
	kinds, err := p.Check(f)
	if err != nil {
		return err
	}
	if len(kinds) > 0 {
		return &refusedError{kinds}
	}
	return nil
}
```

The existing error branch in `download` removes the part file and returns `bare(err)`; make it keep a `*refusedError` unwrapped: `if re := (*refusedError)(nil); errors.As(err, &re) { return w.n, re }` before `return w.n, bare(err)`.

In `runCopy`'s error branch, before `r.Errors.Add`:

```go
			var re *refusedError
			if errors.As(err, &re) {
				r.RefusedCount++
				if len(r.Refused) < MaxErrors {
					r.Refused = append(r.Refused, Refusal{Path: it.rel, Kinds: re.kinds})
				}
				continue
			}
```

- [ ] **Step 4: Run, expect PASS** — `go test -race ./internal/files`.
- [ ] **Step 5: Commit** — `git commit -am "files: refuse downloads a check flags, before commit" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 4: Broker: `Request.Transfer`, send-to-tab refusal, file record fields, outcome filter

**Files:**
- Modify: `internal/broker/broker.go` (`Request`, errors, `Decide`), `internal/broker/audit.go` (`FileRecord`, `match`)
- Test: `internal/broker/broker_test.go`, `internal/broker/audit_test.go`

**Interfaces:**
- Produces:

```go
// Transfer is an AI upload or download awaiting approval. For an upload,
// Remote is the one destination folder; for a download, Local is.
type Transfer struct {
	Op        string   `json:"op"` // "upload" or "download"
	Root      string   `json:"root"`
	Local     []string `json:"local"`
	Remote    []string `json:"remote"`
	Overwrite bool     `json:"overwrite,omitempty"`
}
```

  - `Request.Transfer *Transfer `json:"transfer,omitempty"``
  - `ErrSendToTabTransfer = errors.New("send to tab is not available for a file transfer")`
  - `FileRecord` gains, after `Reason`:

```go
	Client      string         `json:"client,omitempty"`      // set: an AI transfer (spec 2026-11-02)
	Description string         `json:"description,omitempty"`
	Outcome     string         `json:"outcome,omitempty"`     // AI transfers: exec's outcome values
	Approval    string         `json:"approval,omitempty"`    // "auto" under a grant
	WaitMs      int64          `json:"waitMs,omitempty"`
	Refused     int            `json:"refused,omitempty"`     // downloads refused by the secret scan
	Redacted    map[string]int `json:"redacted,omitempty"`    // their kinds, summed; never a value
```

- [ ] **Step 1: Write the failing tests**

broker_test.go (mirror the existing `ErrSendToTabStdin` test at line ~302 for its setup):

```go
func TestDecideRefusesSendToTabForTransfer(t *testing.T) {
	b, _, _ := newTestBroker(time.Minute)
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Submit(context.Background(), Request{Server: "s", Command: "upload 1 path to /srv", Transfer: &Transfer{Op: "upload", Root: "/p", Local: []string{"a"}, Remote: []string{"/srv"}}})
	}()
	r := waitForPending(t, b, 1)[0]
	if err := b.Decide(r.ID, Decision{Outcome: SentToTab}); !errors.Is(err, ErrSendToTabTransfer) {
		t.Fatalf("err = %v", err)
	}
	b.Decide(r.ID, Decision{Outcome: Denied})
	<-done
}
```

audit_test.go:

```go
func TestReadOutcomesMatchAITransferRecords(t *testing.T) {
	a, _ := openTestAudit(t)
	a.WriteFile(FileRecord{Action: "upload", Server: "s", Client: "c", Outcome: "denied"})
	a.WriteFile(FileRecord{Action: "upload", Server: "s", Client: "c", Outcome: "allowed", Approval: "auto"})
	a.WriteFile(FileRecord{Phase: "end", Action: "upload", Server: "s"}) // a UI job: no outcome
	for _, tc := range []struct {
		o    string
		want int
	}{{"denied", 1}, {"auto", 1}, {"allowed", 0}} {
		page, err := a.Read(ReadQuery{Outcomes: []string{tc.o}})
		if err != nil || len(page.Records) != tc.want {
			t.Fatalf("%s: %d records, %v", tc.o, len(page.Records), err)
		}
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/broker -run 'SendToTabForTransfer|AITransferRecords'`.

- [ ] **Step 3: Implement**
  - Add `Transfer`, the field, and the error next to `ErrSendToTabStdin`.
  - In `Decide`'s `SentToTab` loop: `if p.req.ID == id && p.req.Transfer != nil { b.mu.Unlock(); return ErrSendToTabTransfer }` beside the stdin check.
  - Add the `FileRecord` fields.
  - In `match`, replace the last line with:

```go
	return (kind == "exec" || kind == "file" && f.Outcome != "") && slices.ContainsFunc(q.Outcomes, f.outcomeIs)
```

- [ ] **Step 4: Run, expect PASS** — `go test -race ./internal/broker`.
- [ ] **Step 5: Commit** — `git commit -am "broker: transfer requests and AI file audit fields" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 5: Hub plumbing: `ender` job set and `reportAuto`

**Files:**
- Modify: `internal/hub/filejobs.go` (`jobSet`), `internal/hub/autoallow.go` (`autoExec`)
- Test: existing `internal/hub` tests (pure refactor)

**Interfaces:**
- Produces:
  - `type ender interface { serverName() string; end(reason string) }`; `jobSet.add(ender)`, `jobSet.remove(ender)`; `func (j *fileJob) serverName() string { return j.server }`.
  - `func (h *Hub) reportAuto(server string, ran map[string]any, err error)` — the soft-lock accounting and `autoAllow.ran` send now at the end of `autoExec`.

- [ ] **Step 1: Run the hub tests first, record they pass** — `go test -race ./internal/hub` → ok.
- [ ] **Step 2: Refactor `jobSet`**

```go
// ender is a running transfer a server change must stop: a UI job or an AI transfer.
type ender interface {
	serverName() string
	end(reason string)
}

type jobSet struct {
	mu  sync.Mutex
	all map[ender]struct{}
}

func newJobSet() *jobSet { return &jobSet{all: map[ender]struct{}{}} }

func (s *jobSet) add(j ender)    { s.mu.Lock(); s.all[j] = struct{}{}; s.mu.Unlock() }
func (s *jobSet) remove(j ender) { s.mu.Lock(); delete(s.all, j); s.mu.Unlock() }

func (s *jobSet) endServer(name, reason string) {
	s.mu.Lock()
	var js []ender
	for j := range s.all {
		if j.serverName() == name {
			js = append(js, j)
		}
	}
	s.mu.Unlock()
	for _, j := range js {
		j.end(reason)
	}
}

func (j *fileJob) serverName() string { return j.server }
```

- [ ] **Step 3: Split `reportAuto` out of `autoExec`**: move everything from the comment "Nothing with content goes to a locked UI" to the `notifyAuto` call into

```go
// reportAuto counts a run that succeeded under soft lock, or sends ran to an
// unlocked UI (see autoExec).
func (h *Hub) reportAuto(server string, ran map[string]any, err error) {
	h.mu.Lock()
	soft, unlocked := h.autoKey != nil, h.deps.MasterKey != nil
	if soft && err == nil {
		h.ranLocked[server]++
	}
	h.mu.Unlock()
	if unlocked {
		h.notifyAuto("autoAllow.ran", ran)
	}
}
```

keeping the comment above it, and end `autoExec` with `h.reportAuto(r.Server, ran, err); return resp, err`.

- [ ] **Step 4: Run, expect PASS** — `go vet ./internal/hub && go test -race ./internal/hub`.
- [ ] **Step 5: Commit** — `git commit -am "hub: job set takes any ender; reportAuto" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 6: `Hub.Transfer`: validation and the approved path

**Files:**
- Create: `internal/hub/transfer.go`
- Test: `internal/hub/transfer_test.go`

**Interfaces:**
- Consumes: Tasks 1–5 (`config.ScanSecrets`, `files.PlanUploadIn`, `files.PlanDownloadIn`, `Plan.Check`, `Result.Refused`, `broker.Transfer`, `FileRecord` fields, `ender`, `reportAuto`).
- Produces:

```go
type TransferRequest struct {
	Client, Server, Description string
	Op         files.Op // files.OpUpload or files.OpDownload
	Root       string   // the bridge's working directory, absolute
	Local      []string // upload: sources; download: [destination folder]; relative to Root
	Remote     []string // upload: [destination folder]; download: sources; absolute
	Overwrite  bool
	TimeoutSec int
}

type TransferResponse struct {
	Op           files.Op        `json:"op"`
	Files        int             `json:"files"`
	Bytes        int64           `json:"bytes"`
	Skipped      int             `json:"skipped"`
	SkippedNames []string        `json:"skippedNames,omitempty"` // first 5, from the plan's conflicts
	Refused      []files.Refusal `json:"refused,omitempty"`
	RefusedCount int             `json:"refusedCount,omitempty"`
	Errors       []string        `json:"errors,omitempty"`
	ErrorCount   int             `json:"errorCount,omitempty"`
}

var (
	ErrTransferSu      = errors.New("file transfer is not supported on this server")
	ErrProjectFolder   = errors.New("the project folder must be an absolute path other than / and your home folder")
)

func (h *Hub) Transfer(ctx context.Context, r TransferRequest) (TransferResponse, error)
```

- [ ] **Step 1: Write the test fixture and failing tests** in `transfer_test.go`:

```go
package hub

// aiFiles: filesHub with "fs" visible to AI, and a project folder.
func aiFiles(t *testing.T) (filesFixture, string) {
	t.Helper()
	fx := filesHub(t)
	if err := config.Update(fx.store, fx.h.deps.MasterKey, func(f *config.File) error {
		f.Servers[0].AIVisible = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.Reload(); err != nil {
		t.Fatal(err)
	}
	return fx, t.TempDir()
}

// decideTransfer runs h.Transfer, decides its pending request with d, and
// returns what Transfer returned.
func decideTransfer(t *testing.T, h *Hub, r TransferRequest, d broker.Decision) (TransferResponse, error) {
	t.Helper()
	type res struct {
		r   TransferResponse
		err error
	}
	c := make(chan res, 1)
	go func() { r, err := h.Transfer(context.Background(), r); c <- res{r, err} }()
	waitPending(t, h.Broker(), 1)
	h.Broker().Decide(h.Broker().Pending()[0].ID, d)
	got := <-c
	return got.r, got.err
}

func TestTransferUploadApproved(t *testing.T) {
	fx, proj := aiFiles(t)
	os.MkdirAll(filepath.Join(proj, "build"), 0o755)
	os.WriteFile(filepath.Join(proj, "build", "app.bin"), []byte{0, 1, 2, 0xff}, 0o755)
	type res struct {
		r   TransferResponse
		err error
	}
	c := make(chan res, 1)
	go func() {
		r, err := fx.h.Transfer(context.Background(), TransferRequest{Client: "c", Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"build"}, Remote: []string{"/home"}})
		c <- res{r, err}
	}()
	waitPending(t, fx.h.Broker(), 1)
	p := fx.h.Broker().Pending()[0]
	if p.Transfer == nil || p.Transfer.Op != "upload" || p.Transfer.Root != proj || p.Transfer.Remote[0] != "/home" {
		t.Fatalf("pending %+v", p)
	}
	fx.h.Broker().Decide(p.ID, broker.Decision{Outcome: broker.Allowed})
	got := <-c
	resp, err := got.r, got.err
	if err != nil || resp.Files != 1 || resp.Bytes != 4 {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if b, _ := os.ReadFile(filepath.Join(fx.root, "home", "build", "app.bin")); !bytes.Equal(b, []byte{0, 1, 2, 0xff}) {
		t.Fatalf("remote file %v", b)
	}
	rec := fileRecords(t, fx.store)
	last := rec[len(rec)-1]
	if last["client"] != "c" || last["outcome"] != "allowed" || last["action"] != "upload" || last["approval"] != nil {
		t.Fatalf("audit %v", last)
	}
}

func TestTransferDownloadRefusesSecrets(t *testing.T) {
	fx, proj := aiFiles(t)
	os.MkdirAll(filepath.Join(fx.root, "home", "app"), 0o755)
	os.WriteFile(filepath.Join(fx.root, "home", "app", ".env"), []byte("password=hunter2x\n"), 0o600)
	os.WriteFile(filepath.Join(fx.root, "home", "app", "main.go"), []byte("package main\n"), 0o644)
	os.Mkdir(filepath.Join(proj, "out"), 0o755)
	resp, err := decideTransfer(t, fx.h, TransferRequest{Client: "c", Server: "fs", Op: files.OpDownload, Root: proj, Local: []string{"out"}, Remote: []string{"/home/app"}}, broker.Decision{Outcome: broker.Allowed})
	if err != nil || resp.Files != 1 || resp.RefusedCount != 1 || resp.Refused[0].Path != "app/.env" || resp.Refused[0].Kinds["password"] != 1 {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if _, err := os.Stat(filepath.Join(proj, "out", "app", ".env")); !os.IsNotExist(err) {
		t.Fatal(".env written locally")
	}
	rec := fileRecords(t, fx.store)
	if last := rec[len(rec)-1]; last["refused"] != json.Number("1") && last["refused"] != float64(1) {
		t.Fatalf("audit %v", last)
	}
}

func TestTransferDeniedAndExpiredAudited(t *testing.T) {
	fx, proj := aiFiles(t)
	os.WriteFile(filepath.Join(proj, "f"), []byte("x"), 0o644)
	req := TransferRequest{Client: "c", Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}}
	if _, err := decideTransfer(t, fx.h, req, broker.Decision{Outcome: broker.Denied, Reason: "no"}); err == nil {
		t.Fatal("denied transfer succeeded")
	}
	rec := fileRecords(t, fx.store)
	if last := rec[len(rec)-1]; last["outcome"] != "denied" || last["reason"] != "no" {
		t.Fatalf("audit %v", last)
	}
	if _, err := os.Stat(filepath.Join(fx.root, "home", "f")); !os.IsNotExist(err) {
		t.Fatal("denied upload ran")
	}
	// Send to tab is refused by the broker and the request stays pending.
	// (The hub's own SentToTab branch is unreachable through the broker; it
	// is there to fail closed if that refusal ever regresses.)
	c := make(chan error, 1)
	go func() { _, err := fx.h.Transfer(context.Background(), req); c <- err }()
	waitPending(t, fx.h.Broker(), 1)
	id := fx.h.Broker().Pending()[0].ID
	if err := fx.h.Broker().Decide(id, broker.Decision{Outcome: broker.SentToTab}); !errors.Is(err, broker.ErrSendToTabTransfer) {
		t.Fatalf("sent_to_tab: %v", err)
	}
	fx.h.Broker().Decide(id, broker.Decision{Outcome: broker.Denied})
	if err := <-c; err == nil {
		t.Fatal("transfer succeeded")
	}
	if _, err := os.Stat(filepath.Join(fx.root, "home", "f")); !os.IsNotExist(err) {
		t.Fatal("upload ran")
	}
}

// Refused before the broker: nothing pending, nothing audited.
func TestTransferRefusals(t *testing.T) {
	fx, proj := aiFiles(t)
	home, _ := os.UserHomeDir()
	up := func(mod func(*TransferRequest)) error {
		r := TransferRequest{Client: "c", Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}}
		mod(&r)
		_, err := fx.h.Transfer(context.Background(), r)
		return err
	}
	for name, mod := range map[string]func(*TransferRequest){
		"root /":        func(r *TransferRequest) { r.Root = "/" },
		"root home":     func(r *TransferRequest) { r.Root = home },
		"root relative": func(r *TransferRequest) { r.Root = "proj" },
		"local ..":      func(r *TransferRequest) { r.Local = []string{"../x"} },
		"local abs":     func(r *TransferRequest) { r.Local = []string{"/etc/passwd"} },
		"remote rel":    func(r *TransferRequest) { r.Remote = []string{"home"} },
		"two dests":     func(r *TransferRequest) { r.Remote = []string{"/a", "/b"} },
		"no sources":    func(r *TransferRequest) { r.Local = nil },
		"control char":  func(r *TransferRequest) { r.Local = []string{"a\x1bb"} },
		"download ..":   func(r *TransferRequest) { r.Op, r.Local, r.Remote = files.OpDownload, []string{".."}, []string{"/home/f"} },
	} {
		if err := up(mod); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if n := len(fx.h.Broker().Pending()); n != 0 {
		t.Fatalf("%d pending", n)
	}
	if n := len(fileRecords(t, fx.store)); n != 0 {
		t.Fatalf("%d audit records", n)
	}
}

func TestTransferRefusesSuHost(t *testing.T) {
	fx, proj := aiFiles(t)
	if err := config.Update(fx.store, fx.h.deps.MasterKey, func(f *config.File) error {
		s := &f.Servers[0]
		enc, err := config.Encrypt(fx.h.deps.MasterKey, s.Name+"/encSuPassword", config.AADFor(f, *s, "encSuPassword"), "rootpw")
		s.EncSuPassword = enc
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.Reload(); err != nil {
		t.Fatal(err)
	}
	_, err := fx.h.Transfer(context.Background(), TransferRequest{Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}})
	if !errors.Is(err, ErrTransferSu) {
		t.Fatalf("err = %v", err)
	}
}

func TestTransferServerChangedDuringWait(t *testing.T) {
	fx, proj := aiFiles(t)
	os.WriteFile(filepath.Join(proj, "f"), []byte("x"), 0o644)
	c := make(chan error, 1)
	go func() {
		_, err := fx.h.Transfer(context.Background(), TransferRequest{Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}})
		c <- err
	}()
	waitPending(t, fx.h.Broker(), 1)
	id := fx.h.Broker().Pending()[0].ID
	// change the host behind the broker's back, as a hand edit would
	if err := config.Update(fx.store, fx.h.deps.MasterKey, func(f *config.File) error {
		f.Servers[0].Port++
		f.Servers[0].HostKey = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fx.h.Reload()
	fx.h.Broker().Decide(id, broker.Decision{Outcome: broker.Allowed})
	if err := <-c; err == nil {
		t.Fatal("ran after the server changed")
	}
	rec := fileRecords(t, fx.store)
	if last := rec[len(rec)-1]; last["outcome"] != "error" {
		t.Fatalf("audit %v", last)
	}
}
```

Note for the implementer: `fx.h.deps.MasterKey` is read without `h.mu` here; if `-race` flags it, take the key under `fx.h.mu` first.

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/hub -run TestTransfer` → `undefined: TransferRequest`.

- [ ] **Step 3: Implement `internal/hub/transfer.go`**

```go
package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/sftp"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/files"
	"github.com/lang315/sshgate/internal/sshx"
)

// (TransferRequest, TransferResponse and the errors from Interfaces above.)

const maxTransferSources = 100

func clampTransferTimeout(sec int) int {
	switch {
	case sec == 0:
		return 600
	case sec < 1:
		return 1
	case sec > 3600:
		return 3600
	}
	return sec
}

// validateTransfer checks everything that needs no server. Paths are shown
// to the approver, so control and format characters are refused too.
func validateTransfer(r TransferRequest) error {
	if r.Op != files.OpUpload && r.Op != files.OpDownload {
		return errors.New("unknown transfer")
	}
	if err := config.ValidateDescription(r.Description); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	clean := filepath.Clean(r.Root)
	if !filepath.IsAbs(r.Root) || clean == filepath.VolumeName(clean)+string(filepath.Separator) || home != "" && clean == filepath.Clean(home) {
		return ErrProjectFolder
	}
	srcs, dests := r.Local, r.Remote
	if r.Op == files.OpDownload {
		srcs, dests = r.Remote, r.Local
	}
	if len(srcs) == 0 || len(srcs) > maxTransferSources || len(dests) != 1 {
		return fmt.Errorf("give 1 to %d sources and one destination folder", maxTransferSources)
	}
	for _, p := range append(append([]string{}, r.Local...), r.Remote...) {
		if _, err := config.SanitizeCommand(p, -1); err != nil {
			return errors.New("a path contains a forbidden character")
		}
	}
	for _, p := range r.Local {
		if !filepath.IsLocal(p) {
			return errors.New("local paths must be relative to the project folder and stay inside it")
		}
	}
	for _, p := range r.Remote {
		if err := files.CheckAbs(p); err != nil {
			return errors.New("remote paths must be absolute and clean")
		}
	}
	return nil
}

// transferSummary is the one line that stands in for a command.
func transferSummary(r TransferRequest) string {
	if r.Op == files.OpUpload {
		return fmt.Sprintf("upload %d path(s) to %s", len(r.Local), r.Remote[0])
	}
	return fmt.Sprintf("download %d path(s) to %s", len(r.Remote), r.Local[0])
}

// Transfer is Exec's order for a file transfer (spec 2026-11-02).
func (h *Hub) Transfer(ctx context.Context, r TransferRequest) (TransferResponse, error) {
	dc, err := h.resolveForAuto(r.Server)
	if err != nil {
		var he *hiddenError
		if errors.As(err, &he) {
			fmt.Fprintf(os.Stderr, "hub: resolve %q: %v\n", r.Server, he.detail)
		}
		return TransferResponse{}, err
	}
	if err := validateTransfer(r); err != nil {
		return TransferResponse{}, err
	}
	if dc.SuPassword != "" {
		return TransferResponse{}, ErrTransferSu
	}
	timeout := clampTransferTimeout(r.TimeoutSec)
	red := redactorFor(dc)
	local := make([]string, len(r.Local))
	for i, l := range r.Local {
		local[i] = filepath.Join(r.Root, l)
	}
	conflict := "skip"
	if r.Overwrite {
		conflict = "overwrite"
	}
	base := broker.FileRecord{Action: string(r.Op), Server: r.Server, Host: dc.Host, Port: dc.Port, Remote: r.Remote, Local: local,
		Conflict: conflict, Client: r.Client, Description: red.Redact(r.Description)}

	ar, skip := h.autoStart(ctx, r.Server, false)
	if ar != nil {
		defer ar.done()
		base.Approval = "auto"
		resp, err := h.runTransfer(ar.ctx, r, ar.dc, timeout, base, redactorFor(dc, ar.dc))
		ran := map[string]any{"server": r.Server, "op": string(r.Op), "command": transferSummary(r),
			"description": base.Description, "time": time.Now().UTC().Format(time.RFC3339)}
		if err != nil {
			ran["error"] = err.Error()
		}
		h.reportAuto(r.Server, ran, err)
		return resp, err
	}
	if h.Locked() {
		if skip == skipBusy {
			return TransferResponse{}, ErrAutoBusy
		}
		return TransferResponse{}, ErrLocked
	}

	req := broker.Request{Client: r.Client, Server: r.Server, Target: target(dc), Command: transferSummary(r), Description: r.Description, TimeoutSec: timeout,
		Transfer: &broker.Transfer{Op: string(r.Op), Root: r.Root, Local: r.Local, Remote: r.Remote, Overwrite: r.Overwrite}}
	submitted := time.Now()
	d, err := h.broker.Submit(ctx, req)
	if err != nil {
		return TransferResponse{}, err
	}
	base.WaitMs = time.Since(submitted).Milliseconds()
	switch d.Outcome {
	case broker.Expired:
		base.Outcome = string(broker.Expired)
		h.auditFile(base)
		return TransferResponse{}, ErrExpired
	case broker.Denied:
		base.Outcome, base.Reason = string(broker.Denied), d.Reason
		h.auditFile(base)
		return TransferResponse{}, &DeniedError{Reason: d.Reason}
	case broker.SentToTab: // Decide refuses it; the hub fails closed anyway
		base.Outcome, base.Reason = "error", broker.ErrSendToTabTransfer.Error()
		h.auditFile(base)
		return TransferResponse{}, broker.ErrSendToTabTransfer
	}

	h.mu.Lock()
	h.running++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.running--
		h.mu.Unlock()
	}()
	if ctx.Err() != nil {
		base.Outcome = string(broker.ApprovedButCancelled)
		h.auditFile(base)
		return TransferResponse{}, ctx.Err()
	}
	dc2, err := h.resolveForAI(r.Server)
	if err != nil {
		base.Outcome, base.Reason = "error", red.Redact(detail(err))
		h.auditFile(base)
		return TransferResponse{}, err
	}
	if target(dc2) != target(dc) || dc2.HostKey != dc.HostKey {
		base.Outcome = "error"
		base.Reason = fmt.Sprintf("server changed: approved %s %s, now %s %s", target(dc), dc.HostKey, target(dc2), dc2.HostKey)
		h.auditFile(base)
		return TransferResponse{}, ErrServerChanged
	}
	if dc2.SuPassword != "" { // saved during the wait
		base.Outcome, base.Reason = "error", ErrTransferSu.Error()
		h.auditFile(base)
		return TransferResponse{}, ErrTransferSu
	}
	return h.runTransfer(ctx, r, dc2, timeout, base, redactorFor(dc, dc2))
}

// aiTransfer lets a server change stop a running AI transfer (jobSet).
type aiTransfer struct {
	server string
	cancel context.CancelFunc
	mu     sync.Mutex
	reason string
}

func (t *aiTransfer) serverName() string { return t.server }

func (t *aiTransfer) end(reason string) {
	t.mu.Lock()
	if t.reason == "" {
		t.reason = reason
	}
	t.mu.Unlock()
	t.cancel()
}

// runTransfer plans and runs on its own SFTP channel, then audits base.
// Every return after the channel opens writes exactly one record.
func (h *Hub) runTransfer(ctx context.Context, r TransferRequest, dc sshx.DialConfig, timeout int, base broker.FileRecord, red *config.Redactor) (TransferResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	fail := func(outcome, reason string, aiErr error) (TransferResponse, error) {
		base.Outcome, base.Reason = outcome, red.Redact(reason)
		h.auditFile(base)
		return TransferResponse{}, aiErr
	}
	root, err := os.OpenRoot(r.Root)
	if err != nil {
		return fail("error", "project folder: "+err.Error(), ErrProjectFolder)
	}
	defer root.Close()
	c, err := h.Registry().Get(r.Server, dc).NewSFTP()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hub: transfer on %q: %s\n", r.Server, red.Redact(err.Error()))
		return fail("error", err.Error(), forAI(err))
	}
	done := make(chan struct{})
	defer c.Close()
	defer close(done) // runs first: the grace closer below must see done before c closes
	// ctx alone does not stop pkg/sftp's calls: after cancelGrace the
	// channel is closed, as for a UI job.
	stop := context.AfterFunc(ctx, func() {
		time.AfterFunc(cancelGrace, func() {
			select {
			case <-done:
			default:
				c.Close()
			}
		})
	})
	defer stop()
	t := &aiTransfer{server: r.Server, cancel: cancel}
	h.files.add(t)
	defer h.files.remove(t)

	var p *files.Plan
	if r.Op == files.OpUpload {
		p, err = files.PlanUploadIn(ctx, c, root, r.Local, r.Remote[0])
	} else {
		dest, oerr := root.OpenRoot(r.Local[0])
		if oerr != nil {
			return fail("error", "download folder: "+oerr.Error(), errors.New("the download folder is missing or not a folder"))
		}
		p, err = files.PlanDownloadIn(ctx, c, r.Remote, dest, filepath.Join(r.Root, r.Local[0]))
		if err == nil {
			p.Check = func(rd io.Reader) (map[string]int, error) { return config.ScanSecrets(red, rd) }
		}
	}
	if err != nil {
		return h.transferEnded(ctx, t, base, red, files.Result{}, err)
	}
	defer p.Close()
	res := p.Run(ctx, c, r.Overwrite, nil)
	resp, err := h.transferEnded(ctx, t, base, red, res, res.Fatal)
	resp.Op = r.Op
	if res.Skipped > 0 {
		resp.SkippedNames = p.Sample
	}
	return resp, err
}

// transferEnded audits a transfer that reached planning and builds the
// AI's answer. A stop (server change, cancel, timeout) wins over res.
func (h *Hub) transferEnded(ctx context.Context, t *aiTransfer, base broker.FileRecord, red *config.Redactor, res files.Result, fatal error) (TransferResponse, error) {
	t.mu.Lock()
	reason := t.reason
	t.mu.Unlock()
	base.Files, base.Bytes, base.Skipped, base.ErrorCount = res.Copied, res.Bytes, res.Skipped, res.Errors.Count
	base.Refused = res.RefusedCount
	for _, rf := range res.Refused {
		for k, n := range rf.Kinds {
			if base.Redacted == nil {
				base.Redacted = map[string]int{}
			}
			base.Redacted[k] += n
		}
	}
	var aiErr error
	switch {
	case reason != "":
		base.Outcome, base.Reason, aiErr = "error", reason, ErrServerChanged // the job set's only reason
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		base.Outcome, base.Reason, aiErr = "error", "timed out", sshx.ErrTimeout
	case ctx.Err() != nil:
		base.Outcome, aiErr = "cancelled_running", ErrCancelledRunning
	case fatal != nil:
		base.Outcome, base.Reason = "error", red.Redact(fatal.Error())
		aiErr = errors.New("transfer stopped: " + base.Reason)
	default:
		base.Outcome = string(broker.Allowed)
	}
	base.Cancelled = ctx.Err() != nil
	h.auditFile(base)
	errs := make([]string, len(res.Errors.List))
	for i, e := range res.Errors.List {
		errs[i] = red.Redact(e)
	}
	resp := TransferResponse{Files: res.Copied, Bytes: res.Bytes, Skipped: res.Skipped, Refused: res.Refused, RefusedCount: res.RefusedCount,
		Errors: errs, ErrorCount: res.Errors.Count}
	return resp, aiErr
}
```

Check while implementing: `h.Registry()`, `h.files`, `ErrCancelledRunning`, `ErrExpired`, `DeniedError`, `ErrServerChanged`, `sshx.ErrTimeout` exist under those names (grep; they are used by `Hub.Exec`/`run`). If a plan error (`err` from `PlanUploadIn`) is a local or remote message, `transferEnded` passes it as `fatal`, so the AI sees it redacted.

- [ ] **Step 4: Run, expect PASS** — `go test -race ./internal/hub -run TestTransfer -v` → all PASS.
- [ ] **Step 5: Run the whole package** — `go test -race ./internal/hub` → ok.
- [ ] **Step 6: Commit** — `git add internal/hub/transfer.go internal/hub/transfer_test.go && git commit -m "hub: Transfer, the approved upload and download path" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 7: `Hub.Transfer` under grants, soft lock, and server change

**Files:**
- Test: `internal/hub/transfer_test.go` (code from Task 6; fix it if a test fails)

- [ ] **Step 1: Write the tests**

```go
func TestTransferUnderGrantRunsAuto(t *testing.T) {
	fx, proj := aiFiles(t)
	os.WriteFile(filepath.Join(proj, "f"), []byte("x"), 0o644)
	if err := fx.h.SetAutoAllow("fs", "15m"); err != nil {
		t.Fatal(err)
	}
	ran := make(chan map[string]any, 1)
	release := fx.h.setAutoSink(func(m string, p any) {
		if m == "autoAllow.ran" {
			ran <- p.(map[string]any)
		}
	})
	defer release()
	resp, err := fx.h.Transfer(context.Background(), TransferRequest{Client: "c", Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}})
	if err != nil || resp.Files != 1 || len(fx.h.Broker().Pending()) != 0 {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	rec := fileRecords(t, fx.store)
	if last := rec[len(rec)-1]; last["approval"] != "auto" || last["outcome"] != "allowed" {
		t.Fatalf("audit %v", last)
	}
	if p := <-ran; p["op"] != "upload" || p["command"] != "upload 1 path(s) to /home" {
		t.Fatalf("ran %v", p)
	}
}

func TestTransferUnderSoftLock(t *testing.T) {
	fx, proj := aiFiles(t)
	os.WriteFile(filepath.Join(proj, "f"), []byte("x"), 0o644)
	if err := fx.h.SetAutoAllow("fs", "15m"); err != nil {
		t.Fatal(err)
	}
	fx.h.LockKeepAuto()
	if _, err := fx.h.Transfer(context.Background(), TransferRequest{Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}}); err != nil {
		t.Fatalf("soft lock: %v", err)
	}
	fx.h.Lock() // hard: no grant
	if _, err := fx.h.Transfer(context.Background(), TransferRequest{Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"f"}, Remote: []string{"/home"}}); !errors.Is(err, ErrLocked) {
		t.Fatalf("hard lock: %v", err)
	}
}

func TestTransferEndedByServerSave(t *testing.T) {
	fx, proj := aiFiles(t)
	os.WriteFile(filepath.Join(proj, "big"), bytes.Repeat([]byte("x"), 1<<20), 0o644)
	if err := fx.h.SetAutoAllow("fs", "15m"); err != nil {
		t.Fatal(err)
	}
	c := make(chan error, 1)
	go func() {
		_, err := fx.h.Transfer(context.Background(), TransferRequest{Server: "fs", Op: files.OpUpload, Root: proj, Local: []string{"big"}, Remote: []string{"/home"}})
		c <- err
	}()
	waitInflight(t, fx.h, "fs", 1)
	// Gate only once the transfer is in the job set: gated earlier, the stall
	// would land in NewSFTP or the plan's Stat, before registration, and
	// endServer would find nothing to end.
	waitFor(t, "the transfer in the job set", func() bool {
		fx.h.files.mu.Lock()
		defer fx.h.files.mu.Unlock()
		for j := range fx.h.files.all {
			if _, ok := j.(*aiTransfer); ok {
				return true
			}
		}
		return false
	})
	gate := make(chan struct{})
	fx.srv.GateSFTP(gate) // each SFTP request now waits for a gate send (TestJobCancelStopsAStalledServer)
	t.Cleanup(func() { close(gate) })
	fx.h.files.endServer("fs", "server changed") // what servers.save does
	select {
	case err := <-c:
		if !errors.Is(err, ErrServerChanged) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(cancelGrace + 5*time.Second):
		t.Fatal("transfer not stopped")
	}
}
```

The 1 MiB upload is enough 32 KiB write requests for the gate to catch one; if the run already finished before the gate, raise the size. `waitFor(t, what, cond) bool` is the hub tests' existing poll helper (see `stdin_test.go`).

- [ ] **Step 2: Run** — `go test -race ./internal/hub -run 'TestTransfer' -v`. Expected PASS; on a failure, fix `transfer.go`, not the test's intent.
- [ ] **Step 3: Commit** — `git commit -am "hub: transfer tests for grants, soft lock, server change" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 8: MCP door `upload` and `download`; CLI approver; protocol 13

**Files:**
- Modify: `internal/hub/mcpdoor.go`, `internal/hub/cliui.go`, `internal/hub/idle.go`
- Test: `internal/hub/mcpdoor_test.go`, `internal/hub/cliui_test.go`

**Interfaces:**
- Produces door methods `upload` and `download`, params `{requestId?, client, server, root, local, remote, overwrite?, description?, timeoutSec?}` (strict), result `TransferResponse`.

- [ ] **Step 1: Write the failing tests**
  - mcpdoor_test.go: mirror its existing exec-over-door test (dial the door, call a method). Call `download` with an unknown key `{"server":"x","bogus":1}` → `-32602`; call `upload` with valid params on a hub with no vault → the `ErrNoVault` message.
  - cliui_test.go, mirroring `TestCLIApproverShowsStdinAndRefusesSendToTab`:

```go
func TestCLIApproverShowsTransferAndRefusesSendToTab(t *testing.T) {
	// set up exactly as TestCLIApproverShowsStdinAndRefusesSendToTab, but submit
	// broker.Request{Server: "vis", Command: "upload 1 path(s) to /srv", Transfer: &broker.Transfer{Op: "upload", Root: "/p", Local: []string{"a"}, Remote: []string{"/srv"}, Overwrite: true}}
	// straight to h.Broker().Submit on a goroutine, then:
	waitOut(t, out, "upload from /p:\n  a\nto /srv\noverwrite: yes\n")
	// type "s 1\n"
	waitOut(t, out, "send to tab is not available for a file transfer")
}
```

- [ ] **Step 2: Run, expect FAIL.**

- [ ] **Step 3: Implement**
  - `idle.go`: `const ProtocolVersion = 13`; grep `internal/hub` tests for a literal `12` tied to the protocol and update it.
  - `mcpdoor.go`: move the requestId bookkeeping in `exec` into a closure in `serveMCPConn`, `track(ctx context.Context, id string) (reqCtx context.Context, release func(), err error)`, used by both exec and the new methods (same duplicate, early-cancel behaviour). Then:

```go
	transfer := func(op files.Op) rpc.Handler {
		return func(ctx context.Context, raw json.RawMessage) (any, error) {
			var p struct {
				RequestID   string   `json:"requestId"`
				Client      string   `json:"client"`
				Server      string   `json:"server"`
				Root        string   `json:"root"`
				Local       []string `json:"local"`
				Remote      []string `json:"remote"`
				Overwrite   bool     `json:"overwrite"`
				Description string   `json:"description"`
				TimeoutSec  int      `json:"timeoutSec"`
			}
			if err := strictParams(raw, &p, "requestId", "client", "server", "root", "local", "remote", "overwrite", "description", "timeoutSec"); err != nil {
				return nil, err
			}
			reqCtx, release, err := track(ctx, p.RequestID)
			if err != nil {
				return nil, err
			}
			defer release()
			reload()
			return h.Transfer(reqCtx, TransferRequest{Client: p.Client, Server: p.Server, Op: op, Root: p.Root, Local: p.Local, Remote: p.Remote,
				Overwrite: p.Overwrite, Description: p.Description, TimeoutSec: p.TimeoutSec})
		}
	}
	s.HandleRequest("upload", transfer(files.OpUpload))
	s.HandleRequest("download", transfer(files.OpDownload))
```

  (`strictParams` refuses unknown and duplicate keys and allows absent ones.)
  - `cliui.go` `printRequest`: replace the `command:` line, when `r.Transfer != nil`, with

```go
	if tr := r.Transfer; tr != nil {
		src, dst := tr.Local, tr.Remote
		if tr.Op == "download" {
			src, dst = tr.Remote, tr.Local
		}
		fmt.Fprintf(out, "%s from %s:\n", tr.Op, map[bool]string{true: tr.Root, false: r.Server}[tr.Op == "upload"])
		for _, s := range src {
			fmt.Fprintf(out, "  %s\n", s)
		}
		fmt.Fprintf(out, "to %s\n", dst[0])
		if tr.Overwrite {
			fmt.Fprintln(out, "overwrite: yes")
		}
	} else {
		fmt.Fprintf(out, "command: %s\n", r.Command)
	}
```

  and in the `s` case: `if r.Transfer != nil { fmt.Fprintln(out, broker.ErrSendToTabTransfer); break }` beside the stdin check.
- [ ] **Step 4: Run, expect PASS** — `go test -race ./internal/hub`.
- [ ] **Step 5: Commit** — `git commit -am "hub: upload and download on the MCP door; CLI approver; protocol 13" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 9: Bridge tools

**Files:**
- Modify: `internal/mcpserver/bridge.go`
- Test: `internal/mcpserver/bridge_test.go`

**Interfaces:**
- Consumes: door methods from Task 8.
- Produces: tools `upload` (`bridgeUploadInput`) and `download` (`bridgeDownloadInput`); `func layoutTransfer(op string, r transferOut) string`.

- [ ] **Step 1: Write the failing tests**

```go
func TestBridgeUploadSendsRootAndPaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	dial, got := doorRecorder("upload")
	res := callTool(t, BuildBridgeServer(dial), "upload", map[string]any{"server": "vis", "local": []any{"dist"}, "remote": "/srv/app", "overwrite": true})
	if res.IsError {
		t.Fatal(text(res))
	}
	c := <-got
	p := c[1].(map[string]any)
	wd, _ := os.Getwd()
	if c[0] != "upload" || p["root"] != wd || p["remote"].([]any)[0] != "/srv/app" || p["local"].([]any)[0] != "dist" || p["overwrite"] != true {
		t.Fatalf("door call %v", c)
	}
}

func TestLayoutTransfer(t *testing.T) {
	got := layoutTransfer("download", transferOut{Files: 2, Bytes: 10, Skipped: 1, SkippedNames: []string{"a"},
		RefusedCount: 1, Refused: []transferRefusal{{Path: "app/.env", Kinds: map[string]int{"password": 1}}}, ErrorCount: 1, Errors: []string{"x: permission denied"}})
	want := "downloaded 2 files, 10 bytes\n" +
		"skipped 1 existing file (not overwritten): a\n" +
		"refused 1 file holding a recognised secret (not saved):\n  app/.env (password ×1)\n" +
		"1 error:\n  x: permission denied\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestTransferToolsOnlyOnBridge(t *testing.T) {
	if s := inputSchemas(t, BuildBridgeServer(nil)); s["upload"] == "" || s["download"] == "" {
		t.Fatal("bridge lacks transfer tools")
	}
	// and BuildServer (--host mode) has neither: mirror how tools_test.go builds one
}
```

`doorRecorder` returns an exec-shaped result; `layoutTransfer` must cope with zero fields.

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement**

```go
type bridgeUploadInput struct {
	Server      string   `json:"server" jsonschema:"connection name from list-servers"`
	Local       []string `json:"local" jsonschema:"files or folders to send, relative to your project folder (the working directory this tool runs in); at most 100"`
	Remote      string   `json:"remote" jsonschema:"existing remote folder to copy into, absolute path"`
	Overwrite   bool     `json:"overwrite,omitempty" jsonschema:"replace remote files that already exist; default false skips them"`
	Description string   `json:"description,omitempty" jsonschema:"one sentence on what the transfer is for; shown to the approver and recorded in the audit log; at most 500 bytes"`
	TimeoutSec  int      `json:"timeoutSec,omitempty" jsonschema:"transfer timeout in seconds, 1-3600, default 600"`
}

type bridgeDownloadInput struct {
	Server      string   `json:"server" jsonschema:"connection name from list-servers"`
	Remote      []string `json:"remote" jsonschema:"remote files or folders to fetch, absolute paths; at most 100"`
	Local       string   `json:"local" jsonschema:"existing folder to copy into, relative to your project folder; . is the project folder itself"`
	Overwrite   bool     `json:"overwrite,omitempty" jsonschema:"replace local files that already exist; default false skips them"`
	Description string   `json:"description,omitempty" jsonschema:"one sentence on what the transfer is for; shown to the approver and recorded in the audit log; at most 500 bytes"`
	TimeoutSec  int      `json:"timeoutSec,omitempty" jsonschema:"transfer timeout in seconds, 1-3600, default 600"`
}

type transferRefusal struct {
	Path  string         `json:"path"`
	Kinds map[string]int `json:"kinds"`
}

type transferOut struct {
	Files        int               `json:"files"`
	Bytes        int64             `json:"bytes"`
	Skipped      int               `json:"skipped"`
	SkippedNames []string          `json:"skippedNames"`
	Refused      []transferRefusal `json:"refused"`
	RefusedCount int               `json:"refusedCount"`
	Errors       []string          `json:"errors"`
	ErrorCount   int               `json:"errorCount"`
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// layoutTransfer is what the AI reads after a transfer ran.
func layoutTransfer(op string, r transferOut) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%sed %d %s, %d bytes\n", op, r.Files, plural(r.Files, "file", "files"), r.Bytes)
	if r.Skipped > 0 {
		fmt.Fprintf(&b, "skipped %d existing %s (not overwritten): %s\n", r.Skipped, plural(r.Skipped, "file", "files"), strings.Join(r.SkippedNames, ", "))
	}
	if r.RefusedCount > 0 {
		fmt.Fprintf(&b, "refused %d %s holding a recognised secret (not saved):\n", r.RefusedCount, plural(r.RefusedCount, "file", "files"))
		for _, f := range r.Refused {
			var kinds []string
			for _, k := range config.RedactKinds {
				if n := f.Kinds[k]; n > 0 {
					kinds = append(kinds, fmt.Sprintf("%s ×%d", k, n))
				}
			}
			fmt.Fprintf(&b, "  %s (%s)\n", f.Path, strings.Join(kinds, ", "))
		}
	}
	if r.ErrorCount > 0 {
		fmt.Fprintf(&b, "%d %s:\n", r.ErrorCount, plural(r.ErrorCount, "error", "errors"))
		for _, e := range r.Errors {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}
	return b.String()
}
```

("uploaded"/"downloaded": `op+"ed"` gives "uploaded"/"downloaded"; check.) Import `config` for `RedactKinds` if bridge.go does not already (it must not import anything that holds secrets; `config.RedactKinds` is a plain slice).

The tool handler: copy `execTool`'s structure (dial, `randID`, progress ticker with message "waiting for approval in the app", `cancel` on ctx error, `hubCallErr`), with params

```go
wd, err := os.Getwd()
if err != nil {
	return textErr("cannot read the project folder: " + err.Error()), nil
}
params := map[string]any{"requestId": id, "client": clientName(req), "server": in.Server, "root": wd,
	"local": in.Local, "remote": []string{in.Remote}, "overwrite": in.Overwrite, "description": in.Description, "timeoutSec": in.TimeoutSec}
```

(for download: `"remote": in.Remote, "local": []string{in.Local}`), result decoded into `transferOut`, returned as `textOK(layoutTransfer(op, out))`. Extract the ticker and cancel parts of `execTool` into a shared `callHub(ctx, req, c, method, params, out any) *mcp.CallToolResult` (nil on success) so both tools use one copy; `-32601` from an older hub → `textErr("The running sshgate app is older than this bridge and does not support file transfer; restart the app")`.

Tool descriptions:
- upload: "Copy files or folders from your project folder (this tool's working directory) into an existing folder on a saved SSH server, through the sshgate desktop app. Binary files and folders are fine. Each call waits for a human to approve it, unless the server has auto-allow on. Existing remote files are skipped unless overwrite is true. Symlinks you name are refused; symlinks inside folders are skipped. Not available on servers configured with a su password. For a small text file, exec with stdin (`cat > path`) also works."
- download: "Copy files or folders from a saved SSH server into an existing folder inside your project folder, through the sshgate desktop app. Approval, auto-allow and overwrite rules are as for upload. Each downloaded file is checked for recognised secrets (private keys, passwords, tokens, the server's saved secrets); a file holding one is not saved and is listed as refused with its kinds. Not available on servers configured with a su password."

- [ ] **Step 4: Run, expect PASS** — `go test -race ./internal/mcpserver`.
- [ ] **Step 5: Commit** — `git commit -am "bridge: upload and download tools" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 10: End to end

**Files:** Modify `cmd/sshgate/e2e_test.go` (`TestEndToEndAutoAllow`)

- [ ] **Step 1: Extend the test.** After `srv := sshtest.Start(t)`: `sftpRoot := t.TempDir(); os.MkdirAll(filepath.Join(sftpRoot, "home"), 0o755); srv.ServeSFTP(sftpRoot)`. Make a project folder `proj := t.TempDir()` with `proj/dist/app.bin` (bytes `0..255`) and `proj/back/`, and start the bridge with `cmd := exec.Command(bin); cmd.Dir = proj`. At the end:

```go
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "upload", Arguments: map[string]any{"server": "box", "local": []any{"dist"}, "remote": "/home"}})
	if err != nil || res.IsError {
		t.Fatalf("upload: %v %+v", err, res)
	}
	if txt := res.Content[0].(*mcp.TextContent).Text; !strings.HasPrefix(txt, "uploaded 1 file, 256 bytes\n") {
		t.Fatalf("upload: %q", txt)
	}
	os.WriteFile(filepath.Join(sftpRoot, "home", "dist", ".env"), []byte("password=hunter2x\n"), 0o600)
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "download", Arguments: map[string]any{"server": "box", "remote": []any{"/home/dist"}, "local": "back"}})
	if err != nil || res.IsError {
		t.Fatalf("download: %v %+v", err, res)
	}
	txt = res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(txt, "refused 1 file") || strings.Contains(txt, "hunter2x") {
		t.Fatalf("download: %q", txt)
	}
	if b, _ := os.ReadFile(filepath.Join(proj, "back", "dist", "app.bin")); len(b) != 256 {
		t.Fatalf("round trip: %d bytes", len(b))
	}
	if _, err := os.Stat(filepath.Join(proj, "back", "dist", ".env")); !os.IsNotExist(err) {
		t.Fatal(".env downloaded")
	}
```

The `/home` path assumes `ServeSFTP(root)` maps `/` to `root` (as `filesHub` and `TestTransferUploadApproved` do); confirm against `sshtest`. The test's `stdin` audit check reads the last audit line: move it above the new calls.

- [ ] **Step 2: Run** — `go test -race ./cmd/sshgate -run TestEndToEndAutoAllow -v` → PASS.
- [ ] **Step 3: Commit** — `git commit -am "e2e: upload and download through the bridge" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 11: App: approval card, feed row, audit row, protocol 13

**Files:**
- Modify: `app/hubapi/types.go` (`Request`, `AutoRan`, package comment "protocol 13"), `app/hubproc/proc.go` (`Protocol = 13`), `app/view/approvals.go` (`buildItem`, `buildFeed`), `app/model/audit.go` (`AuditRowView` file case)
- Test: `app/view/approvals_test.go`, `app/model/audit_test.go`

**Interfaces:**

```go
// hubapi
type Transfer struct {
	Op        string   `json:"op"`
	Root      string   `json:"root"`
	Local     []string `json:"local"`
	Remote    []string `json:"remote"`
	Overwrite bool     `json:"overwrite,omitempty"`
}
// Request gains: Transfer *Transfer `json:"transfer,omitempty"`
// AutoRan gains: Op string `json:"op,omitempty"`
```

- [ ] **Step 1: Write the failing tests**

approvals_test.go:

```go
func TestTransferRequestShowsPathsAndDisablesSendToTab(t *testing.T) {
	a, tt, c, d := colApp(t)
	r := reqN("tr", "box", "upload 2 path(s) to /srv/app")
	r.Transfer = &hubapi.Transfer{Op: "upload", Root: "/Users/me/proj", Local: []string{"dist", "README.md"}, Remote: []string{"/srv/app"}, Overwrite: true}
	note(t, a, tt, "pending", map[string]any{"request": r})
	for _, s := range []string{"UPLOAD", "from /Users/me/proj", "dist", "README.md", "to /srv/app", "overwrites existing files"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	c.add(time.Second)
	tt.Frame()
	a.Dial = (&dialRec{}).dial
	_ = tt.Click("Send to tab")
	tt.flush()
	if decided(d) != "" || len(a.tabs.Tabs) != 0 {
		t.Fatal("Send to tab must stay disabled for a transfer")
	}
}
```

audit_test.go (model):

```go
func TestAuditRowAITransfer(t *testing.T) {
	r := AuditRecord{"kind": "file", "action": "download", "server": "box", "remote": []any{"/srv/app"}, "client": "c",
		"outcome": "allowed", "approval": "auto", "refused": json.Number("2")}
	v := AuditRowView(r, time.Now())
	want := []Badge{{"download", "plain"}, {"auto", "auto"}}
	if !reflect.DeepEqual(v.Badges, want) || !slices.Contains(v.Side, "2 refused") {
		t.Fatalf("view %+v", v)
	}
	r["outcome"], r["approval"] = "denied", nil
	if v := AuditRowView(r, time.Now()); !v.Alert || v.Badges[1] != (Badge{"denied", "danger"}) {
		t.Fatalf("view %+v", v)
	}
}
```

(Check the `AuditRecord` constructor and number type the model tests already use, and `r.num`.)

- [ ] **Step 2: Run, expect FAIL** — `(cd app && go test ./model -run TestAuditRowAITransfer && go test ./view -run TestTransferRequest)`.

- [ ] **Step 3: Implement**
  - hubapi types and protocol constant.
  - `buildItem`: replace `segments(c, model.HighlightNonASCII(r.Command, ""))` and its summary with:

```go
		if tr := r.Transfer; tr != nil {
			src, dst, from := tr.Local, tr.Remote, "from "+tr.Root
			if tr.Op == "download" {
				src, dst, from = tr.Remote, tr.Local, "from the server"
			}
			ui.Text(c, strings.ToUpper(tr.Op)).Bold()
			ui.Text(c, model.DisplayText(from)).Font("monospace").TextColor(t.TextMuted)
			for _, s := range src {
				segments(c, model.HighlightNonASCII(s, ""))
			}
			to := "to " + dst[0]
			if tr.Op == "download" {
				to = "to " + filepath.Join(tr.Root, dst[0])
			}
			ui.Text(c, model.DisplayText(to)).Font("monospace")
			if tr.Overwrite {
				ui.Text(c, "overwrites existing files").TextColor(t.Warning)
			}
		} else {
			segments(c, model.HighlightNonASCII(r.Command, ""))
			if s := model.NonASCIISummary(r.Command, ""); s != "" {
				ui.Text(c, s).TextColor(t.Warning)
			}
		}
```

    and `sendOK := r.Stdin == "" && r.Transfer == nil && …`, with the tooltip branch `if r.Stdin != "" || r.Transfer != nil` and tooltip text `"Not available: the request has stdin or is a file transfer"`. Use `filepath` from `path/filepath` (macOS app; local path).
  - `buildFeed` status: `if r.Op != "" && r.Error == "" { status, col = "done", t.Success }` after the exit-code block.
  - `model/audit.go` file case: after `v.Badges = []Badge{{r.str("action"), "plain"}}`:

```go
		if o := r.str("outcome"); o != "" { // an AI transfer (spec 2026-11-02)
			auto := r["approval"] == "auto"
			if auto {
				v.Badges = append(v.Badges, Badge{"auto", "auto"})
			}
			if !auto || o != "allowed" {
				b, ok := execBadge[o]
				if !ok {
					b = Badge{o, "plain"}
				}
				v.Badges = append(v.Badges, b)
			}
			if n, ok := r.num("refused"); ok && n > 0 {
				v.Side = append(v.Side, fmt.Sprintf("%d refused", int(n)))
			}
			if w, ok := r.num("waitMs"); ok && w > 0 {
				v.Side = append(v.Side, "wait "+formatWait(w))
			}
			v.Alert = o == "denied" || o == "error"
		}
```

- [ ] **Step 4: Run** — `(cd app && go vet ./... && go test ./model ./hubapi ./hubproc && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race ./view -run 'TestTransferRequest|TestStdinRequest|TestItemEscapes')` → PASS. Grep `app/` tests for a literal protocol `12` and update it.
- [ ] **Step 5: Commit** — `git commit -am "app: transfer approval card, feed and audit rows; protocol 13" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 12: Docs and full verification

**Files:** `CLAUDE.md`, `README.md`, `docs/superpowers/ROADMAP.md`, the spec's Status line

- [ ] **Step 1: CLAUDE.md**:
  - "What this is": the bridge forwards `exec`, `sudo-exec`, `list-servers`, `upload`, `download` (standalone `--host` keeps three).
  - MCP-door method list: add `upload`/`download` (request-only, strict params, `requestId` cancel).
  - UI door: protocol 13; `pending` carries `transfer`; `decide sent_to_tab` refused with `ErrSendToTabTransfer`.
  - A new bullet "AI transfers (`transfer.go`)": `Hub.Transfer` order, `root` checks and what they are worth, su refusal, grant cap shared, `ScanSecrets` on downloads, one `kind: "file"` record with `client`/`outcome`, `aiTransfer` in the job set.
- [ ] **Step 2: README.md**: one paragraph on the two tools in the bridge section.
- [ ] **Step 3: ROADMAP row 8**: "Built 2026-11-02 (PR #N); exit gate pending a hand check on a real host". Spec Status: add "Implemented (plan `plans/2026-11-02-mcp-file-transfer.md`)".
- [ ] **Step 4: Full verification**, fresh, recording exit codes:

```bash
go vet ./... ; echo vet=$?
go test -race ./... ; echo test=$?
go test -race -v ./... 2>&1 | grep -- '--- SKIP' ; echo
(cd app && go vet ./... ; echo appvet=$?)
(cd app && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race ./... ; echo apptest=$?)
(cd app && ./scripts/package-mac.sh ; echo package=$?)
```

  Expected: every status 0; the only SKIPs are `TestLiveImportConnect`, `TestLiveRedact`, `TestLiveVaultConnect` (root) and `TestLiveFiles`, `TestLiveTunnels` (app).
- [ ] **Step 5: Commit** — `git commit -am "docs: upload and download MCP tools" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`
- [ ] **Step 6: Push and open the PR** with the Step 4 output as evidence and the exit gate listed as not yet done (hand check on a real host). `gh pr create -R lang315/sshgate`, body ending with "🤖 Generated with [Claude Code](https://claude.com/claude-code)". Do not merge until the author says so.
