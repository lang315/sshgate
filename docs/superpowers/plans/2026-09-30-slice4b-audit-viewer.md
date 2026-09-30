# Slice 4b: Audit Viewer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An **Audit** tab in the desktop app that reads `audit.jsonl` through a new UI-door method `audit.read`, filters and pages it, and updates live from a new `audit.appended` notification.

**Architecture:** `broker.Audit` gains a line counter (a record's `seq` is its 1-based line number), an `OnAppend` callback, and a whole-file `Read(ReadQuery)`. The hub serves `audit.read` on the UI door (strict params, unlocked vault only) and forwards every appended record as `audit.appended` while unlocked, through lock-free state because the callback can run with `h.mu` held. The renderer holds the list in a pure module (`audit.ts`) and a fixed tab component (`AuditView.tsx`) that calls the hub only on real UI actions.

**Tech Stack:** Go 1.26 (`internal/broker`, `internal/hub`, `internal/rpc`), Electron + React 19 + TypeScript (`desktop/`), vitest, Playwright.

**Spec:** `docs/superpowers/specs/2026-09-30-slice4b-audit-viewer-design.md` (binding). It depends on slice 4a (`docs/superpowers/specs/2026-09-30-slice4a-output-redaction-design.md`), which is implemented before this plan runs: exec audit records then carry an optional `redacted` map (`map[string]int`, JSON `redacted`, omitted when empty). Treat it as existing.

## Global Constraints

- No new dependencies, Go or npm.
- Protocol **8** in all three places, in one commit (Task 2): `ProtocolVersion` in `internal/hub/idle.go`, `PROTOCOL_VERSION` in `desktop/src/shared/protocol.ts`, and the `hello` reply in `desktop/test/fixtures/fakeHub.mjs`.
- Nothing is added to the MCP door. The AI never reads the audit log.
- Records are returned as stored; no new redaction.
- The renderer never calls the hub in response to `audit.appended`, `locked`, or a timer. The only timer is the search box's 300 ms debounce, which follows typing. Every hub call goes through `desktop/src/renderer/transport.ts`.
- UI conventions: CSS tokens only (no raw colours) in `styles.css`, system fonts, existing `.btn`/`.chip`/`.icon`/`.select` classes, no new keyboard shortcuts (Enter on a focused row toggles it, which the spec asks for). Server-sourced text goes through `displayText`.
- Commits end with these two trailer lines (use your own model name in the first if you are not Claude Opus 5.5):
  ```
  Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
  ```
- Never stage `.DS_Store` or `.idea/`. Stage files by name, never `git add -A` or `git add .`.
- Stay on branch `feat/slice4a-redaction`. Do not push.
- Checks before each commit that touches the area: `go vet ./...`; `go test -race -timeout 900s ./...` (Docker integration tests skip themselves without Docker); `cd desktop && npm run typecheck && npm test`; `cd desktop && npm run e2e` for Task 4 (needs a display; `xvfb-run -a` on Linux).

## Spec ambiguities resolved here

These are binding for the tasks below.

1. **Footer path.** The spec's footer shows the audit file's path, but `audit.read` returns only `{records, next?, skipped}` and `status` has only `storePath`. `broker.Audit` keeps its path, and the result gains `path`: `{records, next?, skipped, path}`.
2. **Kinds × outcomes.** Chips are a union ("several can be on"). With AND semantics, Auto + Config could not be expressed. So: a record passes if its kind is in `kinds`, or it is an exec record whose outcome is in `outcomes`; both empty passes every record; `server` and `text` then narrow the result (AND).
3. **`cancelled`.** No audit record has `outcome: "cancelled"`. The real values are `approved_but_cancelled` (`hub.go` Exec) and `cancelled_running` (`hub.go` run). `cancelled` matches those two. Badges also cover `sent_to_tab` ("sent to tab"), which the spec's badge list leaves out.
4. **Text search and Go's JSON escaping.** `json.Marshal` writes `<`, `>`, `&` as `<`, `>`, `&`, so a raw-line match on `2>&1` would find nothing. The broker decodes those three escapes before a case-insensitive substring match. The renderer mirrors this with `JSON.stringify` for live records.
5. **`skipped`** counts malformed lines in the whole file, not just the page, so the footer's number stays the same while paging. A line is malformed unless it starts with `{` and unmarshals into the filter fields (so `null`, arrays, and wrong field types are skipped, and every returned `record` is valid JSON).
6. **A torn last line** (a crash mid-write) is ended with `\n` by `OpenAudit`, so it counts as one malformed line and the next record starts on its own line.
7. **Limit bounds.** The hub refuses `limit` < 0 or > 500 and `before` < 0 with -32602; `limit` 0 (or omitted) is 200. `broker.Read` itself clamps (0 → 200, > 500 → 500).
8. **"Save a host edit" in e2e.** The Host editor's Save that arms auto-allow writes a `save` config record, so the e2e asserts that row instead of making a second edit.

## File map

| File | Change |
|---|---|
| `internal/broker/audit.go` | `O_RDWR`, line counter, `OnAppend`, `ReadQuery`/`Entry`/`ReadResult`, `Read` |
| `internal/broker/audit_test.go` | counter, callback, filters, paging, limits, malformed lines, race |
| `internal/hub/audit.go` (new) | `auditAppended`, `setAuditSink`, `auditQuery` (params), `ReadAudit` |
| `internal/hub/hub.go` | `unlocked atomic.Bool`, `auditSink atomic.Pointer`, `OnAppend` wiring in `New`, flag in `Unlock`/`zeroKeyLocked` |
| `internal/hub/hosts.go` | flag in `CreateVault` |
| `internal/hub/uidoor.go` | `audit.read` request, `audit.appended` sink |
| `internal/hub/idle.go` | `ProtocolVersion = 8` |
| `internal/hub/audit_test.go` (new) | door tests |
| `internal/hub/hub_test.go`, `uidoor_test.go` | `unlockForTest` sets the flag; `startUI`/`startUIRaw` drop `audit.appended`; hello wants 8 |
| `desktop/src/shared/protocol.ts` | protocol 8 (Task 2); audit types, `HubEvent`, `REQUEST_METHODS` (Task 3) |
| `desktop/test/fixtures/fakeHub.mjs` | hello returns 8 |
| `desktop/src/renderer/transport.ts` | `hub.auditRead` |
| `desktop/src/renderer/audit.ts` (new) | chips → query, `matches`, `rowView`, list state functions |
| `desktop/src/renderer/terminals.ts` | `AUDIT_TAB`, `TabSet.showAudit` |
| `desktop/src/renderer/AuditView.tsx` (new) | the tab |
| `desktop/src/renderer/TerminalTabs.tsx`, `App.tsx`, `icons.tsx`, `styles.css` | tab button, view, `ready` prop, `ListIcon`, CSS |
| `desktop/test/audit.test.ts` (new), `terminals.test.ts`, `transport.test.ts` | vitest |
| `desktop/e2e/audit.spec.ts` (new) | e2e |
| `README.md`, `PRODUCT.md`, `CLAUDE.md`, `docs/superpowers/ROADMAP.md` | docs |

---

### Task 1: `broker.Audit` line counter, `OnAppend`, and `Read`

**Files:**
- Modify: `internal/broker/audit.go` (imports; everything from `// Audit appends one JSON object per line.` to the end of the file)
- Test: `internal/broker/audit_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces (Task 2 relies on these exact names):
  ```go
  const DefaultReadLimit = 200
  const MaxReadLimit = 500
  type ReadQuery struct {
      Before   int      `json:"before"`
      Limit    int      `json:"limit"`
      Server   string   `json:"server"`
      Kinds    []string `json:"kinds"`
      Outcomes []string `json:"outcomes"`
      Text     string   `json:"text"`
  }
  type Entry struct {
      Seq    int             `json:"seq"`
      Record json.RawMessage `json:"record"`
  }
  type ReadResult struct {
      Records []Entry `json:"records"`
      Next    int     `json:"next,omitempty"`
      Skipped int     `json:"skipped"`
      Path    string  `json:"path"`
  }
  func (a *Audit) OnAppend(fn func(seq int, line json.RawMessage))
  func (a *Audit) Read(q ReadQuery) (ReadResult, error)
  ```

- [ ] **Step 1: Write the failing tests**

In `internal/broker/audit_test.go`, replace the import block with:

```go
import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)
```

Append to the end of the file:

```go
func openTestAudit(t *testing.T) (*Audit, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, path
}

func seqsOf(r ReadResult) []int {
	out := []int{}
	for _, e := range r.Records {
		out = append(out, e.Seq)
	}
	return out
}

func TestAuditSeqCountsLinesAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := a.Write(AuditRecord{Command: "ls", Outcome: "allowed"}); err != nil {
			t.Fatal(err)
		}
	}
	a.Close()
	a, err = OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var got []int
	a.OnAppend(func(seq int, _ json.RawMessage) { got = append(got, seq) })
	if err := a.WriteConfig(ConfigRecord{Action: "save"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []int{4}) {
		t.Fatalf("seqs after reopen = %v, want [4]", got)
	}
}

