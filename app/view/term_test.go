package view

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/termconn"
)

// rec is a Conn that records writes and marks, for the IME and clipboard
// assertions of desktop/e2e/ime.spec.ts and clipboard.spec.ts.
type rec struct {
	mu     sync.Mutex
	writes []string
	marks  int
	block  chan struct{}
}

func (r *rec) Read(p []byte) (int, error) { <-r.block; return 0, os.ErrClosed }
func (r *rec) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.writes = append(r.writes, string(p))
	r.mu.Unlock()
	return len(p), nil
}
func (r *rec) Close() error { return nil }

// waitWrites waits up to 2 s for ok(everything written so far): the terminal
// writes its input from a queue goroutine, not during the frame. Once ok holds
// it waits another 100 ms, so a late duplicate write lands before the caller
// asserts on the settled writes.
func (r *rec) waitWrites(ok func(string) bool) {
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		r.mu.Lock()
		done := ok(strings.Join(r.writes, ""))
		r.mu.Unlock()
		if done {
			time.Sleep(100 * time.Millisecond)
			return
		}
	}
}

func needLib(t *testing.T) {
	if os.Getenv("MYGO_GHOSTTY_VT") == "" {
		t.Skip("MYGO_GHOSTTY_VT unset: run scripts/build-ghostty-vt.sh; never download egoist's build")
	}
}

func TestIMECommitSentOnceAsUser(t *testing.T) {
	needLib(t)
	r := &rec{block: make(chan struct{})}
	term, err := terminal.New(terminal.Options{Conn: r, OnInput: func(string) { r.mu.Lock(); r.marks++; r.mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	tt := ui.NewTester(func(c *ui.Context) { terminal.View(c, term).Fill().AutoFocus() }, 400, 200)
	tt.Frame()
	tt.Compose("tiê", 3)
	tt.Compose("tiến", 4)
	tt.Type("tiếng")
	tt.Frame()
	r.waitWrites(func(got string) bool { return len(got) >= len("tiếng") })
	r.mu.Lock()
	defer r.mu.Unlock()
	if got := strings.Join(r.writes, ""); got != "tiếng" {
		t.Fatalf("sent %q, want the committed word once", got)
	}
	if r.marks != 1 {
		t.Fatalf("OnInput marked %d times", r.marks)
	}
}

func TestCopyAndPaste(t *testing.T) {
	needLib(t)
	r := &rec{block: make(chan struct{})}
	pasted := 0
	term, err := terminal.New(terminal.Options{Conn: r, OnPaste: func(string) bool { pasted++; return false }})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	term.Feed([]byte("hello world"))
	tt := ui.NewTester(func(c *ui.Context) { terminal.View(c, term).Fill().AutoFocus() }, 400, 200)
	tt.Frame()
	tt.Command("selectAll")
	tt.Command("copy")
	if !strings.Contains(tt.Clipboard(), "hello world") {
		t.Fatalf("clipboard %q", tt.Clipboard())
	}
	tt.SetClipboard("pasted text")
	tt.Command("paste")
	tt.Frame()
	r.waitWrites(func(got string) bool { return strings.Contains(got, "pasted text") })
	r.mu.Lock()
	defer r.mu.Unlock()
	if !strings.Contains(strings.Join(r.writes, ""), "pasted text") || pasted != 1 {
		t.Fatalf("writes %q, OnPaste %d", r.writes, pasted)
	}
}

// hubDoor is the hub side of a termconn.Conn: it answers term.open and
// records every notification, so the tests see what the hub would receive.
type hubDoor struct {
	mu     sync.Mutex
	writes []hubWrite
	calls  []string
}

type hubWrite struct {
	data string
	user bool
}

func (d *hubDoor) Call(_ context.Context, method string, _, result any) error {
	d.mu.Lock()
	d.calls = append(d.calls, method)
	d.mu.Unlock()
	if method == "term.open" {
		return json.Unmarshal([]byte(`{"status":"open"}`), result)
	}
	return nil
}

func (d *hubDoor) Notify(method string, params any) error {
	if method == "term.write" {
		p := params.(map[string]any)
		d.mu.Lock()
		d.writes = append(d.writes, hubWrite{string(p["data"].([]byte)), p["user"].(bool)})
		d.mu.Unlock()
	}
	return nil
}

func (d *hubDoor) got() (data string, allUser, noneUser bool, n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	allUser, noneUser = true, true
	for _, w := range d.writes {
		data += w.data
		allUser = allUser && w.user
		noneUser = noneUser && !w.user
	}
	return data, allUser, noneUser, len(d.writes)
}

// waitData waits up to 2 s for ok(all data sent), then 100 ms more so a late
// duplicate lands before the caller asserts.
func (d *hubDoor) waitData(ok func(string) bool) {
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if data, _, _, _ := d.got(); ok(data) {
			time.Sleep(100 * time.Millisecond)
			return
		}
	}
}

func (d *hubDoor) closed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if c == "term.close" {
			return true
		}
	}
	return false
}

// hubTerm is the app's terminal as main builds it (TerminalOptions) over a
// real termconn.Conn, in a headless window.
func hubTerm(t *testing.T) (*terminal.Terminal, *ui.Tester, *hubDoor) {
	t.Helper()
	d := &hubDoor{}
	conn, err := termconn.Open(context.Background(), d, termconn.NewMux(), "t1", "box", 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	term, err := terminal.New(TerminalOptions(conn))
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { term.Close() })
	tt := ui.NewTester(func(c *ui.Context) { terminal.View(c, term).Fill().AutoFocus() }, 400, 200)
	tt.Frame()
	return term, tt, d
}

