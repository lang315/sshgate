package broker

import (
	"bufio"
	"bytes"
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
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v", info.Mode().Perm())
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
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

// The new auto-allow fields must stay out of every ordinary record's JSON.
func TestAuditRecordOmitsEmptyAutoFields(t *testing.T) {
	ab, err := json.Marshal(AuditRecord{})
	if err != nil {
		t.Fatal(err)
	}
	var am map[string]any
	if err := json.Unmarshal(ab, &am); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"approval", "waitMs"} {
		if _, ok := am[name]; ok {
			t.Fatalf("AuditRecord{} JSON has %q: %s", name, ab)
		}
	}

	cb, err := json.Marshal(ConfigRecord{})
	if err != nil {
		t.Fatal(err)
	}
	var cm map[string]any
	if err := json.Unmarshal(cb, &cm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"until", "forever", "reason"} {
		if _, ok := cm[name]; ok {
			t.Fatalf("ConfigRecord{} JSON has %q: %s", name, cb)
		}
	}
}

func TestOpenAuditTightensExistingFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", info.Mode().Perm())
	}
}

func TestAuditConfigRecordShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.WriteConfig(ConfigRecord{Time: time.Now(), Action: "trust", Server: "s", Host: "h", Port: 22, Fingerprint: "SHA256:x", Algo: "ssh-ed25519"}); err != nil {
		t.Fatal(err)
	}
	a.Close()
	raw, _ := os.ReadFile(path)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["kind"] != "config" || m["action"] != "trust" || m["fingerprint"] != "SHA256:x" || m["algo"] != "ssh-ed25519" {
		t.Fatalf("got %v", m)
	}
	if _, has := m["command"]; has {
		t.Fatal("a config record carries exec fields")
	}
}

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
			t.Errorf("%s: skipped %d, want 1 (the scan reached the start of the file)", c.name, res.Skipped)
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