// A line cut short by a crash is ended at open: it stays one malformed line,
// and the next record starts on its own line.
func TestOpenAuditEndsACutLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(path, []byte(`{"command":"ok","outcome":"allowed"}`+"\n"+`{"comm`), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var seq int
	a.OnAppend(func(s int, _ json.RawMessage) { seq = s })
	if err := a.Write(AuditRecord{Command: "after", Outcome: "allowed"}); err != nil {
		t.Fatal(err)
	}
	if seq != 3 {
		t.Fatalf("seq = %d, want 3", seq)
	}
	res, err := a.Read(ReadQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || !slices.Equal(seqsOf(res), []int{3, 1}) {
		t.Fatalf("skipped %d, seqs %v; want 1, [3 1]", res.Skipped, seqsOf(res))
	}
}

func TestAuditOnAppendOncePerRecord(t *testing.T) {
	a, _ := openTestAudit(t)
	type call struct {
		seq  int
		line string
	}
	var calls []call
	a.OnAppend(func(seq int, line json.RawMessage) { calls = append(calls, call{seq, string(line)}) })
	a.Write(AuditRecord{Command: "ls", Outcome: "allowed"})
	a.WriteConfig(ConfigRecord{Action: "save"})
	a.WriteFile(FileRecord{Action: "mkdir", Server: "s"})
	a.WriteTunnel(TunnelRecord{Phase: "start", Server: "s"})
	if len(calls) != 4 {
		t.Fatalf("%d calls, want 4", len(calls))
	}
	for i, c := range calls {
		if c.seq != i+1 || !json.Valid([]byte(c.line)) || strings.HasSuffix(c.line, "\n") {
			t.Fatalf("call %d = %+v", i, c)
		}
	}
	if !strings.Contains(calls[3].line, `"kind":"tunnel"`) {
		t.Fatalf("tunnel line %s", calls[3].line)
	}
}

// The callback runs with a.mu released: it may read the log itself.
func TestAuditOnAppendRunsUnlocked(t *testing.T) {
	a, _ := openTestAudit(t)
	a.OnAppend(func(int, json.RawMessage) {
		if _, err := a.Read(ReadQuery{}); err != nil {
			t.Error(err)
		}
	})
	done := make(chan struct{})
	go func() {
		a.Write(AuditRecord{Command: "ls", Outcome: "allowed"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("OnAppend ran with a.mu held")
	}
}

// readFixture writes ten lines, oldest first:
//
//	1 exec vis "ls" allowed           6 exec box "tail 2>&1" cancelled_running
//	2 exec vis "rm x" denied          7 file box upload
//	3 exec box "uptime" allowed auto  8 exec vis "sleep 9" approved_but_cancelled
//	4 config vis save                 9 tunnel box start
//	5 "not json"                      10 exec vis "false" error auto
func readFixture(t *testing.T) *Audit {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	a, err := OpenAudit(path)
	must(err)
	must(a.Write(AuditRecord{Server: "vis", Command: "ls", Outcome: "allowed"}))
	must(a.Write(AuditRecord{Server: "vis", Command: "rm x", Outcome: "denied"}))
	must(a.Write(AuditRecord{Server: "box", Command: "uptime", Outcome: "allowed", Approval: "auto"}))
	must(a.WriteConfig(ConfigRecord{Server: "vis", Action: "save"}))
	a.Close()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	must(err)
	_, err = f.WriteString("not json\n")
	must(err)
	f.Close()
	a, err = OpenAudit(path)
	must(err)
	t.Cleanup(func() { a.Close() })
	must(a.Write(AuditRecord{Server: "box", Command: "tail 2>&1", Outcome: "cancelled_running"}))
	must(a.WriteFile(FileRecord{Server: "box", Action: "upload"}))
	must(a.Write(AuditRecord{Server: "vis", Command: "sleep 9", Outcome: "approved_but_cancelled"}))
	must(a.WriteTunnel(TunnelRecord{Server: "box", Phase: "start"}))
	must(a.Write(AuditRecord{Server: "vis", Command: "false", Outcome: "error", Approval: "auto"}))
	return a
}

func TestAuditReadFilters(t *testing.T) {
	a := readFixture(t)
	for _, c := range []struct {
		name string
		q    ReadQuery
		want []int
	}{
		{"all", ReadQuery{}, []int{10, 9, 8, 7, 6, 4, 3, 2, 1}},
		{"server", ReadQuery{Server: "box"}, []int{9, 7, 6, 3}},
		{"server is exact", ReadQuery{Server: "bo"}, []int{}},
		{"kind exec", ReadQuery{Kinds: []string{"exec"}}, []int{10, 8, 6, 3, 2, 1}},
		{"kind config", ReadQuery{Kinds: []string{"config"}}, []int{4}},
		{"kinds file and tunnel", ReadQuery{Kinds: []string{"file", "tunnel"}}, []int{9, 7}},
		{"auto", ReadQuery{Outcomes: []string{"auto"}}, []int{10, 3}},
		{"allowed is not auto", ReadQuery{Outcomes: []string{"allowed"}}, []int{1}},
		{"denied", ReadQuery{Outcomes: []string{"denied"}}, []int{2}},
		{"cancelled", ReadQuery{Outcomes: []string{"cancelled"}}, []int{8, 6}},
		{"error", ReadQuery{Outcomes: []string{"error"}}, []int{10}},
		{"expired", ReadQuery{Outcomes: []string{"expired"}}, []int{}},
		{"text is case-insensitive", ReadQuery{Text: "UPTIME"}, []int{3}},
		{"text matches what json escaped", ReadQuery{Text: "2>&1"}, []int{6}},
		{"kinds and outcomes are a union", ReadQuery{Kinds: []string{"config"}, Outcomes: []string{"denied"}}, []int{4, 2}},
		{"server narrows the union", ReadQuery{Server: "box", Kinds: []string{"config"}, Outcomes: []string{"auto"}}, []int{3}},
		{"text narrows too", ReadQuery{Server: "vis", Text: "save"}, []int{4}},
	} {
		res, err := a.Read(c.q)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := seqsOf(res); !slices.Equal(got, c.want) {
			t.Errorf("%s: seqs %v, want %v", c.name, got, c.want)
		}
		if res.Skipped != 1 {
			t.Errorf("%s: skipped %d, want 1 (whole file)", c.name, res.Skipped)
		}
		if res.Next != 0 {
			t.Errorf("%s: next %d with nothing older", c.name, res.Next)
		}
	}
}

func TestAuditReadPaging(t *testing.T) {
	a := readFixture(t)
	res, err := a.Read(ReadQuery{Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seqsOf(res), []int{10, 9, 8, 7}) || res.Next != 7 {
		t.Fatalf("page 1: %v next %d", seqsOf(res), res.Next)
	}
	if !strings.Contains(string(res.Records[0].Record), `"command":"false"`) || filepath.Base(res.Path) != "audit.jsonl" {
		t.Fatalf("record %s, path %q", res.Records[0].Record, res.Path)
	}
	res, _ = a.Read(ReadQuery{Limit: 4, Before: res.Next})
	if !slices.Equal(seqsOf(res), []int{6, 4, 3, 2}) || res.Next != 2 {
		t.Fatalf("page 2: %v next %d", seqsOf(res), res.Next)
	}
	res, _ = a.Read(ReadQuery{Limit: 4, Before: res.Next})
	if !slices.Equal(seqsOf(res), []int{1}) || res.Next != 0 {
		t.Fatalf("page 3: %v next %d", seqsOf(res), res.Next)
	}
	// Paging keeps the filter.
	res, _ = a.Read(ReadQuery{Limit: 1, Kinds: []string{"exec"}, Before: 8})
	if !slices.Equal(seqsOf(res), []int{6}) || res.Next != 6 {
		t.Fatalf("filtered page: %v next %d", seqsOf(res), res.Next)
	}
}

func TestAuditReadLimitBounds(t *testing.T) {
	a, _ := openTestAudit(t)
	for i := range 505 {
		if err := a.Write(AuditRecord{Command: fmt.Sprint("c", i), Outcome: "allowed"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ limit, n, next int }{
		{0, DefaultReadLimit, 306}, {1, 1, 505}, {MaxReadLimit, 500, 6}, {10000, 500, 6},
	} {
		res, err := a.Read(ReadQuery{Limit: c.limit})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Records) != c.n || res.Next != c.next {
			t.Errorf("limit %d: %d records, next %d; want %d, %d", c.limit, len(res.Records), res.Next, c.n, c.next)
		}
	}
}

func TestAuditReadSkipsNonObjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(path, []byte("null\n[]\n"+`{"command":"x","outcome":"allowed"}`+"\n"+`{"server":5}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	res, err := a.Read(ReadQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seqsOf(res), []int{3}) || res.Skipped != 3 {
		t.Fatalf("seqs %v skipped %d; want [3], 3", seqsOf(res), res.Skipped)
	}
}

func TestAuditReadEmptyIsAnEmptyList(t *testing.T) {
	a, path := openTestAudit(t)
	res, err := a.Read(ReadQuery{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	pj, _ := json.Marshal(path)
	if want := `{"records":[],"skipped":0,"path":` + string(pj) + `}`; string(b) != want {
		t.Fatalf("got %s, want %s", b, want)
	}
}

// Read holds a.mu while it reads, so it never sees a half-written line, and
// seqs stay contiguous under concurrent appends.
func TestAuditReadDuringAppends(t *testing.T) {
	a, _ := openTestAudit(t)
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 100 {
				a.Write(AuditRecord{Command: fmt.Sprintf("w%d-%d", w, i), Outcome: "allowed"})
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	for reading := true; reading; {
		select {
		case <-done:
			reading = false
		default:
		}
		res, err := a.Read(ReadQuery{Limit: MaxReadLimit})
		if err != nil {
			t.Fatal(err)
		}
		if res.Skipped != 0 {
			t.Fatalf("read saw %d half-written lines", res.Skipped)
		}
		for i, e := range res.Records {
			if e.Seq != len(res.Records)-i {
				t.Fatalf("seq %d at index %d of %d", e.Seq, i, len(res.Records))
			}
		}
	}
	res, _ := a.Read(ReadQuery{Limit: MaxReadLimit})
	if len(res.Records) != 400 {
		t.Fatalf("%d records, want 400", len(res.Records))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -race -timeout 300s ./internal/broker -run 'TestAudit|TestOpenAudit' -v`
Expected: FAIL to build: `a.OnAppend undefined (type *Audit has no field or method OnAppend)`, `undefined: ReadQuery`, `undefined: ReadResult`, `undefined: DefaultReadLimit`.

- [ ] **Step 3: Implement**

In `internal/broker/audit.go`, replace the import block with:

```go
import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"math"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)
```

Replace everything from the line `// Audit appends one JSON object per line. Output content is never part of a` to the end of the file with:

```go
// Audit appends one JSON object per line. Output content is never part of a
// record; only byte counts are. A record's seq is its 1-based line number.
type Audit struct {
	mu       sync.Mutex
	f        *os.File
	path     string
	n        int // lines in the file: the last record's seq
	onAppend func(seq int, line json.RawMessage)
}

func OpenAudit(path string) (*Audit, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	// O_CREATE's mode only applies when the file is newly created; enforce
	// 0600 explicitly so a pre-existing file with looser permissions is
	// tightened too.
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, err
	}
	a := &Audit{f: f, path: path}
	data, err := a.readAll()
	if err != nil {
		f.Close()
		return nil, err
	}
	a.n = bytes.Count(data, []byte{'\n'})
	// A line cut short (a crash mid-write) is ended here: it stays one
	// malformed line, and the next record starts on its own line.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		if _, err := f.Write([]byte{'\n'}); err != nil {
			f.Close()
			return nil, err
		}
		a.n++
	}
	return a, nil
}

// OnAppend sets fn to be called after each record is written, with its seq
// and the line without its newline. It runs on the writer's goroutine with
// a.mu released, and must not block. The hub sets it once.
func (a *Audit) OnAppend(fn func(seq int, line json.RawMessage)) {
	a.mu.Lock()
	a.onAppend = fn
	a.mu.Unlock()
}

func (a *Audit) Write(r AuditRecord) error { return a.append(r) }

func (a *Audit) WriteConfig(r ConfigRecord) error {
	r.Kind = "config"
	return a.append(r)
}

func (a *Audit) WriteFile(r FileRecord) error {
	r.Kind = "file"
	return a.append(r)
}

func (a *Audit) WriteTunnel(r TunnelRecord) error {
	r.Kind = "tunnel"
	return a.append(r)
}

func (a *Audit) append(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	a.mu.Lock()
	_, err = a.f.Write(append(line, '\n'))
	if err == nil {
		a.n++
	}
	seq, fn := a.n, a.onAppend
	a.mu.Unlock()
	if err == nil && fn != nil {
		fn(seq, line)
	}
	return err
}

// readAll reads the whole file through a.f, the file being written to.
func (a *Audit) readAll() ([]byte, error) {
	return io.ReadAll(io.NewSectionReader(a.f, 0, math.MaxInt64))
}

const (
	DefaultReadLimit = 200
	MaxReadLimit     = 500
)

// ReadQuery selects records for Read. Kinds and Outcomes together pick a
// union: a record passes if its kind is in Kinds, or it is an exec record
// whose outcome is in Outcomes; both empty passes every record. Server and
// Text then narrow that.
type ReadQuery struct {
	Before   int      `json:"before"`   // only seq < Before; 0 means from the newest
	Limit    int      `json:"limit"`    // 0 means DefaultReadLimit; capped at MaxReadLimit
	Server   string   `json:"server"`   // an exact server name
	Kinds    []string `json:"kinds"`    // exec, config, file, tunnel
	Outcomes []string `json:"outcomes"` // allowed, auto, denied, expired, cancelled, error
	Text     string   `json:"text"`     // case-insensitive substring of the line
}

type Entry struct {
	Seq    int             `json:"seq"`
	Record json.RawMessage `json:"record"`
}

type ReadResult struct {
	Records []Entry `json:"records"`        // newest first
	Next    int     `json:"next,omitempty"` // the smallest seq returned, when older matches exist
	Skipped int     `json:"skipped"`        // lines in the whole file that did not parse
	Path    string  `json:"path"`
}

// fields are what Read filters on. An exec record has no kind.
type fields struct {
	Kind     string `json:"kind"`
	Server   string `json:"server"`
	Outcome  string `json:"outcome"`
	Approval string `json:"approval"`
}

func (f fields) outcomeIs(o string) bool {
	switch o {
	case "auto":
		return f.Approval == "auto"
	case "allowed":
		return f.Outcome == "allowed" && f.Approval != "auto"
	case "cancelled":
		return f.Outcome == "approved_but_cancelled" || f.Outcome == "cancelled_running"
	}
	return f.Outcome == o
}

// json.Marshal escapes <, > and &; Text matches what a person would type.
var unescapeHTML = strings.NewReplacer(`<`, "<", `>`, ">", `&`, "&")

// match reports whether a parsed line passes q; text is q.Text lowercased.
func (q ReadQuery) match(f fields, line []byte, text string) bool {
	if q.Server != "" && f.Server != q.Server {
		return false
	}
	if text != "" && !strings.Contains(strings.ToLower(unescapeHTML.Replace(string(line))), text) {
		return false
	}
	if len(q.Kinds) == 0 && len(q.Outcomes) == 0 {
		return true
	}
	kind := cmp.Or(f.Kind, "exec")
	if slices.Contains(q.Kinds, kind) {
		return true
	}
	return kind == "exec" && slices.ContainsFunc(q.Outcomes, f.outcomeIs)
}

// Read returns up to q.Limit records matching q, newest first, before
// q.Before. It holds a.mu only while reading, so it never sees a
// half-written line.
// ponytail: whole-file scan on every call. Switch to a backwards reader from
// EOF when the file is large; that is also when rotation is needed.
func (a *Audit) Read(q ReadQuery) (ReadResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	limit = min(limit, MaxReadLimit)
	a.mu.Lock()
	data, err := a.readAll()
	a.mu.Unlock()
	if err != nil {
		return ReadResult{}, err
	}
	res := ReadResult{Records: []Entry{}, Path: a.path}
	text := strings.ToLower(q.Text)
	lines := bytes.Split(data, []byte{'\n'})
	lines = lines[:len(lines)-1] // what follows the last '\n' is not a line yet
	more := false
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		var f fields
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &f) != nil {
			res.Skipped++
			continue
		}
		seq := i + 1
		if q.Before > 0 && seq >= q.Before || !q.match(f, line, text) {
			continue
		}
		if len(res.Records) == limit {
			more = true
			continue
		}
		res.Records = append(res.Records, Entry{Seq: seq, Record: line})
	}
	if more {
		res.Next = res.Records[len(res.Records)-1].Seq
	}
	return res, nil
}

func (a *Audit) Close() error { return a.f.Close() }
```

Then run `gofmt -w internal/broker`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race -timeout 300s ./internal/broker -v`
Expected: PASS, every test including the three pre-existing `TestAudit*`/`TestOpenAudit*` ones.

Run: `go vet ./... && go test -race -timeout 900s ./...`
Expected: PASS (the hub still builds: nothing it uses changed signature).

- [ ] **Step 5: Commit**

```bash
git add internal/broker/audit.go internal/broker/audit_test.go
git commit -F - <<'EOF'
feat(broker): audit line counter, OnAppend and Read

Each record's seq is its 1-based line number, counted at open (a torn
last line is ended there). Read scans the whole file newest first with
server, kind/outcome (a union) and text filters, paging by seq.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
```

---

### Task 2: hub `audit.read`, `audit.appended`, protocol 8

**Files:**
- Create: `internal/hub/audit.go`
- Modify: `internal/hub/hub.go` (imports, `Hub` struct, `New`, `Unlock`, `zeroKeyLocked`)
- Modify: `internal/hub/hosts.go` (`CreateVault`, after `h.deps.MasterKey = mk`)
- Modify: `internal/hub/uidoor.go` (`ServeUIDoor`)
- Modify: `internal/hub/idle.go:5`
- Modify: `internal/hub/hub_test.go:59-63` (`unlockForTest`)
- Modify: `internal/hub/uidoor_test.go` (`startUI`, `startUIRaw`, `TestUIDoorHelloAndLockedNotification`)
- Create: `internal/hub/audit_test.go`
- Modify: `desktop/src/shared/protocol.ts` (last line), `desktop/test/fixtures/fakeHub.mjs`

**Interfaces:**
- Consumes (Task 1): `broker.ReadQuery`, `broker.ReadResult`, `broker.Entry`, `broker.MaxReadLimit`, `(*broker.Audit).OnAppend`, `(*broker.Audit).Read`.
- Produces (Tasks 3-4 rely on the wire format):
  - request `audit.read` with params `{before?, limit?, server?, kinds?, outcomes?, text?}` (strict keys) returning `{records: [{seq, record}], next?, skipped, path}`; `ErrLocked` ("Vault is locked; unlock it in the app") while locked; -32602 for a bad key, `limit` < 0 or > 500, `before` < 0, or an unknown kind/outcome.
  - notification `audit.appended` with params `{seq, record}`, for every record, only while unlocked.
  - `hello` returns `{protocol: 8}`.
  - `func (h *Hub) ReadAudit(q broker.ReadQuery) (broker.ReadResult, error)`.

Why the callback is lock-free: `auditConfig` is called with `h.mu` held by `CreateVault` (`hosts.go`), `SaveServerWithAutoAllow` (`auditGrantEnded`, `armLocked`) and `SetAutoAllow`. `OnAppend` runs on that goroutine, so the callback may not take `h.mu` (no `h.Locked()`, no `h.autoSink`). The unlocked state is mirrored in an `atomic.Bool` wherever `deps.MasterKey` is assigned: `Unlock`, `CreateVault`, `zeroKeyLocked`, and the test helper `unlockForTest`, which bypasses `Unlock`.

Why the test helpers change: `startUI` and `startUIRaw` hand notifications to 16-slot channels that tests read in order (`pending`, `decided`, `locked`). A denied exec now also sends `audit.appended`, which races `decided`, and a full channel would block the client's reader and then the hub's write. Those two helpers drop `audit.appended`. `startTermDoor` (1024 slots, readers skip by method) keeps it, so the new tests use it and the `filesHub`/`tunnelsHub` fixtures built on it. `TestTermOpenWriteInOrderAndClose` (`term_test.go`) fails on any non-`term.data` notification, but its `fakeServerHub` host is pinned, so no audit record is written there.

- [ ] **Step 1: Write the failing tests**

In `internal/hub/hub_test.go`, replace `unlockForTest`'s body:

```go
// unlockForTest installs testMK without a second Argon2 run.
func unlockForTest(h *Hub) {
	h.mu.Lock()
	h.deps.MasterKey = bytes.Clone(testMK)
	h.unlocked.Store(true)
	h.mu.Unlock()
}
```

In `internal/hub/uidoor_test.go`, in `startUI`, replace
`	c := rpc.NewClient(uiR, uiW, func(m string, _ json.RawMessage) { notes <- m })`
with:

```go
	// audit.appended is dropped: it races pending/decided/locked, which these
	// tests read in order, and would fill the channel. Audit tests use
	// startTermDoor, which keeps every notification.
	c := rpc.NewClient(uiR, uiW, func(m string, _ json.RawMessage) {
		if m != "audit.appended" {
			notes <- m
		}
	})
```

In `startUIRaw`, replace
`	c := rpc.NewClient(uiR, uiW, func(m string, p json.RawMessage) { notes <- uiNote{m, p} })`
with:

```go
	// audit.appended is dropped, as in startUI.
	c := rpc.NewClient(uiR, uiW, func(m string, p json.RawMessage) {
		if m != "audit.appended" {
			notes <- uiNote{m, p}
		}
	})
```

In `TestUIDoorHelloAndLockedNotification`, replace

```go
	if hello.Protocol != 7 {
		t.Fatalf("protocol = %d, want 7", hello.Protocol)
	}
```

with:

```go
	if hello.Protocol != 8 {
		t.Fatalf("protocol = %d, want 8", hello.Protocol)
	}
```

Create `internal/hub/audit_test.go`:

```go
package hub

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/rpc"
)

type auditPage struct {
	Records []struct {
		Seq    int            `json:"seq"`
		Record map[string]any `json:"record"`
	} `json:"records"`
	Next    int    `json:"next"`
	Skipped int    `json:"skipped"`
	Path    string `json:"path"`
}

// waitAudit returns the next audit.appended whose record has kind (exec
// records have none), skipping every other notification.
func waitAudit(t *testing.T, notes chan note, kind string) (int, map[string]any) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case n := <-notes:
			if n.method != "audit.appended" {
				continue
			}
			var p struct {
				Seq    int            `json:"seq"`
				Record map[string]any `json:"record"`
			}
			if err := json.Unmarshal(n.params, &p); err != nil {
				t.Fatal(err)
			}
			if k, _ := p.Record["kind"].(string); cmp.Or(k, "exec") == kind {
				return p.Seq, p.Record
			}
		case <-deadline:
			t.Fatalf("no audit.appended of kind %s", kind)
		}
	}
}

func TestAuditReadLockedAndCountsAsActivity(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: -1})
	c, _ := startUIRaw(t, h)
	ctx := context.Background()
	h.mu.Lock()
	h.lastActivity = time.Now().Add(-time.Hour)
	h.mu.Unlock()
	if err := c.Call(ctx, "audit.read", map[string]any{}, nil); err == nil || err.Error() != ErrLocked.Error() {
		t.Fatalf("locked: got %v, want %v", err, ErrLocked)
	}
	h.mu.Lock()
	quiet := time.Since(h.lastActivity)
	h.mu.Unlock()
	if quiet > time.Minute {
		t.Fatal("audit.read did not count as UI activity")
	}
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	var page auditPage
	if err := c.Call(ctx, "audit.read", map[string]any{}, &page); err != nil {
		t.Fatal(err)
	}
	if page.Records == nil || len(page.Records) != 0 || filepath.Base(page.Path) != "audit.jsonl" {
		t.Fatalf("%+v", page)
	}
}