// Ports of desktop/e2e/ime.spec.ts and clipboard.spec.ts: what the hub gets
// from term.write, and with which user flag (the idle lock counts only user:true).

func TestHubIMECommitIsOneUserWrite(t *testing.T) {
	needLib(t)
	_, tt, d := hubTerm(t)
	tt.Compose("tiê", 3)
	tt.Compose("tiến", 4)
	tt.Type("tiếng")
	tt.Frame()
	d.waitData(func(s string) bool { return len(s) >= len("tiếng") })
	data, allUser, _, n := d.got()
	if data != "tiếng" || n != 1 || !allUser {
		t.Fatalf("hub got %q in %d writes (all user: %v), want %q once as user input", data, n, allUser, "tiếng")
	}
}

func TestHubTypedKeyIsUserAndQueryReplyIsNot(t *testing.T) {
	needLib(t)
	term, tt, d := hubTerm(t)
	term.Feed([]byte("\x1b[c")) // a device-attributes query: the terminal answers it by itself
	tt.Frame()
	d.waitData(func(s string) bool { return s != "" })
	data, _, noneUser, n := d.got()
	if n == 0 || !noneUser {
		t.Fatalf("query reply: %q in %d writes, want at least one, none as user input", data, n)
	}
	before := n
	tt.Type("k")
	tt.Frame()
	d.waitData(func(s string) bool { return strings.HasSuffix(s, "k") })
	d.mu.Lock()
	defer d.mu.Unlock()
	if last := d.writes[len(d.writes)-1]; len(d.writes) <= before || last.data != "k" || !last.user {
		t.Fatalf("typed key: writes %+v", d.writes)
	}
}

func TestHubPasteIsUserWrite(t *testing.T) {
	needLib(t)
	_, tt, d := hubTerm(t)
	tt.SetClipboard("pasted text")
	tt.Command("paste")
	tt.Frame()
	d.waitData(func(s string) bool { return strings.Contains(s, "pasted text") })
	data, allUser, _, _ := d.got()
	if !strings.Contains(data, "pasted text") || !allUser {
		t.Fatalf("hub got %q (all user: %v)", data, allUser)
	}
}

func TestHubCopySendsNothing(t *testing.T) {
	needLib(t)
	term, tt, d := hubTerm(t)
	term.Feed([]byte("hello world"))
	tt.Frame()
	tt.Command("selectAll")
	tt.Command("copy")
	tt.Frame()
	if !strings.Contains(tt.Clipboard(), "hello world") {
		t.Fatalf("clipboard %q", tt.Clipboard())
	}
	d.waitData(func(string) bool { return false }) // the full 2 s: nothing may arrive
	if data, _, _, n := d.got(); n != 0 {
		t.Fatalf("copy sent %q to the hub", data)
	}
}

func TestHubControlKeysReachTheShell(t *testing.T) {
	needLib(t)
	term, tt, d := hubTerm(t)
	term.Feed([]byte("keep me"))
	for end := time.Now().Add(2 * time.Second); !strings.Contains(term.Text(), "keep me") && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
	}
	ctrl := ui.Ctrl // Ctrl on every platform; Cmd is the macOS copy/paste modifier
	for _, k := range []ui.Key{ui.KeyC, ui.KeyV, ui.KeyD, ui.KeyW, ui.KeyR} {
		tt.Key(ctrl, k)
	}
	tt.Frame()
	const want = "\x03\x16\x04\x17\x12"
	d.waitData(func(s string) bool { return len(s) >= len(want) })
	data, allUser, _, _ := d.got()
	if data != want || !allUser {
		t.Fatalf("hub got %q (all user: %v), want %q as user input", data, allUser, want)
	}
	// Ctrl+W did not close the tab and Ctrl+R did not reload: it still shows its output.
	select {
	case <-term.Done():
		t.Fatal("the terminal ended")
	default:
	}
	if d.closed() || !strings.Contains(term.Text(), "keep me") {
		t.Fatalf("hub terminal closed (%v) or the screen lost its output: %q", d.closed(), term.Text())
	}
}

// closeRec reports when the terminal closes its Conn.
type closeRec struct {
	*rec
	once   sync.Once
	closed chan struct{}
}

func (r *closeRec) Close() error { r.once.Do(func() { close(r.closed); close(r.block) }); return nil }

// TestHubStopClosesItsTerminal (M4): a stopped hub's terminal is closed and
// its page left, so it does not come back after the restart.
func TestHubStopClosesItsTerminal(t *testing.T) {
	needLib(t)
	r := &closeRec{rec: &rec{block: make(chan struct{})}, closed: make(chan struct{})}
	term, err := terminal.New(terminal.Options{Conn: r})
	if err != nil {
		t.Fatal(err)
	}
	a, tt, _ := readyApp(t, nil)
	a.Update(func() { a.term, a.termServer, a.page = term, "deadbox", "term" })
	tt.flush()
	if !tt.HasText("deadbox") {
		t.Fatal(tt.Texts())
	}
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", Attempt: 1, In: time.Second}) })
	tt.flush()
	if a.term != nil || a.page != "home" {
		t.Fatalf("term kept %v page %q after the hub stopped", a.term != nil, a.page)
	}
	select {
	case <-r.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the dead hub's terminal was not closed")
	}
	running(a, tt)
	if tt.HasText("deadbox") || !tt.HasText("Hosts · 0") {
		t.Fatal("the dead terminal came back:", tt.Texts())
	}
}