// Text matches a record's decoded string and number values, not its raw
// line: quotes, backslashes and tabs match as typed, key names never do.
func TestAuditReadTextMatchesDecodedValues(t *testing.T) {
	a, _ := openTestAudit(t)
	for _, c := range []string{`echo "hi"`, `dir C:\Users`, "a\tb", "plain"} {
		if err := a.Write(AuditRecord{Server: "box", Command: c, Outcome: "allowed", TimeoutSec: 30}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		text string
		want []int
	}{
		{`echo "hi"`, []int{1}},
		{`C:\Users`, []int{2}},
		{"a\tb", []int{3}},
		{"30", []int{4, 3, 2, 1}}, // a number value
		{"command", []int{}},      // a key name
		{"server", []int{}},
		{"box", []int{4, 3, 2, 1}},
	} {
		res, err := a.Read(ReadQuery{Text: c.text})
		if err != nil {
			t.Fatal(err)
		}
		if got := seqsOf(res); !slices.Equal(got, c.want) {
			t.Errorf("text %q: seqs %v, want %v", c.text, got, c.want)
		}
	}
}

// A change to the file that bypasses Audit (an appended line, a truncation)
// must not leave the live seq different from the line number Read reports.
func TestAuditSeqSurvivesExternalEdits(t *testing.T) {
	a, path := openTestAudit(t)
	var seqs []int
	a.OnAppend(func(seq int, _ json.RawMessage) { seqs = append(seqs, seq) })
	write := func(c string) {
		t.Helper()
		if err := a.Write(AuditRecord{Command: c, Outcome: "allowed"}); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(flag int, data string) {
		t.Helper()
		f, err := os.OpenFile(path, flag, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(data); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	check := func(cmd string, want int) {
		t.Helper()
		res, err := a.Read(ReadQuery{Text: cmd})
		if err != nil || len(res.Records) != 1 {
			t.Fatalf("read %q: %v %v", cmd, res, err)
		}
		if got := seqs[len(seqs)-1]; got != want || res.Records[0].Seq != want {
			t.Fatalf("%s: live seq %d, Read seq %d, want %d", cmd, got, res.Records[0].Seq, want)
		}
	}
	write("one")
	edit(os.O_APPEND|os.O_WRONLY, "{\"command\":\"hand\"}\ntorn")
	write("two")
	check("two", 4) // 1 one, 2 hand, 3 torn (ended), 4 two
	edit(os.O_TRUNC|os.O_WRONLY, "{\"a\":1}\n")
	write("three")
	check("three", 2) // 1 a, 2 three
}

// The line count streams the file in chunks: a file far larger than the
// buffer, ending mid-line, counts right and its torn line is ended.
func TestOpenAuditCountsALargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	line := `{"command":"` + strings.Repeat("x", 1000) + `"}` + "\n"
	if err := os.WriteFile(path, []byte(strings.Repeat(line, 300)+"torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Write(AuditRecord{Command: "next", Outcome: "allowed"}); err != nil {
		t.Fatal(err)
	}
	res, _ := a.Read(ReadQuery{Limit: 1})
	if got := seqsOf(res); !slices.Equal(got, []int{302}) {
		t.Fatalf("seqs %v, want [302]", got)
	}
}

// A file record's local paths never leave the broker: Read and OnAppend omit
// them and a text search cannot find them; the file on disk keeps them.
func TestAuditFileLocalPathsStayInTheBroker(t *testing.T) {
	a, path := openTestAudit(t)
	var appended json.RawMessage
	a.OnAppend(func(_ int, line json.RawMessage) { appended = line })
	if err := a.WriteFile(FileRecord{Server: "box", Action: "upload", Remote: []string{"/r/a"}, Local: []string{"/Users/me/secret/a"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(appended), "local") || !strings.Contains(string(appended), "/r/a") {
		t.Fatalf("appended: %s", appended)
	}
	for text, want := range map[string]int{"me/secret": 0, "/r/a": 1} {
		res, err := a.Read(ReadQuery{Text: text})
		if err != nil || len(res.Records) != want {
			t.Fatalf("text %q: %d records, %v; want %d", text, len(res.Records), err, want)
		}
		for _, e := range res.Records {
			if strings.Contains(string(e.Record), "local") {
				t.Fatalf("Read returned %s", e.Record)
			}
		}
	}
	if disk, _ := os.ReadFile(path); !strings.Contains(string(disk), "/Users/me/secret/a") {
		t.Fatalf("disk lost local: %s", disk)
	}
}

// A softLock record names its hosts in servers, not server; filtering the log
// to one of them must still find it.
func TestAuditReadServerMatchesSoftLockServers(t *testing.T) {
	a, _ := openTestAudit(t)
	if err := a.WriteConfig(ConfigRecord{Action: "softLock", Servers: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	for server, want := range map[string][]int{"a": {1}, "b": {1}, "c": {}} {
		res, err := a.Read(ReadQuery{Server: server})
		if err != nil {
			t.Fatal(err)
		}
		if got := seqsOf(res); !slices.Equal(got, want) {
			t.Errorf("server %q: seqs %v, want %v", server, got, want)
		}
	}
}

// rawAudit opens an audit log holding exactly these lines.
func rawAudit(t *testing.T, lines []string) *Audit {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// Read walks the file backwards in 64 KiB chunks: a line longer than a chunk,
// and lines cut by a chunk edge, must come back whole and in order.
func TestAuditReadLinesAcrossChunks(t *testing.T) {
	var lines []string
	for i := 1; i <= 300; i++ {
		pad := 60
		if i%50 == 0 {
			pad = 150 << 10
		}
		lines = append(lines, fmt.Sprintf(`{"command":"%d-%s","outcome":"allowed"}`, i, strings.Repeat("x", pad)))
	}
	a := rawAudit(t, lines)
	res, err := a.Read(ReadQuery{Limit: MaxReadLimit})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != 300 || res.Skipped != 0 || res.Next != 0 {
		t.Fatalf("%d records, skipped %d, next %d", len(res.Records), res.Skipped, res.Next)
	}
	for i, e := range res.Records {
		if want := 300 - i; e.Seq != want || string(e.Record) != lines[want-1] {
			t.Fatalf("record %d: seq %d, %d bytes; want seq %d, %d bytes", i, e.Seq, len(e.Record), want, len(lines[want-1]))
		}
	}
}

// Skipped covers only the span a call scanned, so paging through the file
// returns every record once and the pages' counts add up to the file's.
func TestAuditReadPagesAddUpSkipped(t *testing.T) {
	var lines []string
	var valid []int
	junk := 0
	for i := 1; i <= 40; i++ {
		if i%3 == 0 || i > 38 || i < 3 {
			lines = append(lines, "junk")
			junk++
			continue
		}
		lines = append(lines, `{"command":"x","outcome":"allowed"}`)
		valid = append(valid, i)
	}
	a := rawAudit(t, lines)
	var got []int
	skipped, before := 0, 0
	for page := 0; ; page++ {
		res, err := a.Read(ReadQuery{Limit: 4, Before: before})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, seqsOf(res)...)
		skipped += res.Skipped
		if before = res.Next; before == 0 {
			break
		}
		if page > 40 {
			t.Fatal("paging never ended")
		}
	}
	slices.Reverse(got)
	if !slices.Equal(got, valid) || skipped != junk {
		t.Fatalf("seqs %v, skipped %d; want %v, %d", got, skipped, valid, junk)
	}
}

type countingReaderAt struct {
	r *bytes.Reader
	n int
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.n += n
	return n, err
}

// The point of reading backwards: the newest lines cost one chunk, however
// large the file is.
func TestLinesBackwardReadsOnlyWhatItNeeds(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef\n"), 1<<16) // about 1.1 MiB
	c := &countingReaderAt{r: bytes.NewReader(data)}
	var got []string
	err := linesBackward(c, int64(len(data)), func(line []byte) bool {
		got = append(got, string(line))
		return len(got) < 10
	})
	if err != nil || len(got) != 10 || got[9] != "0123456789abcdef" {
		t.Fatalf("err %v, lines %q", err, got)
	}
	if c.n > 64<<10 {
		t.Fatalf("read %d bytes for 10 lines", c.n)
	}
	// What follows the last newline is not a line; the first line needs none before it.
	got = nil
	if err := linesBackward(strings.NewReader("a\n\nb\npartial"), 12, func(line []byte) bool {
		got = append(got, string(line))
		return true
	}); err != nil || !slices.Equal(got, []string{"b", "", "a"}) {
		t.Fatalf("err %v, lines %q", err, got)
	}
}

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