func TestAuditReadStrictParams(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	ctx := context.Background()
	for _, p := range []map[string]any{
		{"limit": 501}, {"limit": -1}, {"before": -1}, {"limit": "5"},
		{"kinds": []string{"bogus"}}, {"outcomes": []string{"sent_to_tab"}},
		{"extra": 1}, {"Limit": 5},
	} {
		err := c.Call(ctx, "audit.read", p, nil)
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != -32602 {
			t.Fatalf("%v: want -32602, got %v", p, err)
		}
	}
	all := map[string]any{"limit": 500, "before": 1, "server": "vis", "text": "x",
		"kinds":    []string{"exec", "config", "file", "tunnel"},
		"outcomes": []string{"allowed", "auto", "denied", "expired", "cancelled", "error"}}
	if err := c.Call(ctx, "audit.read", all, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAuditReadReturnsRecordsNewestFirst(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	ctx := context.Background()
	go decideFirst(t, h.Broker(), broker.Decision{Outcome: broker.Denied, Reason: "no"})
	if _, err := h.Exec(ctx, ExecRequest{Server: "vis", Command: "rm -rf /tmp/x", Description: "clean up"}); err == nil {
		t.Fatal("want denied")
	}
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	var first auditPage
	if err := c.Call(ctx, "audit.read", map[string]any{"limit": 1}, &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Records) != 1 || first.Records[0].Seq != 2 || first.Records[0].Record["action"] != "autoAllowOn" || first.Next != 2 {
		t.Fatalf("page 1: %+v", first)
	}
	var older auditPage
	if err := c.Call(ctx, "audit.read", map[string]any{"before": first.Next, "outcomes": []string{"denied"}}, &older); err != nil {
		t.Fatal(err)
	}
	if len(older.Records) != 1 || older.Records[0].Record["command"] != "rm -rf /tmp/x" ||
		older.Records[0].Record["description"] != "clean up" || older.Next != 0 {
		t.Fatalf("page 2: %+v", older)
	}
}

func TestAuditAppendedExecAndConfig(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	_, _, notes := startTermDoor(t, h)
	go decideFirst(t, h.Broker(), broker.Decision{Outcome: broker.Denied})
	h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	if seq, rec := waitAudit(t, notes, "exec"); seq != 1 || rec["command"] != "ls" || rec["outcome"] != "denied" {
		t.Fatalf("exec: seq %d %v", seq, rec)
	}
	// SetAutoAllow writes its record with h.mu held: the callback must not take it.
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	if seq, rec := waitAudit(t, notes, "config"); seq != 2 || rec["action"] != "autoAllowOn" {
		t.Fatalf("config: seq %d %v", seq, rec)
	}
}

func TestAuditAppendedFileAndTunnel(t *testing.T) {
	fx := tunnelsHub(t)
	ctx := context.Background()
	if err := fx.c.Call(ctx, "files.mkdir", map[string]any{"server": "fs", "path": "/home/d"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, rec := waitAudit(t, fx.notes, "file"); rec["action"] != "mkdir" {
		t.Fatalf("file: %v", rec)
	}
	id := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "dynamic", "listenPort": tunnelFreePort(t)})
	if _, rec := waitAudit(t, fx.notes, "config"); rec["action"] != "tunnelSave" {
		t.Fatalf("config: %v", rec)
	}
	if err := fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": id}, nil); err != nil {
		t.Fatal(err)
	}
	if _, rec := waitAudit(t, fx.notes, "tunnel"); rec["phase"] != "start" || rec["id"] != id {
		t.Fatalf("tunnel: %v", rec)
	}
}

func TestAuditAppendedNotWhileLocked(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	_, _, notes := startTermDoor(t, h)
	h.Lock()
	h.auditConfig(broker.ConfigRecord{Action: "whileLocked"})
	unlockForTest(h)
	h.auditConfig(broker.ConfigRecord{Action: "afterUnlock"})
	if seq, rec := waitAudit(t, notes, "config"); seq != 2 || rec["action"] != "afterUnlock" {
		t.Fatalf("first audit.appended: seq %d %v; the locked write must send nothing", seq, rec)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -race -timeout 300s ./internal/hub -run 'TestAudit|TestUIDoor' -v`
Expected: FAIL to build: `h.unlocked undefined (type *Hub has no field or method unlocked)`.

- [ ] **Step 3: Implement**

Create `internal/hub/audit.go`:

```go
package hub

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/rpc"
)

// auditAppended is the audit log's OnAppend callback. It runs on whichever
// goroutine wrote the record, sometimes with h.mu held (CreateVault,
// SaveServer, SetAutoAllow, a grant ending), so it takes no hub lock: the
// lock state and the sink are atomics. Nothing is sent while locked.
// ponytail: the sink writes to the UI door's pipe synchronously, like every
// other notification; Electron main drains it continuously. If a slow reader
// ever shows, queue on a buffered channel with one drain goroutine.
func (h *Hub) auditAppended(seq int, line json.RawMessage) {
	if !h.unlocked.Load() {
		return
	}
	if f := h.auditSink.Load(); f != nil {
		(*f)(seq, line)
	}
}

// setAuditSink installs f for audit.appended; release clears it only while
// it is still f, like setEventSink's.
func (h *Hub) setAuditSink(f func(seq int, line json.RawMessage)) (release func()) {
	p := &f
	h.auditSink.Store(p)
	return func() { h.auditSink.CompareAndSwap(p, nil) }
}

var (
	auditKinds    = []string{"exec", "config", "file", "tunnel"}
	auditOutcomes = []string{"allowed", "auto", "denied", "expired", "cancelled", "error"}
)

// auditQuery decodes audit.read's params strictly and checks every value.
func auditQuery(raw json.RawMessage) (broker.ReadQuery, error) {
	var q broker.ReadQuery
	if err := strictParams(raw, &q, "before", "limit", "server", "kinds", "outcomes", "text"); err != nil {
		return q, err
	}
	if q.Before < 0 {
		return q, &rpc.Error{Code: -32602, Message: "before must be a seq"}
	}
	if q.Limit < 0 || q.Limit > broker.MaxReadLimit {
		return q, &rpc.Error{Code: -32602, Message: "limit must be 1-" + strconv.Itoa(broker.MaxReadLimit)}
	}
	for _, k := range q.Kinds {
		if !slices.Contains(auditKinds, k) {
			return q, &rpc.Error{Code: -32602, Message: "unknown kind " + strconv.Quote(k)}
		}
	}
	for _, o := range q.Outcomes {
		if !slices.Contains(auditOutcomes, o) {
			return q, &rpc.Error{Code: -32602, Message: "unknown outcome " + strconv.Quote(o)}
		}
	}
	return q, nil
}

// ReadAudit serves audit.read. The log is shown only while the vault is
// unlocked.
func (h *Hub) ReadAudit(q broker.ReadQuery) (broker.ReadResult, error) {
	if !h.unlocked.Load() {
		return broker.ReadResult{}, ErrLocked
	}
	if h.audit == nil {
		return broker.ReadResult{Records: []broker.Entry{}}, nil
	}
	return h.audit.Read(q)
}
```

In `internal/hub/hub.go`:

1. Add `"encoding/json"` and `"sync/atomic"` to the import block.
2. In the `Hub` struct, after `closeOnce    sync.Once`, add:

```go
	unlocked     atomic.Bool                                          // deps.MasterKey != nil, for auditAppended, which must not take h.mu
	auditSink    atomic.Pointer[func(seq int, line json.RawMessage)] // see audit.go
```

3. In `New`, right after the line `h := &Hub{o: o, reg: sshx.NewRegistry(), ...}`, add:

```go
	if o.Audit != nil {
		o.Audit.OnAppend(h.auditAppended)
	}
```

4. In `Unlock`, replace

```go
	clear(h.deps.MasterKey)
	h.deps.MasterKey = mk
	h.lastActivity = time.Now()
	return nil
}
```

with:

```go
	clear(h.deps.MasterKey)
	h.deps.MasterKey = mk
	h.unlocked.Store(true)
	h.lastActivity = time.Now()
	return nil
}
```

5. In `zeroKeyLocked`, replace

```go
	h.deps.MasterKey = nil
	return h.lockSink
```

with:

```go
	h.deps.MasterKey = nil
	h.unlocked.Store(false)
	return h.lockSink
```

In `internal/hub/hosts.go` (`CreateVault`), replace

```go
	h.deps.MasterKey = mk
	h.lastActivity = time.Now()
	h.auditConfig(broker.ConfigRecord{Action: "vaultCreate", KeptServers: kept})
```

with:

```go
	h.deps.MasterKey = mk
	h.unlocked.Store(true)
	h.lastActivity = time.Now()
	h.auditConfig(broker.ConfigRecord{Action: "vaultCreate", KeptServers: kept})
```

In `internal/hub/uidoor.go` (`ServeUIDoor`), after

```go
	releaseAuto := h.setAutoSink(func(method string, params any) { s.Notify(method, params) })
	defer releaseAuto()
```

add:

```go
	releaseAudit := h.setAuditSink(func(seq int, line json.RawMessage) {
		s.Notify("audit.appended", map[string]any{"seq": seq, "record": line})
	})
	defer releaseAudit()
```

and before `req("hello", ...)` add:

```go
	req("audit.read", func(_ context.Context, raw json.RawMessage) (any, error) {
		q, err := auditQuery(raw)
		if err != nil {
			return nil, err
		}
		return h.ReadAudit(q)
	})
```

In `internal/hub/idle.go`, change `const ProtocolVersion = 7` to `const ProtocolVersion = 8`.

In `desktop/src/shared/protocol.ts`, change `export const PROTOCOL_VERSION = 7` to `export const PROTOCOL_VERSION = 8`.

In `desktop/test/fixtures/fakeHub.mjs`, change `result: { protocol: mode === 'badproto' ? 99 : 7 }` to `result: { protocol: mode === 'badproto' ? 99 : 8 }`.

Run `gofmt -w internal/hub`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race -timeout 300s ./internal/hub -run 'TestAudit|TestUIDoor' -v`
Expected: PASS, including the six new `TestAudit*` tests and `TestUIDoorHelloAndLockedNotification` (protocol 8).

Run: `go vet ./... && go test -race -timeout 900s ./...`
Expected: PASS. Already checked: the `startTermDoor` fixtures whose flows write audit records (`trust_test.go`, `import_test.go`, `files_test.go`, `tunnels_test.go`) read notifications only through `waitNote`/`noNote` or loops that skip other methods, so the new notification cannot break them. If a hub test still fails on an unexpected `audit.appended`, make that reader skip it the way `waitNote` skips other methods; do not change the hub.

Run: `cd desktop && npm run typecheck && npm test`
Expected: PASS (`hubProcess.test.ts` handshakes with `fakeHub.mjs` at protocol 8).

- [ ] **Step 5: Commit**

```bash
git add internal/hub/audit.go internal/hub/audit_test.go internal/hub/hub.go internal/hub/hosts.go \
  internal/hub/uidoor.go internal/hub/idle.go internal/hub/hub_test.go internal/hub/uidoor_test.go \
  desktop/src/shared/protocol.ts desktop/test/fixtures/fakeHub.mjs
git commit -F - <<'EOF'
feat(hub): audit.read and audit.appended on the UI door, protocol 8

audit.read (strict params, unlocked only, counts as activity) pages the
audit log with filters. Every record written while unlocked is pushed as
audit.appended; the callback can run under h.mu, so it reads only atomics.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
```

---

### Task 3: desktop audit types, transport, and list logic

**Files:**
- Modify: `desktop/src/shared/protocol.ts` (new types, `HubEvent`, `REQUEST_METHODS`)
- Modify: `desktop/src/renderer/transport.ts` (import line; `hub` object)
- Create: `desktop/src/renderer/audit.ts`
- Modify: `desktop/src/renderer/terminals.ts` (`AUDIT_TAB`, `TabSet.showAudit`)
- Create: `desktop/test/audit.test.ts`
- Modify: `desktop/test/transport.test.ts`, `desktop/test/terminals.test.ts`

**Interfaces:**
- Consumes (Task 2): the `audit.read` and `audit.appended` wire format.
- Produces (Task 4 relies on these):
  ```ts
  // protocol.ts
  export type AuditKind = 'exec' | 'config' | 'file' | 'tunnel'
  export type AuditOutcome = 'allowed' | 'auto' | 'denied' | 'expired' | 'cancelled' | 'error'
  export interface AuditQuery { before?: number; limit?: number; server?: string; kinds?: AuditKind[]; outcomes?: AuditOutcome[]; text?: string }
  export interface AuditRecord { time: string; kind?: 'config' | 'file' | 'tunnel'; server?: string; /* see below */ }
  export interface AuditEntry { seq: number; record: AuditRecord }
  export interface AuditPage { records: AuditEntry[]; next?: number; skipped: number; path: string }
  // HubEvent gains: { method: 'audit.appended'; params: AuditEntry }
  // transport.ts
  hub.auditRead(q: AuditQuery): Promise<AuditPage>
  // terminals.ts
  export const AUDIT_TAB = 'audit'
  TabSet.showAudit(): void
  // audit.ts
  export type Chip = 'exec' | 'auto' | 'denied' | 'config' | 'files' | 'tunnels'
  export const CHIPS: { id: Chip; label: string; kind?: AuditKind; outcome?: AuditOutcome }[]
  export interface Filters { server: string; chips: Chip[]; text: string }
  export const NO_FILTERS: Filters
  export function toggleChip(chips: Chip[], c: Chip): Chip[]
  export function toQuery(f: Filters): AuditQuery
  export function matches(r: AuditRecord, q: AuditQuery): boolean
  export function formatTime(iso: string, now: Date): string
  export type Tone = 'ai' | 'auto' | 'danger' | 'wait' | 'plain'
  export interface Badge { text: string; tone: Tone }
  export interface RowView { time: string; host: string; badges: Badge[]; main: string; side: string[] }
  export function rowView(r: AuditRecord, now: Date): RowView
  export interface AuditList { status: 'idle' | 'loading' | 'loaded' | 'error' | 'cleared'; entries: AuditEntry[]; held: AuditEntry[]; next?: number; skipped: number; path: string; error?: string }
  export const EMPTY: AuditList
  export function mergeEntries(a: AuditEntry[], b: AuditEntry[]): AuditEntry[]
  export function startLoad(l: AuditList): AuditList
  export function applyPage(l: AuditList, p: AuditPage): AuditList
  export function failLoad(l: AuditList, message: string): AuditList
  export function appendLive(l: AuditList, e: AuditEntry, q: AuditQuery, atTop: boolean): AuditList
  export function releaseHeld(l: AuditList): AuditList
  export function clearOnLock(): AuditList
  ```

List states: `idle` (nothing loaded; the view reads when shown with `ready`), `loading`, `loaded`, `error`, and `cleared` (set by the `locked` notification; never loads by itself, so no hub call follows a notification; the view resets it to `idle` when `ready` changes). Live records are merged only while `loading` or `loaded`, deduplicated by `seq`, and held as "N new" while the table is scrolled away from the top.

- [ ] **Step 1: Write the failing tests**

Create `desktop/test/audit.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import {
  appendLive, applyPage, clearOnLock, EMPTY, failLoad, formatTime, matches, mergeEntries, releaseHeld, rowView,
  startLoad, toggleChip, toQuery, type AuditList,
} from '../src/renderer/audit'
import { REQUEST_METHODS, type AuditEntry, type AuditRecord } from '../src/shared/protocol'

const T = new Date(2026, 8, 30, 14, 5, 9) // local time, so the test is TZ-independent
const iso = (d: Date) => d.toISOString()
const at = (ms: number) => iso(new Date(T.getTime() + ms))
const entry = (seq: number, record: Partial<AuditRecord> = {}): AuditEntry => ({ seq, record: { time: iso(T), ...record } })
const seqs = (l: AuditList) => l.entries.map((e) => e.seq)
const loaded = (entries: AuditEntry[]): AuditList => ({ ...EMPTY, status: 'loaded', entries })

describe('formatTime', () => {
  it('shows HH:MM:SS today and the date otherwise', () => {
    expect(formatTime(iso(T), T)).toBe('14:05:09')
    expect(formatTime(iso(new Date(2026, 8, 29, 23, 59, 1)), T)).toBe('2026-09-29 23:59:01')
    expect(formatTime('not a time', T)).toBe('')
  })
})

describe('rowView', () => {
  it('exec: outcome, sudo and wait', () => {
    expect(rowView({ time: iso(T), server: 'box', command: 'rm -rf /tmp/x', outcome: 'denied', sudo: true, waitMs: 12_300 }, T)).toEqual({
      time: '14:05:09', host: 'box', main: 'rm -rf /tmp/x', side: ['wait 12 s'],
      badges: [{ text: 'denied', tone: 'danger' }, { text: 'sudo', tone: 'danger' }],
    })
  })
  it('exec: an auto run shows auto, its exit code and the masked count', () => {
    const v = rowView({ time: iso(T), server: 'box', command: 'cat .env', outcome: 'allowed', approval: 'auto', exitCode: 0, redacted: { password: 2, token: 1 } }, T)
    expect(v.badges).toEqual([{ text: 'auto', tone: 'auto' }])
    expect(v.side).toEqual(['exit 0', '3 masked'])
  })
  it('exec: every other outcome', () => {
    const badges = (r: Partial<AuditRecord>) => rowView({ time: iso(T), command: 'x', ...r }, T).badges.map((b) => b.text)
    expect(badges({ outcome: 'allowed' })).toEqual(['allowed'])
    expect(badges({ outcome: 'error', approval: 'auto' })).toEqual(['auto', 'error'])
    expect(badges({ outcome: 'expired' })).toEqual(['expired'])
    expect(badges({ outcome: 'approved_but_cancelled' })).toEqual(['cancelled'])
    expect(badges({ outcome: 'cancelled_running' })).toEqual(['cancelled'])
    expect(badges({ outcome: 'sent_to_tab' })).toEqual(['sent to tab'])
    expect(rowView({ time: iso(T), command: 'x', outcome: 'allowed', waitMs: 400 }, T).side).toEqual(['wait 400 ms'])
  })
  it('config: the action and its detail', () => {
    const main = (r: Partial<AuditRecord>) => rowView({ time: iso(T), kind: 'config', ...r }, T).main
    expect(main({ action: 'autoAllowOn', until: at(15 * 60_000 - 800) })).toBe('autoAllowOn 15m')
    expect(main({ action: 'autoAllowOn', until: at(2 * 3_600_000) })).toBe('autoAllowOn 2h')
    expect(main({ action: 'autoAllowOn', forever: true })).toBe('autoAllowOn forever')
    expect(main({ action: 'save', changed: ['host: a → 10.0.0.2', 'password'] })).toBe('save host: a → 10.0.0.2, password')
    expect(main({ action: 'autoAllowOff', reason: 'locked' })).toBe('autoAllowOff locked')
    expect(main({ action: 'delete' })).toBe('delete')
    expect(rowView({ time: iso(T), kind: 'config', action: 'save', server: 'box' }, T).badges).toEqual([{ text: 'save', tone: 'plain' }])
  })
  it('file: the action, the first path and the phase', () => {
    expect(rowView({ time: iso(T), kind: 'file', server: 'box', action: 'upload', phase: 'start', remote: ['/home/u/a.txt', '/home/u/b.txt'] }, T)).toEqual({
      time: '14:05:09', host: 'box', badges: [{ text: 'upload', tone: 'plain' }], main: '/home/u/a.txt', side: ['start'],
    })
    expect(rowView({ time: iso(T), kind: 'file', action: 'rename', from: '/a', to: '/b' }, T).main).toBe('/a → /b')
  })
  it('tunnel: the phase and listen → target', () => {
    const v = rowView({ time: iso(T), kind: 'tunnel', server: 'box', phase: 'start', listen: '127.0.0.1:5433', to: 'db:5432' }, T)
    expect(v.badges).toEqual([{ text: 'start', tone: 'plain' }])
    expect(v.main).toBe('127.0.0.1:5433 → db:5432')
    expect(rowView({ time: iso(T), kind: 'tunnel', phase: 'end', listen: '127.0.0.1:1080' }, T).main).toBe('127.0.0.1:1080 SOCKS5')
  })
  it('never throws on a record with wrong field types', () => {
    const bad = { time: 5, command: 7, server: {}, outcome: null, redacted: { x: 'y' } } as unknown as AuditRecord
    expect(rowView(bad, T)).toMatchObject({ time: '', host: '', main: '' })
  })
})

describe('toQuery', () => {
  it('maps chips to a union of kinds and exec outcomes; none on is everything', () => {
    expect(toQuery({ server: '', chips: [], text: '' })).toEqual({})
    expect(toQuery({ server: '', chips: ['auto'], text: '' })).toEqual({ outcomes: ['auto'] })
    expect(toQuery({ server: '', chips: ['denied', 'exec'], text: '' })).toEqual({ kinds: ['exec'], outcomes: ['denied'] })
    expect(toQuery({ server: '', chips: ['tunnels', 'files', 'config'], text: '' })).toEqual({ kinds: ['config', 'file', 'tunnel'] })
    expect(toQuery({ server: 'box', chips: [], text: '  2>&1 ' })).toEqual({ server: 'box', text: '2>&1' })
  })
  it('toggles a chip', () => {
    expect(toggleChip(['exec'], 'auto')).toEqual(['exec', 'auto'])
    expect(toggleChip(['exec', 'auto'], 'exec')).toEqual(['auto'])
  })
})

describe('matches', () => {
  const auto: AuditRecord = { time: 't', server: 'box', command: 'tail 2>&1', outcome: 'allowed', approval: 'auto' }
  const denied: AuditRecord = { time: 't', server: 'vis', command: 'rm x', outcome: 'denied' }
  const save: AuditRecord = { time: 't', kind: 'config', server: 'vis', action: 'save' }
  const all = [auto, denied, save]
  it('mirrors the hub: a union of kinds and exec outcomes, narrowed by server and text', () => {
    expect(all.map((r) => matches(r, {}))).toEqual([true, true, true])
    expect(all.map((r) => matches(r, { outcomes: ['auto'] }))).toEqual([true, false, false])
    expect(all.map((r) => matches(r, { kinds: ['config'], outcomes: ['denied'] }))).toEqual([false, true, true])
    expect(all.map((r) => matches(r, { outcomes: ['allowed'] }))).toEqual([false, false, false])
    expect(all.map((r) => matches(r, { server: 'vis' }))).toEqual([false, true, true])
    expect(all.map((r) => matches(r, { text: 'TAIL 2>&1' }))).toEqual([true, false, false])
    expect(matches({ time: 't', command: 'x', outcome: 'cancelled_running' }, { outcomes: ['cancelled'] })).toBe(true)
  })
})

describe('the record list', () => {
  it('merges by seq, newest first', () => {
    expect(mergeEntries([entry(5), entry(3)], [entry(4), entry(5), entry(1)]).map((e) => e.seq)).toEqual([5, 4, 3, 1])
  })
  it('applies a first page, then an older one', () => {
    let l = startLoad({ ...EMPTY, path: '/old' })
    expect(l).toMatchObject({ status: 'loading', entries: [], path: '/old' })
    l = applyPage(l, { records: [entry(9), entry(8)], next: 8, skipped: 1, path: '/a/audit.jsonl' })
    expect(l).toMatchObject({ status: 'loaded', next: 8, skipped: 1, path: '/a/audit.jsonl' })
    l = applyPage(l, { records: [entry(7)], skipped: 1, path: '/a/audit.jsonl' })
    expect(seqs(l)).toEqual([9, 8, 7])
    expect(l.next).toBeUndefined()
    expect(failLoad(l, 'boom')).toMatchObject({ status: 'error', error: 'boom', entries: l.entries })
  })
  it('inserts a live record at the top, once, only if it matches', () => {
    const l = loaded([entry(2), entry(1)])
    expect(seqs(appendLive(l, entry(3), {}, true))).toEqual([3, 2, 1])
    expect(appendLive(l, entry(2), {}, true)).toBe(l)
    expect(appendLive(l, entry(3, { kind: 'config' }), { kinds: ['exec'] }, true)).toBe(l)
  })
  it('holds live records as "N new" while scrolled away, then releases them', () => {
    let l = loaded([entry(1)])
    l = appendLive(l, entry(2), {}, false)
    l = appendLive(l, entry(3), {}, false)
    l = appendLive(l, entry(3), {}, false)
    expect(seqs(l)).toEqual([1])
    expect(l.held.map((e) => e.seq)).toEqual([3, 2])
    l = releaseHeld(l)
    expect(seqs(l)).toEqual([3, 2, 1])
    expect(l.held).toEqual([])
    expect(releaseHeld(l)).toBe(l)
  })
  it('keeps a live record that arrives while a read is in flight', () => {
    let l = appendLive(startLoad(EMPTY), entry(5), {}, true)
    l = applyPage(l, { records: [entry(5), entry(4)], skipped: 0, path: '/p' })
    expect(seqs(l)).toEqual([5, 4])
  })
  it('is cleared on lock, and ignores live records until the next read', () => {
    const l = clearOnLock()
    expect(l).toMatchObject({ status: 'cleared', entries: [], held: [], skipped: 0, path: '' })
    expect(appendLive(l, entry(9), {}, true)).toBe(l)
    expect(appendLive(EMPTY, entry(9), {}, true)).toBe(EMPTY)
  })
  it('audit.read is on Electron main\'s relay whitelist', () => {
    expect(REQUEST_METHODS).toContain('audit.read')
  })
})
```

In `desktop/test/transport.test.ts`, add after the `'auto-allow calls'` test:

```ts
  it('audit.read passes the query as is', async () => {
    const page = { records: [{ seq: 3, record: { time: 't', command: 'ls' } }], next: 3, skipped: 0, path: '/p/audit.jsonl' }
    bridge.call.mockResolvedValueOnce(page)
    expect(await hub.auditRead({ before: 9, kinds: ['exec'], text: 'ls' })).toEqual(page)
    expect(bridge.call).toHaveBeenLastCalledWith('audit.read', { before: 9, kinds: ['exec'], text: 'ls' })
  })
```

In `desktop/test/terminals.test.ts`, change the import line to

```ts
import { approvalsKey, AUDIT_TAB, clipboardKey, Debouncer, Dispatcher, newTermId, printable, TabSet, isUserInput } from '../src/renderer/terminals'
```

and add inside `describe('TabSet', ...)`, after `'shows the Hosts home when asked and after the last tab closes'`:

```ts
  it('shows the fixed Audit tab, which is never in the activation order', () => {
    const s = new TabSet()
    const a = s.open('box')
    s.showAudit()
    expect(s.active).toBe(AUDIT_TAB)
    s.close(a.id)
    expect(s.active).toBe(AUDIT_TAB)
    const b = s.open('box')
    s.showAudit()
    s.activate(b.id)
    s.close(b.id)
    expect(s.active).toBeUndefined() // back to Hosts, not to Audit
  })
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd desktop && npx vitest run test/audit.test.ts test/transport.test.ts test/terminals.test.ts`
Expected: FAIL: `Failed to load url ../src/renderer/audit` (audit.test.ts), `hub.auditRead is not a function` (transport), `s.showAudit is not a function` (terminals).

- [ ] **Step 3: Implement**

In `desktop/src/shared/protocol.ts`, insert before `export type HubEvent =`:

```ts
// Audit log (slice 4b). A record is one audit.jsonl line as stored; exec
// records have no kind. Every field but time is optional per kind, and the
// renderer must not trust field types (the file can be edited by hand).
export type AuditKind = 'exec' | 'config' | 'file' | 'tunnel'
export type AuditOutcome = 'allowed' | 'auto' | 'denied' | 'expired' | 'cancelled' | 'error'
export interface AuditQuery { before?: number; limit?: number; server?: string; kinds?: AuditKind[]; outcomes?: AuditOutcome[]; text?: string }
export interface AuditRecord {
  time: string; kind?: 'config' | 'file' | 'tunnel'; server?: string; reason?: string
  // exec
  client?: string; command?: string; description?: string; sudo?: boolean; timeoutSec?: number; outcome?: string
  exitCode?: number; durationMs?: number; approval?: string; waitMs?: number; redacted?: Record<string, number>
  // config
  action?: string; changed?: string[]; until?: string; forever?: boolean; fingerprint?: string; oldFingerprint?: string
  // file (action too)
  phase?: string; remote?: string[]; from?: string; to?: string
  // tunnel (phase and to too)
  listen?: string; tunnelKind?: string; target?: string; id?: string
}
export interface AuditEntry { seq: number; record: AuditRecord }
export interface AuditPage { records: AuditEntry[]; next?: number; skipped: number; path: string }
```

In the `HubEvent` union, after `| { method: 'autoAllow.off'; params: { server: string; reason: string } }` add:

```ts
  | { method: 'audit.appended'; params: AuditEntry }
```

In `REQUEST_METHODS`, replace `'servers.setAutoAllow', 'servers.autoAllowCheck'] as const` with:

```ts
  'servers.setAutoAllow', 'servers.autoAllowCheck', 'audit.read'] as const
```

In `desktop/src/renderer/transport.ts`, replace the first line's import with:

```ts
import type { ApprovalRequest, AuditPage, AuditQuery, AutoAllowCheck, AutoAllowMode, FileGrant, FileOp, FilesListResult, HubEvent, HubState, ImportResult, ImportScan, ServerInfo, ServerInput, Status, TermOpenResult, Tunnel, TunnelView } from '../shared/protocol'
```

and add as the last member of the `hub` object, after `autoAllowCheck`:

```ts
  auditRead: (q: AuditQuery) => call<AuditPage>('audit.read', q),
```

In `desktop/src/renderer/terminals.ts`, add above `export class TabSet {`:

```ts
// The fixed Audit tab's value of TabSet.active; terminal ids start with "t-".
export const AUDIT_TAB = 'audit'
```

and after `showHome() { this.active = undefined }` add:

```ts
  // The fixed Audit tab: like Hosts, never closed and never in the activation order.
  showAudit() { this.active = AUDIT_TAB }
```

Create `desktop/src/renderer/audit.ts`:

```ts
import type { AuditEntry, AuditKind, AuditOutcome, AuditPage, AuditQuery, AuditRecord } from '../shared/protocol'

export type Chip = 'exec' | 'auto' | 'denied' | 'config' | 'files' | 'tunnels'

// The filter chips, in display order. A kind chip adds that record kind;
// Auto and Denied add those exec outcomes. The hub ORs kinds and outcomes,
// so the chips that are on form a union, and none on means every record.
export const CHIPS: { id: Chip; label: string; kind?: AuditKind; outcome?: AuditOutcome }[] = [
  { id: 'exec', label: 'Exec', kind: 'exec' },
  { id: 'auto', label: 'Auto', outcome: 'auto' },
  { id: 'denied', label: 'Denied', outcome: 'denied' },
  { id: 'config', label: 'Config', kind: 'config' },
  { id: 'files', label: 'Files', kind: 'file' },
  { id: 'tunnels', label: 'Tunnels', kind: 'tunnel' },
]

export interface Filters { server: string; chips: Chip[]; text: string }
export const NO_FILTERS: Filters = { server: '', chips: [], text: '' }

export const toggleChip = (chips: Chip[], c: Chip): Chip[] =>
  chips.includes(c) ? chips.filter((x) => x !== c) : [...chips, c]

export function toQuery(f: Filters): AuditQuery {
  const q: AuditQuery = {}
  if (f.server) q.server = f.server
  const on = CHIPS.filter((c) => f.chips.includes(c.id))
  const kinds = on.flatMap((c) => (c.kind ? [c.kind] : []))
  const outcomes = on.flatMap((c) => (c.outcome ? [c.outcome] : []))
  if (kinds.length) q.kinds = kinds
  if (outcomes.length) q.outcomes = outcomes
  const text = f.text.trim()
  if (text) q.text = text
  return q
}

// broker's fields.outcomeIs, for records that arrive by audit.appended.
function outcomeIs(r: AuditRecord, o: AuditOutcome): boolean {
  switch (o) {
    case 'auto': return r.approval === 'auto'
    case 'allowed': return r.outcome === 'allowed' && r.approval !== 'auto'
    case 'cancelled': return r.outcome === 'approved_but_cancelled' || r.outcome === 'cancelled_running'
    default: return r.outcome === o
  }
}

// Whether r belongs in a list read with q: broker's ReadQuery.match, mirrored.
// JSON.stringify writes <, > and & as themselves, like the hub's decoded line.
export function matches(r: AuditRecord, q: AuditQuery): boolean {
  if (q.server && r.server !== q.server) return false
  if (q.text && !JSON.stringify(r).toLowerCase().includes(q.text.toLowerCase())) return false
  if (!q.kinds?.length && !q.outcomes?.length) return true
  const kind: AuditKind = r.kind || 'exec'
  if (q.kinds?.includes(kind)) return true
  return kind === 'exec' && !!q.outcomes?.some((o) => outcomeIs(r, o))
}

// The file can be edited by hand: never trust a field's type.
const str = (v: unknown): string => (typeof v === 'string' ? v : '')
const pad = (n: number) => String(n).padStart(2, '0')

// HH:MM:SS local time, with the date in front when it is not today.
export function formatTime(iso: string, now: Date): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const t = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  return d.toDateString() === now.toDateString() ? t : `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${t}`
}

const formatWait = (ms: number) => (ms < 1000 ? `${ms} ms` : `${Math.round(ms / 1000)} s`)

// A timed grant's length (15m, 2h), from its record's time to its deadline.
function grantLength(r: AuditRecord): string {
  if (r.forever === true) return 'forever'
  const min = Math.round((new Date(str(r.until)).getTime() - new Date(str(r.time)).getTime()) / 60_000)
  if (!Number.isFinite(min)) return ''
  return min >= 60 && min % 60 === 0 ? `${min / 60}h` : `${min}m`
}

export function maskedCount(r: AuditRecord): number {
  const red: unknown = r.redacted
  if (!red || typeof red !== 'object') return 0
  return Object.values(red).reduce<number>((n, v) => n + (typeof v === 'number' ? v : 0), 0)
}

export type Tone = 'ai' | 'auto' | 'danger' | 'wait' | 'plain'
export interface Badge { text: string; tone: Tone }
export interface RowView { time: string; host: string; badges: Badge[]; main: string; side: string[] }

const EXEC_BADGE: Record<string, Badge> = {
  allowed: { text: 'allowed', tone: 'ai' },
  denied: { text: 'denied', tone: 'danger' },
  expired: { text: 'expired', tone: 'wait' },
  sent_to_tab: { text: 'sent to tab', tone: 'plain' },
  approved_but_cancelled: { text: 'cancelled', tone: 'wait' },
  cancelled_running: { text: 'cancelled', tone: 'wait' },
  error: { text: 'error', tone: 'danger' },
}

// One table row: time, host, badges, the main text, and the side notes.
export function rowView(r: AuditRecord, now: Date): RowView {
  const base = { time: formatTime(str(r.time), now), host: str(r.server) }
  const list = (v: unknown): string[] => (Array.isArray(v) ? v.map(str) : [])
  switch (r.kind) {
    case 'config': {
      const action = str(r.action)
      const changed = list(r.changed)
      const detail = action === 'autoAllowOn' ? grantLength(r)
        : changed.length ? changed.join(', ')
          : str(r.reason) || str(r.fingerprint) || str(r.oldFingerprint)
      return { ...base, badges: [{ text: action, tone: 'plain' }], main: detail ? `${action} ${detail}` : action, side: [] }
    }
    case 'file': {
      const main = r.action === 'rename' ? `${str(r.from)} → ${str(r.to)}` : list(r.remote)[0] ?? ''
      return { ...base, badges: [{ text: str(r.action), tone: 'plain' }], main, side: r.phase ? [str(r.phase)] : [] }
    }
    case 'tunnel':
      return { ...base, badges: [{ text: str(r.phase), tone: 'plain' }],
        main: r.to ? `${str(r.listen)} → ${str(r.to)}` : `${str(r.listen)} SOCKS5`, side: [] }
  }
  const outcome = str(r.outcome)
  const auto = r.approval === 'auto'
  const badges: Badge[] = auto ? [{ text: 'auto', tone: 'auto' }] : []
  if (!auto || outcome !== 'allowed') badges.push(EXEC_BADGE[outcome] ?? { text: outcome, tone: 'plain' })
  if (r.sudo === true) badges.push({ text: 'sudo', tone: 'danger' })
  const side: string[] = []
  if (typeof r.exitCode === 'number') side.push(`exit ${r.exitCode}`)
  if (typeof r.waitMs === 'number' && r.waitMs > 0) side.push(`wait ${formatWait(r.waitMs)}`)
  const masked = maskedCount(r)
  if (masked > 0) side.push(`${masked} masked`)
  return { ...base, badges, main: str(r.command), side }
}

// The records the tab holds. 'cleared' follows a lock and never loads by
// itself; the view resets it to 'idle' when ready changes, and reads from
// 'idle' only when shown with ready.
export interface AuditList {
  status: 'idle' | 'loading' | 'loaded' | 'error' | 'cleared'
  entries: AuditEntry[] // newest first, one per seq
  held: AuditEntry[] // live records that arrived while scrolled away: "N new"
  next?: number
  skipped: number
  path: string
  error?: string
}

export const EMPTY: AuditList = { status: 'idle', entries: [], held: [], skipped: 0, path: '' }

export function mergeEntries(a: AuditEntry[], b: AuditEntry[]): AuditEntry[] {
  const bySeq = new Map<number, AuditEntry>()
  for (const e of [...a, ...b]) if (!bySeq.has(e.seq)) bySeq.set(e.seq, e)
  return [...bySeq.values()].sort((x, y) => y.seq - x.seq)
}

export const startLoad = (l: AuditList): AuditList => ({ ...EMPTY, status: 'loading', path: l.path })

export const applyPage = (l: AuditList, p: AuditPage): AuditList => ({
  ...l, status: 'loaded', entries: mergeEntries(l.entries, p.records), next: p.next, skipped: p.skipped, path: p.path, error: undefined,
})

export const failLoad = (l: AuditList, message: string): AuditList => ({ ...l, status: 'error', error: message })

// An audit.appended record: kept only while a list is loading or loaded, if
// it matches the query that list was read with and is not there yet; held
// while the table is scrolled away from the top, so rows never move under
// the cursor.
export function appendLive(l: AuditList, e: AuditEntry, q: AuditQuery, atTop: boolean): AuditList {
  if (l.status !== 'loading' && l.status !== 'loaded') return l
  if (!matches(e.record, q)) return l
  if (l.entries.some((x) => x.seq === e.seq) || l.held.some((x) => x.seq === e.seq)) return l
  return atTop ? { ...l, entries: mergeEntries(l.entries, [e]) } : { ...l, held: mergeEntries(l.held, [e]) }
}

export const releaseHeld = (l: AuditList): AuditList =>
  l.held.length ? { ...l, entries: mergeEntries(l.entries, l.held), held: [] } : l

export const clearOnLock = (): AuditList => ({ ...EMPTY, status: 'cleared' })
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd desktop && npx vitest run test/audit.test.ts test/transport.test.ts test/terminals.test.ts`
Expected: PASS.

Run: `cd desktop && npm run typecheck && npm test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add desktop/src/shared/protocol.ts desktop/src/renderer/transport.ts desktop/src/renderer/audit.ts \
  desktop/src/renderer/terminals.ts desktop/test/audit.test.ts desktop/test/transport.test.ts desktop/test/terminals.test.ts
git commit -F - <<'EOF'
feat(desktop): audit types, hub.auditRead and the audit list logic

Chips map to a union query, live records are matched against the shown
query and merged by seq (held as "N new" while scrolled away), and a lock
clears the list without triggering a read.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
```

---

### Task 4: the Audit tab and its e2e

**Files:**
- Create: `desktop/e2e/audit.spec.ts`
- Create: `desktop/src/renderer/AuditView.tsx`
- Modify: `desktop/src/renderer/TerminalTabs.tsx` (imports, props, tab strip, `termarea`)
- Modify: `desktop/src/renderer/App.tsx` (the `<Terminals ... />` element)
- Modify: `desktop/src/renderer/icons.tsx` (`ListIcon`)
- Modify: `desktop/src/renderer/styles.css` (`.hometab` rules at lines 109-111; new audit block at the end)

**Interfaces:**
- Consumes (Task 3): `hub.auditRead`, `AUDIT_TAB`, `TabSet.showAudit`, everything exported by `audit.ts`, `AuditRecord`/`AuditQuery`/`ServerInfo` types; `Latest` from `approvals.ts`; `Debouncer` from `terminals.ts`; `displayText` from `shared/display.ts`.
- Produces: `export function AuditView(props: { visible: boolean; ready: boolean; servers: ServerInfo[] })`; `Terminals` gains a required `ready: boolean` prop. e2e selectors: `.tabbar .audittab`, region `Audit log`, `tr.auditrow` (with `data-seq`), `td.badges`, `tr.auditdetail`, filter chip buttons named `Exec`, `Auto`, `Denied`, `Config`, `Files`, `Tunnels`.

Notes the code below already handles:
- The tab button's class is `audittab`, not `hometab`: `e2e/launch.ts` and several specs click `.tabbar .hometab`, and a second match is a Playwright strict-mode error.
- `Terminals` gets `ready` from `App`: its `locked` prop is `!!status?.locked`, which is `false` while the hub restarts and `status` is undefined.
- Chromium drops `scrollTop` under `display:none`, so the view saves it on scroll (only while visible) and restores it when shown.
- `displayText` escapes newlines, so the raw JSON block is escaped per line.

- [ ] **Step 1: Write the failing e2e test**

Create `desktop/e2e/audit.spec.ts`:

```ts
import { test, expect, type Page } from '@playwright/test'
import { launch, unlock, type Launched } from './launch'
import { doorCall } from './doorClient'

// Spec 2026-09-30-slice4b-audit-viewer-design.md.
test.skip(process.platform === 'win32', 'door client uses a Unix socket')

let l: Launched
test.beforeAll(async () => { l = await launch() })
test.afterAll(async () => { await l?.close() })

const exec = (id: string, command: string) =>
  doorCall(l.socket, 'exec', { requestId: id, client: 'e2e', server: 'box', command, description: 'audit e2e' })

// An AI request the human denies from the AI column.
async function denied(win: Page, id: string, command: string) {
  const p = exec(id, command)
  p.catch(() => {}) // denied below; avoids a transient unhandled-rejection warning
  await expect(win.locator('.approval')).toContainText(command)
  await win.locator('.approval input').press('Enter') // deny
  await expect(p).rejects.toThrow(/Denied/)
}

// Copied from autoallow.spec.ts: opens the Host editor on box, expands AI
// access, picks a duration, and Saves. The caller clicks Enable.
async function chooseAutoAllow(win: Page, mode: string) {
  await win.locator('.tabbar .hometab').click()
  await win.locator('nav.hosts').getByRole('button', { name: 'Edit box' }).click()
  const editor = win.getByRole('dialog', { name: 'Host editor' })
  await editor.locator('summary', { hasText: 'AI access' }).click()
  await editor.getByLabel('Auto-allow', { exact: true }).selectOption(mode)
  await editor.getByRole('button', { name: 'Save' }).click()
}

test('the Audit tab lists, filters, expands, updates live, and reloads after a lock', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  await denied(win, 'd1', 'echo audit-denied')

  // Auto-allow box (the editor's Save also writes a save record), run one exec, stop.
  await chooseAutoAllow(win, '15m')
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await win.waitForTimeout(600)
  await d.getByRole('button', { name: 'Enable' }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveText(/Auto 1[45]m/)
  expect(JSON.stringify(await exec('a1', 'echo audit-auto'))).toContain('echo audit-auto')
  await win.getByRole('button', { name: 'Stop auto-allow box', exact: true }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveCount(0)

  // Everything is listed, newest first.
  await win.locator('.tabbar .audittab').click()
  const region = win.getByRole('region', { name: 'Audit log' })
  const rows = region.locator('tr.auditrow')
  await expect(rows.filter({ hasText: 'echo audit-denied' })).toHaveCount(1)
  await expect(rows.filter({ hasText: 'echo audit-auto' })).toHaveCount(1)
  await expect(rows.filter({ hasText: 'autoAllowOn 15m' })).toHaveCount(1)
  await expect(region.locator('tr.auditrow td.badges', { hasText: /^save$/ })).toHaveCount(1)
  const seqs = await rows.evaluateAll((els) => els.map((e) => Number(e.getAttribute('data-seq'))))
  expect(seqs).toEqual([...seqs].sort((a, b) => b - a))
  const texts = await rows.allTextContents()
  expect(texts.findIndex((t) => t.includes('echo audit-auto'))).toBeLessThan(texts.findIndex((t) => t.includes('echo audit-denied')))
  await expect(region.locator('footer')).toContainText('audit.jsonl')

  // Auto shows only the auto-allowed run.
  const autoChip = region.getByRole('button', { name: 'Auto', exact: true })
  await autoChip.click()
  await expect(rows).toHaveCount(1)
  await expect(rows.first()).toContainText('echo audit-auto')
  await autoChip.click()
  await expect(rows.filter({ hasText: 'echo audit-denied' })).toHaveCount(1)

  // Expanding a row shows the full command and the AI's description.
  await rows.filter({ hasText: 'echo audit-denied' }).click()
  const detail = region.locator('tr.auditdetail')
  await expect(detail).toContainText('echo audit-denied')
  await expect(detail).toContainText("AI's description · unverified")
  await expect(detail).toContainText('audit e2e')

  // With the tab open, a new record appears without Refresh.
  await denied(win, 'l1', 'echo audit-live')
  await expect(rows.first()).toContainText('echo audit-live')

  // Lock drops the records; unlocking reads them again.
  await win.getByRole('button', { name: 'Lock' }).click()
  await expect(win.getByLabel('Master password')).toBeVisible()
  await expect(win.locator('tr.auditrow')).toHaveCount(0)
  await unlock(win)
  await expect(rows.first()).toContainText('echo audit-live')
  await expect(rows.filter({ hasText: 'echo audit-denied' })).toHaveCount(1)
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd desktop && npm run build && npx playwright test e2e/audit.spec.ts` (Linux: prefix `xvfb-run -a`)
Expected: FAIL at `win.locator('.tabbar .audittab').click()`: `Timeout ... waiting for locator('.tabbar .audittab')`.

- [ ] **Step 3: Implement**

In `desktop/src/renderer/icons.tsx`, after the `StopIcon` line add:

```tsx
export const ListIcon = icon(<path d="M9 6h11M9 12h11M9 18h11M4 6h.01M4 12h.01M4 18h.01" />)
```

Create `desktop/src/renderer/AuditView.tsx`:

```tsx
import { Fragment, useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { AuditQuery, AuditRecord, ServerInfo } from '../shared/protocol'
import { displayText } from '../shared/display'
import { hub } from './transport'
import { Latest } from './approvals'
import { Debouncer } from './terminals'
import {
  appendLive, applyPage, CHIPS, clearOnLock, EMPTY, failLoad, maskedCount, NO_FILTERS, releaseHeld, rowView,
  startLoad, toggleChip, toQuery, type AuditList, type Filters,
} from './audit'
import { ChevronDownIcon, RefreshIcon } from './icons'

// The fixed Audit tab. It calls audit.read only when it is shown with the
// vault unlocked and nothing loaded, or on a filter change, Enter or 300 ms
// after typing in search, Refresh, or Load older. audit.appended records are
// merged in live; a notification or timer never calls the hub.
export function AuditView({ visible, ready, servers }: { visible: boolean; ready: boolean; servers: ServerInfo[] }) {
  const [list, setList] = useState<AuditList>(EMPTY)
  const [filters, setFilters] = useState<Filters>(NO_FILTERS)
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(new Set())
  const [older, setOlder] = useState(false) // a Load older call is in flight
  const gen = useRef(new Latest()).current // replies to superseded reads are dropped
  const query = useRef<AuditQuery>({}) // the query the shown list was read with
  const filtersNow = useRef(filters)
  filtersNow.current = filters
  const scroller = useRef<HTMLDivElement>(null)
  const savedTop = useRef(0)
  const atTop = useRef(true)

  const toTop = () => {
    if (scroller.current) scroller.current.scrollTop = 0
    savedTop.current = 0
    atTop.current = true
  }
  const load = async (f: Filters) => {
    const g = gen.next()
    query.current = toQuery(f)
    setOlder(false)
    setList(startLoad)
    toTop()
    try {
      const page = await hub.auditRead(query.current)
      if (gen.isCurrent(g)) setList((l) => applyPage(l, page))
    } catch (e) {
      if (gen.isCurrent(g)) setList((l) => failLoad(l, (e as Error).message))
    }
  }
  const loadOlder = async (before: number) => {
    const g = gen.next()
    setOlder(true)
    try {
      const page = await hub.auditRead({ ...query.current, before })
      if (gen.isCurrent(g)) setList((l) => applyPage(l, page))
    } catch (e) {
      if (gen.isCurrent(g)) setList((l) => failLoad(l, (e as Error).message))
    } finally {
      if (gen.isCurrent(g)) setOlder(false)
    }
  }
  // Follows the human's typing only; never a poll.
  const typing = useRef(new Debouncer(300, () => void load(filtersNow.current))).current
  useEffect(() => () => typing.cancel(), [typing])

  useEffect(() => hub.onEvent((e) => {
    if (e.method === 'locked') {
      gen.next()
      setOlder(false)
      setExpanded(new Set())
      setList(clearOnLock())
    } else if (e.method === 'audit.appended') {
      setList((l) => appendLive(l, e.params, query.current, atTop.current))
    }
  }), [gen])
  // Every change of ready (a lock, an unlock, a hub restart) starts over.
  useEffect(() => { gen.next(); setOlder(false); setList(EMPTY) }, [ready, gen])
  useEffect(() => {
    if (visible && ready && list.status === 'idle') void load(filtersNow.current)
  }, [visible, ready, list.status])

  const onScroll = () => {
    const el = scroller.current
    if (!visible || !el) return // display:none zeroes scrollTop; keep the saved one
    savedTop.current = el.scrollTop
    atTop.current = el.scrollTop <= 2
    if (atTop.current) setList(releaseHeld)
  }
  // Chromium drops scrollTop under display:none; put it back when shown.
  useLayoutEffect(() => {
    if (visible && scroller.current) scroller.current.scrollTop = savedTop.current
  }, [visible])

  const change = (f: Filters) => { typing.cancel(); setFilters(f); void load(f) }
  const toggle = (seq: number) => setExpanded((s) => {
    const n = new Set(s)
    if (!n.delete(seq)) n.add(seq)
    return n
  })
  const now = new Date()
  return (
    <section className="auditview" role="region" aria-label="Audit log" style={{ display: visible ? 'flex' : 'none' }}>
      <div className="audit-toolbar">
        <span className="select audit-host">
          <select aria-label="Host" value={filters.server} onChange={(e) => change({ ...filters, server: e.target.value })}>
            <option value="">All hosts</option>
            {servers.map((s) => <option key={s.name} value={s.name}>{displayText(s.name)}</option>)}
          </select>
          <ChevronDownIcon />
        </span>
        <div className="audit-chips">
          {CHIPS.map((c) => (
            <button key={c.id} type="button" className="filterchip" aria-pressed={filters.chips.includes(c.id)}
              onClick={() => change({ ...filters, chips: toggleChip(filters.chips, c.id) })}>{c.label}</button>
          ))}
        </div>
        <input type="search" aria-label="Search audit" placeholder="Search" value={filters.text}
          onChange={(e) => { setFilters({ ...filters, text: e.target.value }); typing.poke() }}
          onKeyDown={(e) => { if (e.key === 'Enter') { typing.cancel(); void load(filtersNow.current) } }} />
        <button type="button" className="btn" onClick={() => { typing.cancel(); void load(filters) }}><RefreshIcon />Refresh</button>
      </div>
      {list.error && <p className="error audit-error" role="alert">{displayText(list.error)}</p>}
      <div className="audit-body">
        {list.held.length > 0 && (
          <button type="button" className="btn sm newpill" onClick={() => { toTop(); setList(releaseHeld) }}>{`${list.held.length} new`}</button>
        )}
        <div className="audit-scroll" ref={scroller} onScroll={onScroll}>
          <table className="audittable">
            <tbody>
              {list.entries.map((e) => {
                const v = rowView(e.record, now)
                const open = expanded.has(e.seq)
                return (
                  <Fragment key={e.seq}>
                    <tr className="auditrow" data-seq={e.seq} tabIndex={0} aria-expanded={open} onClick={() => toggle(e.seq)}
                      onKeyDown={(k) => { if (k.key === 'Enter') { k.preventDefault(); toggle(e.seq) } }}>
                      <td className="when">{v.time}</td>
                      <td className="host">{displayText(v.host)}</td>
                      <td className="badges">{v.badges.map((b, i) => <span key={i} className={'chip ' + b.tone}>{displayText(b.text)}</span>)}</td>
                      <td className="main mono" title={displayText(v.main)}>{displayText(v.main)}</td>
                      <td className="side">{v.side.join(' · ')}</td>
                    </tr>
                    {open && <tr className="auditdetail"><td colSpan={5}><AuditDetail record={e.record} /></td></tr>}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
          {list.status === 'loading' && <p className="muted audit-note">Loading…</p>}
          {list.status === 'loaded' && list.entries.length === 0 && <p className="empty audit-note">No audit records match.</p>}
          {list.next !== undefined && (
            <button type="button" className="btn audit-older" disabled={older || list.status !== 'loaded'}
              onClick={() => { if (list.next !== undefined) void loadOlder(list.next) }}>Load older</button>
          )}
        </div>
      </div>
      <footer className="muted">
        {list.skipped > 0 && `${list.skipped} malformed line${list.skipped === 1 ? '' : 's'} skipped · `}
        {list.path && <>Audit file: <code>{list.path}</code></>}
      </footer>
    </section>
  )
}

function AuditDetail({ record: r }: { record: AuditRecord }) {
  const facts: [string, string][] = []
  const add = (label: string, v: unknown) => { if (typeof v === 'string' ? v !== '' : typeof v === 'number') facts.push([label, String(v)]) }
  add('Reason', r.reason)
  add('Client', r.client)
  if (typeof r.timeoutSec === 'number') add('Timeout', `${r.timeoutSec} s`)
  if (typeof r.durationMs === 'number') add('Duration', `${r.durationMs} ms`)
  const redacted = maskedCount(r) > 0 ? Object.entries(r.redacted ?? {}).filter(([, n]) => typeof n === 'number') : []
  return (
    <div className="audit-detail">
      {!r.kind && typeof r.command === 'string' && <pre className="cmd">{displayText(r.command)}</pre>}
      {typeof r.description === 'string' && r.description !== '' && (
        <div className="desc"><span className="desc-label">AI&apos;s description · unverified</span>{displayText(r.description)}</div>
      )}
      {facts.length > 0 && (
        <dl className="audit-facts">{facts.map(([k, v]) => <Fragment key={k}><dt>{k}</dt><dd>{displayText(v)}</dd></Fragment>)}</dl>
      )}
      {redacted.length > 0 && <p className="muted">{'Masked: ' + redacted.map(([k, n]) => `${displayText(k)} ×${n}`).join(', ')}</p>}
      <details>
        <summary>Raw JSON</summary>
        <pre className="cmd">{JSON.stringify(r, null, 2).split('\n').map(displayText).join('\n')}</pre>
      </details>
    </div>
  )
}
```

In `desktop/src/renderer/TerminalTabs.tsx`:

1. Replace the icons and terminals imports with:

```tsx
import { CloseIcon, FolderIcon, HomeIcon, ListIcon, TunnelIcon } from './icons'
import { AUDIT_TAB, Dispatcher, TabSet } from './terminals'
```

and add after `import { TunnelsView } from './TunnelsView'`:

```tsx
import { AuditView } from './AuditView'
```

2. In the props type, replace `tunnels: TunnelView[]; locked: boolean; onTunnelsChanged: () => void; autoHosts: Set<string>` with:

```tsx
  tunnels: TunnelView[]; locked: boolean; onTunnelsChanged: () => void; autoHosts: Set<string>; ready: boolean
```

and in the destructuring replace `tunnels, locked, onTunnelsChanged, autoHosts }, ref) {` with `tunnels, locked, onTunnelsChanged, autoHosts, ready }, ref) {`.

3. Replace `  const home = tabs.active === undefined` with:

```tsx
  const home = tabs.active === undefined
  const audit = tabs.active === AUDIT_TAB
```

4. After the Hosts button (the `<button type="button" className={'hometab' ...>` … `</button>` element), add:

```tsx
        <button type="button" className={'audittab' + (audit ? ' active' : '')} onClick={() => { tabs.showAudit(); changed() }}>
          <ListIcon />Audit
        </button>
```

5. Replace `        <div className="homeview" style={{ display: home ? 'block' : 'none' }}>{homeContent}</div>` with:

```tsx
        <div className="homeview" style={{ display: home ? 'block' : 'none' }}>{homeContent}</div>
        <AuditView visible={audit} ready={ready} servers={servers} />
```

In `desktop/src/renderer/App.tsx`, in the `<Terminals ... />` element, replace `              autoHosts={autoHostsSet} />` with `              autoHosts={autoHostsSet} ready={ready} />`.

In `desktop/src/renderer/styles.css`, replace the three lines

```css
.hometab, .tab { height: 32px; display: flex; align-items: center; gap: 6px; border-radius: 8px 8px 0 0; color: var(--muted); }
.hometab { padding: 0 12px; background: none; border: 0; font-weight: 500; }
.hometab.active, .tab.active { background: var(--bg); color: var(--text); }
```

with:

```css
.hometab, .audittab, .tab { height: 32px; display: flex; align-items: center; gap: 6px; border-radius: 8px 8px 0 0; color: var(--muted); }
.hometab, .audittab { padding: 0 12px; background: none; border: 0; font-weight: 500; }
.hometab.active, .audittab.active, .tab.active { background: var(--bg); color: var(--text); }
```

and append at the end of the file:

```css

/* == audit == */
.auditview { position: absolute; inset: 0; flex-direction: column; background: var(--bg); }
.audit-toolbar { display: flex; align-items: center; gap: 8px; padding: 8px 12px; border-bottom: 1px solid var(--divider); flex-wrap: wrap; }
.audit-host { width: 180px; }
.audit-chips { display: flex; gap: 4px; flex-wrap: wrap; }
.filterchip { height: 28px; padding: 0 10px; border-radius: 14px; border: 1px solid var(--line); background: transparent; color: var(--muted); font-size: 12px; font-weight: 500; }
.filterchip:hover { border-color: var(--muted); }
.filterchip[aria-pressed="true"] { background: var(--accent-tint); color: var(--accent); border-color: var(--accent); }
.audit-toolbar input[type="search"] { flex: 1 1 200px; width: auto; }
.audit-error { padding: 8px 12px 0; }
.audit-body { position: relative; flex: 1; min-height: 0; display: flex; flex-direction: column; }
.audit-scroll { flex: 1; min-height: 0; overflow: auto; padding-bottom: 12px; }
.newpill { position: absolute; top: 8px; left: 50%; transform: translateX(-50%); z-index: 1; box-shadow: var(--shadow-sm); }
.audittable { border-collapse: collapse; width: 100%; table-layout: fixed; }
.audittable td { padding: 6px 8px; border-bottom: 1px solid var(--divider); vertical-align: top; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.auditrow { cursor: default; }
.auditrow:hover { background: var(--raised); }
.auditrow td.when { width: 150px; color: var(--muted); font-variant-numeric: tabular-nums; }
.auditrow td.host { width: 140px; }
.auditrow td.badges { width: 160px; }
.auditrow td.badges .chip + .chip { margin-left: 4px; }
.auditrow td.side { width: 170px; color: var(--muted); text-align: right; font-variant-numeric: tabular-nums; }
.chip.plain { background: var(--raised); color: var(--label); }
.auditdetail td { white-space: normal; background: var(--surface); }
.audit-detail { display: flex; flex-direction: column; gap: 8px; padding: 4px 0; }
.audit-facts { display: grid; grid-template-columns: max-content 1fr; gap: 2px 12px; margin: 0; font-size: 12px; }
.audit-facts dt { color: var(--muted); }
.audit-facts dd { margin: 0; overflow-wrap: anywhere; }
.audit-note { padding: 12px; }
.audit-older { margin: 12px; }
.auditview > footer { padding: 6px 12px; border-top: 1px solid var(--divider); word-break: break-all; }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd desktop && npm run typecheck && npm test`
Expected: PASS.

Run: `cd desktop && npm run build && npx playwright test e2e/audit.spec.ts`
Expected: PASS (1 test).

Run: `cd desktop && npm run e2e`
Expected: PASS, every spec (the others must be unaffected by the new tab; `.tabbar .hometab` still matches one element).

Run: `go vet ./... && go test -race -timeout 900s ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add desktop/e2e/audit.spec.ts desktop/src/renderer/AuditView.tsx desktop/src/renderer/TerminalTabs.tsx \
  desktop/src/renderer/App.tsx desktop/src/renderer/icons.tsx desktop/src/renderer/styles.css
git commit -F - <<'EOF'
feat(desktop): Audit tab after Hosts

Filters (host, kind/outcome chips, search), newest-first rows that expand
to the full command, the AI's description and the raw record, Load older,
live records with an "N new" pill, and a list that is dropped on lock and
read again when the tab is next shown unlocked.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
```

---

### Task 5: README, PRODUCT, CLAUDE.md and ROADMAP

**Files:**
- Modify: `README.md` (section "The desktop app")
- Modify: `PRODUCT.md` (section "Capabilities and Constraints")
- Modify: `CLAUDE.md` (Commands; Hub and bridge; desktop)
- Modify: `docs/superpowers/ROADMAP.md` (the `| 4b |` row; "Findings carried forward")

**Interfaces:**
- Consumes: the behaviour built in Tasks 1-4, as named there.
- Produces: nothing code depends on.

- [ ] **Step 1: README**

In `README.md`, insert after the bullet that starts `- **Tunnels** opens a tab per host for SSH port forwards` (one bullet, one line):

```markdown
- **Audit** is the second fixed tab, after Hosts: the audit log (`audit.jsonl`) newest first, with every AI request and its outcome, auto-allowed runs, host and auto-allow changes, file operations, and tunnels. Filter by host, by the **Exec**, **Auto**, **Denied**, **Config**, **Files** and **Tunnels** chips (several on shows all of them), or by text; click a row for the full command, the AI's description, and the raw record. New records appear while the tab is open. It needs an unlocked vault, forgets what it showed when the vault locks, and changes nothing; the footer shows the file's path.
```

- [ ] **Step 2: PRODUCT.md**

In `PRODUCT.md`, in the "Screens that exist:" bullet, replace `store-error banner.` with:

```markdown
store-error banner, Audit tab (filters, expandable rows, Load older, live updates; read-only).
```

In the Playwright bullet, replace `` `.approval`, `button.allow`, `button.denyall`) `` with:

```markdown
`.approval`, `button.allow`, `button.denyall`, `.audittab`, `tr.auditrow`, `tr.auditdetail`)
```

- [ ] **Step 3: CLAUDE.md**

1. In the Commands block, replace `tunnels, autoallow, and autoallow-mcp` with `tunnels, autoallow, audit, and autoallow-mcp`.
2. In the `sshgate hub` owns … bullet, replace `` `tunnels.stop` (a notification); protocol 7, `ProtocolVersion` in `idle.go`) `` with:

```markdown
`tunnels.stop` (a notification), `audit.read`; the hub also pushes `audit.appended` for every audit record while unlocked; protocol 8, `ProtocolVersion` in `idle.go`)
```

3. In **Hub and bridge**, after the bullet that starts `- Tunnels (\`tunnels.go\`, \`internal/tunnel\`)`, add:

```markdown
- Audit (`audit.go`, `internal/broker/audit.go`): `broker.Audit` counts lines at open (a record's `seq` is its 1-based line number; a torn last line is ended there) and calls `OnAppend` after each write with its lock released. `audit.read {before?, limit?, server?, kinds?, outcomes?, text?}` (strict params; limit 0 means 200, over 500 or negative is -32602; `ErrLocked` while locked; counts as UI activity) scans the whole file newest first (`ponytail:` a backwards reader, with rotation, when the file is large) and returns `{records: [{seq, record}], next?, skipped, path}`; only lines that start with `{` and parse are returned, the rest count in `skipped`. `kinds` and `outcomes` are a union (kind in `kinds`, or an exec record whose outcome is in `outcomes`: `auto` is `approval: "auto"`, `allowed` excludes it, `cancelled` is `approved_but_cancelled` or `cancelled_running`); `server` (exact) and `text` (case-insensitive, with `<`/`>`/`&` decoded) narrow it. `audit.appended {seq, record}` goes out for every record while unlocked. The callback can run with `h.mu` held (`CreateVault`, `SaveServer`, `SetAutoAllow`, grant ends), so it reads only atomics: `h.unlocked`, set wherever `deps.MasterKey` changes (`Unlock`, `CreateVault`, `zeroKeyLocked`, and the test helper `unlockForTest`), and `h.auditSink`. The test helpers `startUI`/`startUIRaw` drop `audit.appended`; `startTermDoor` keeps it. No MCP-door access.
```

4. In the **`desktop/`** section, after the bullet that starts `- Tunnels tab per host`, add:

```markdown
- Audit tab (`AuditView.tsx`, `audit.ts`): a fixed tab after `⌂ Hosts` (`.audittab`; `TabSet.active === AUDIT_TAB`), always mounted, so filters, scroll (saved by hand: `display:none` drops it) and expanded rows survive tab switches. It calls `audit.read` only when shown with `ready` and an `idle` list, or on a filter change, Enter or 300 ms after typing in search, Refresh, or Load older. `audit.appended` records that match the shown query (`matches`, the hub's rule mirrored) merge by `seq`, held behind an "N new" pill while scrolled away. `locked` sets the list to `cleared`, which never loads until `ready` changes, so no hub call follows a notification.
```

- [ ] **Step 4: ROADMAP**

In `docs/superpowers/ROADMAP.md`, read the table row that starts with `| 4b |` (slice 4a's docs split row 4 into 4a and 4b). Replace only its Status cell (the third cell) with the text below, using today's date from `date +%F`:

```markdown
Built YYYY-MM-DD: Audit tab, `audit.read`, `audit.appended` (UI-door protocol 8); exit gate pending: on the author's real vault the tab shows the day's AI requests with outcomes, Auto filters to auto-allowed runs only, and a new request appears while the tab is open
```

If there is no `| 4b |` row, stop and report it to the controller: slice 4a's docs were meant to add it.

Under `## Findings carried forward`, add as the last bullet:

```markdown
- 4b → later (2026-09-30): audit rotation stays deferred. Trigger: the audit file is large enough that `audit.read`'s whole-file scan shows in the Audit tab; rotation then comes with a backwards reader from EOF (the `ponytail:` note on `broker.Audit.Read`). Not planned: export (the file is already JSONL, and the tab's footer shows its path), viewing while locked, and MCP-door access (the AI never reads the audit log).
```

- [ ] **Step 5: Check and commit**

Run: `git diff --stat` and confirm only the four files above changed in this task.
Run: `go vet ./... && cd desktop && npm run typecheck && npm test`
Expected: PASS (docs-only change; a guard).

```bash
git add README.md PRODUCT.md CLAUDE.md docs/superpowers/ROADMAP.md
git commit -F - <<'EOF'
docs: slice 4b audit viewer

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
```

---

## Self-review

**Spec coverage.**
- Audit tab, fixed, second, list icon, not closable, keeps state: Task 3 (`AUDIT_TAB`, `showAudit`), Task 4 (button, `ListIcon`, always-mounted view, scroll restore).
- `audit.read` with `before`/`limit`/`server`/`kinds`/`outcomes`/`text`, `next`, `skipped`, strict params, `ErrLocked`, activity, limit bounds: Task 1 (`Read`), Task 2 (`auditQuery`, `ReadAudit`, `req`).
- `audit.appended` for every kind, only while unlocked, never waits on the renderer: Task 2 (`OnAppend` → `auditAppended`, tests for exec, config, file, tunnel, locked).
- Protocol 8 in three places: Task 2. MCP door unchanged: nothing touches `mcpdoor.go`.
- Line counter, `OnAppend` with `a.mu` released, `Read` under `a.mu`, `ponytail:` whole-file scan: Task 1.
- Filter bar (host select, six chips, search with Enter/300 ms, Refresh), table columns, badges, main text, side (`exit N`, wait, `N masked` from 4a's `redacted`): Task 3 (`rowView`, `toQuery`), Task 4 (view).
- Expanded row: full command, "AI's description · unverified", reason/client/timeout/duration, `redacted` by kind, raw JSON collapsed: Task 4 (`AuditDetail`).
- Load older, footer (skipped, path), live insert, "N new" pill, dedupe by `seq`, clear on lock, reload when next shown after unlock, no hub call from notifications/timers: Task 3 (list functions), Task 4 (view wiring).
- `REQUEST_METHODS` gains `audit.read`, `NOTIFY_METHODS` unchanged, calls only through `transport.ts`: Task 3.
- Testing section: broker (Task 1), hub (Task 2), vitest (Task 3), e2e (Task 4), checks (every task).
- ROADMAP, README, PRODUCT, CLAUDE changes: Task 5.

**Placeholder scan.** No TBD/TODO; every code step has complete code. The only value filled at run time is the ROADMAP date (`date +%F`).

**Type consistency.** `ReadQuery`/`ReadResult`/`Entry`/`MaxReadLimit`/`OnAppend` (Task 1) match their uses in Task 2. The wire shape `{records: [{seq, record}], next?, skipped, path}` matches `AuditPage`/`AuditEntry` (Task 3). `AuditList`, `EMPTY`, `startLoad`, `applyPage`, `failLoad`, `appendLive`, `releaseHeld`, `clearOnLock`, `maskedCount`, `rowView`, `CHIPS`, `toggleChip`, `toQuery`, `NO_FILTERS` (Task 3) match their imports in `AuditView.tsx` (Task 4). `Terminals`' new `ready` prop is passed by `App.tsx` in the same task.
