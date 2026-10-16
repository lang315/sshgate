package termconn

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/door"
	"github.com/lang315/sshgate/app/hubapi"
)

// fakeDoor records notifications and answers term.open.
type fakeDoor struct {
	mu    sync.Mutex
	notes []struct {
		method string
		params map[string]any
	}
	openResult string // term.open's reply; {"status":"open"} when empty
	openParams string
	openErr    error
	fail       bool
	calls      []string
}

func (f *fakeDoor) Call(_ context.Context, method string, params, result any) error {
	f.mu.Lock()
	f.calls = append(f.calls, method)
	f.mu.Unlock()
	if method == "term.open" {
		b, _ := json.Marshal(params)
		f.mu.Lock()
		f.openParams = string(b)
		res := f.openResult
		f.mu.Unlock()
		if f.openErr != nil {
			return f.openErr
		}
		if res == "" {
			res = `{"status":"open"}`
		}
		return json.Unmarshal([]byte(res), result)
	}
	return nil
}

func (f *fakeDoor) Notify(method string, params any) error {
	b, _ := json.Marshal(params)
	var m map[string]any
	json.Unmarshal(b, &m)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notes = append(f.notes, struct {
		method string
		params map[string]any
	}{method, m})
	if f.fail {
		return io.ErrClosedPipe
	}
	return nil
}

func (f *fakeDoor) last(method string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.notes) - 1; i >= 0; i-- {
		if f.notes[i].method == method {
			return f.notes[i].params
		}
	}
	return nil
}

func open(t *testing.T, d *fakeDoor) (*Conn, *Mux) {
	t.Helper()
	m := NewMux()
	c, err := Open(context.Background(), d, m, "t1", "box", 24, 80, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c, m
}

func note(m *Mux, method string, params any) bool {
	b, _ := json.Marshal(params)
	return m.Note(method, b)
}

func TestReadAcksWhatItReturns(t *testing.T) {
	d := &fakeDoor{}
	c, m := open(t, d)
	note(m, "term.data", map[string]any{"id": "t1", "data": []byte("hello world")})
	buf := make([]byte, 5)
	n, err := c.Read(buf)
	if err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("read %q, %v", buf[:n], err)
	}
	if a := d.last("term.ack"); a == nil || a["n"].(float64) != 5 || a["id"] != "t1" {
		t.Fatalf("ack %v", a)
	}
	n, _ = c.Read(make([]byte, 64))
	if n != 6 {
		t.Fatalf("second read %d bytes", n)
	}
}

// readAll reads until EOF, as the terminal's read loop does.
func readAll(t *testing.T, c *Conn) string {
	t.Helper()
	var got []byte
	buf := make([]byte, 64)
	for {
		n, err := c.Read(buf)
		got = append(got, buf[:n]...)
		if err == io.EOF {
			return string(got)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func (f *fakeDoor) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, x := range f.notes {
		if x.method == method {
			n++
		}
	}
	return n
}

func TestExitEndsReadWithTheReason(t *testing.T) {
	d := &fakeDoor{}
	c, m := open(t, d)
	note(m, "term.data", map[string]any{"id": "t1", "data": []byte("bye")})
	note(m, "term.exit", map[string]any{"id": "t1", "code": -1, "reason": "lost\x1b]0;pwn\x07 \u009b2J"})
	note(m, "term.exit", map[string]any{"id": "t1", "code": 0, "reason": "again"})
	if got := readAll(t, c); got != "bye\r\n[exited: lost]0;pwn 2J]\r\n" {
		t.Fatalf("read %q: the reason goes after the output, once, with no control characters", got)
	}
	if c.ExitReason() != "lost\x1b]0;pwn\x07 \u009b2J" {
		t.Fatalf("reason %q", c.ExitReason())
	}
	if n := d.count("term.ack"); n != 1 {
		t.Fatalf("%d acks: only the hub's bytes are acked, not the app's line", n)
	}
}

func TestExitWithoutReasonShowsTheCode(t *testing.T) {
	c, m := open(t, &fakeDoor{})
	note(m, "term.exit", map[string]any{"id": "t1", "code": 0, "reason": ""})
	if got := readAll(t, c); got != "\r\n[exited: code 0]\r\n" {
		t.Fatalf("read %q", got)
	}
}

func TestEndAsksNothingOfTheHub(t *testing.T) {
	d := &fakeDoor{}
	c, m := open(t, d)
	c.End("hub restarted")
	if got := readAll(t, c); got != "\r\n[exited: hub restarted]\r\n" {
		t.Fatalf("read %q", got)
	}
	c.Close()
	note(m, "term.data", map[string]any{"id": "t1", "data": []byte("late")}) // dropped: End unregistered it
	if len(d.calls) != 1 {
		t.Fatalf("calls %v: an ended terminal is gone from the hub, nothing to close", d.calls)
	}
}

func TestClosingAnExitedTerminalSendsNoClose(t *testing.T) {
	d := &fakeDoor{}
	c, m := open(t, d)
	note(m, "term.exit", map[string]any{"id": "t1", "code": 0, "reason": "closed"})
	c.Close()
	if len(d.calls) != 1 {
		t.Fatalf("calls %v, want only term.open", d.calls)
	}
}

func TestOutputClosesAtTheFirstData(t *testing.T) {
	c, m := open(t, &fakeDoor{})
	select {
	case <-c.Output():
		t.Fatal("Output closed before any data")
	default:
	}
	note(m, "term.data", map[string]any{"id": "t1", "data": []byte("$ ")})
	note(m, "term.data", map[string]any{"id": "t1", "data": []byte("more")}) // closing twice would panic
	select {
	case <-c.Output():
	default:
		t.Fatal("Output still open after data")
	}
}

func TestWriteUserWindow(t *testing.T) {
	d := &fakeDoor{}
	c, _ := open(t, d)
	now := time.Unix(100, 0)
	c.now = func() time.Time { return now }
	c.Write([]byte("\x1b[?1;2c")) // a query reply: no input before it
	if d.last("term.write")["user"] != false {
		t.Fatal("query reply sent as user input")
	}
	c.MarkUser()
	now = now.Add(999 * time.Millisecond)
	c.Write([]byte("a"))
	if d.last("term.write")["user"] != true {
		t.Fatal("typed key not sent as user input")
	}
	now = now.Add(time.Millisecond) // exactly 1000 ms after the mark: no longer the user's
	c.Write([]byte("b"))
	if d.last("term.write")["user"] != false {
		t.Fatal("user flag outlived the window")
	}
}

func TestWriteNeverFails(t *testing.T) {
	d := &fakeDoor{fail: true}
	c, _ := open(t, d)
	n, err := c.Write([]byte("abc"))
	if n != 3 || err != nil {
		t.Fatalf("Write = %d, %v; mygo closes its input for good on an error", n, err)
	}
}

func TestResizeSwapsAndClamps(t *testing.T) {
	d := &fakeDoor{}
	c, _ := open(t, d)
	for _, tc := range []struct{ cols, rows, wantRows, wantCols int }{
		{120, 40, 40, 120},
		{0, 0, 1, 1},
		{5000, 5000, 1000, 1000},
	} {
		if err := c.Resize(tc.cols, tc.rows); err != nil {
			t.Fatal(err)
		}
		r := d.last("term.resize")
		if int(r["rows"].(float64)) != tc.wantRows || int(r["cols"].(float64)) != tc.wantCols {
			t.Errorf("Resize(%d, %d) sent %v", tc.cols, tc.rows, r)
		}
	}
}

func TestMuxDropsUnknownID(t *testing.T) {
	m := NewMux()
	if !note(m, "term.data", map[string]any{"id": "gone", "data": []byte("x")}) {
		t.Fatal("term.data for an unknown id not taken")
	}
	if note(m, "pending", map[string]any{}) {
		t.Fatal("took a note that is not term.*")
	}
}

func TestCloseSendsTermCloseOnce(t *testing.T) {
	d := &fakeDoor{}
	c, m := open(t, d)
	c.Close()
	c.Close()
	n := 0
	for _, x := range d.calls {
		if x == "term.close" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("term.close called %d times", n)
	}
	if d.last("term.close") != nil {
		t.Fatal("term.close sent as a notification; the hub ignores it")
	}
	note(m, "term.data", map[string]any{"id": "t1", "data": []byte("late")}) // dropped, no panic
	if _, err := c.Read(make([]byte, 4)); err != io.EOF {
		t.Fatalf("read after close: %v", err)
	}
}

func TestDroppedCountsAndShows(t *testing.T) {
	d := &fakeDoor{}
	c, m := open(t, d)
	note(m, "term.dropped", map[string]any{"id": "t1", "bytes": 7})
	if c.Dropped() != 7 {
		t.Fatalf("dropped %d", c.Dropped())
	}
	note(m, "term.exit", map[string]any{"id": "t1", "code": 0, "reason": "closed"})
	if got := readAll(t, c); got != "\r\n[input dropped: 7 bytes]\r\n\r\n[exited: closed]\r\n" {
		t.Fatalf("read %q", got)
	}
}

func TestOpenRefusedStatusCarriesTheKey(t *testing.T) {
	d := &fakeDoor{openResult: `{"status":"hostKeyUnknown","server":"box","host":"127.0.0.1","port":2222,"user":"test","fingerprint":"SHA256:abc","keyType":"ssh-ed25519","knownHosts":"absent"}`}
	m := NewMux()
	_, err := Open(context.Background(), d, m, "t1", "box", 24, 80, nil)
	oe, ok := err.(*OpenError)
	want := hubapi.HostKey{Status: "hostKeyUnknown", Server: "box", Host: "127.0.0.1", Port: 2222, User: "test", Fingerprint: "SHA256:abc", KeyType: "ssh-ed25519", KnownHosts: "absent"}
	if !ok || oe.Status != "hostKeyUnknown" || oe.Key != want {
		t.Fatalf("err %#v", err)
	}
	// The hub released the id: the retry opens with it, trusting the key.
	d.openResult = ""
	if _, err := Open(context.Background(), d, m, "t1", "box", 24, 80, &hubapi.Trust{Fingerprint: "SHA256:abc", KeyType: "ssh-ed25519"}); err != nil {
		t.Fatal(err)
	}
	if d.openParams != `{"cols":80,"id":"t1","rows":24,"server":"box","trustHostKey":{"fingerprint":"SHA256:abc","keyType":"ssh-ed25519"}}` {
		t.Fatal(d.openParams)
	}
}

func TestOpenUnexpectedReplyIsAClearError(t *testing.T) {
	for _, res := range []string{`{}`, `null`, `{"status":""}`, `{"status":"weird"}`, `[1]`, `{"status":7}`} {
		d := &fakeDoor{openResult: res}
		m := NewMux()
		_, err := Open(context.Background(), d, m, "t1", "box", 24, 80, nil)
		var oe *OpenError
		if err == nil || errors.As(err, &oe) || err.Error() != "terminal not opened: unexpected reply" {
			t.Fatalf("reply %s: err %#v", res, err)
		}
		// The id is free again.
		d.openResult = ""
		if _, err := Open(context.Background(), d, m, "t1", "box", 24, 80, nil); err != nil {
			t.Fatalf("reply %s: reopen: %v", res, err)
		}
	}
}

func TestOpenDuplicateIDKeepsFirst(t *testing.T) {
	d := &fakeDoor{}
	c, m := open(t, d)
	if _, err := Open(context.Background(), d, m, "t1", "box", 24, 80, nil); err == nil {
		t.Fatal("second Open with a live id succeeded")
	}
	for _, x := range d.calls {
		if x == "term.close" {
			t.Fatal("duplicate Open closed the first terminal")
		}
	}
	note(m, "term.data", map[string]any{"id": "t1", "data": []byte("hi")})
	buf := make([]byte, 8)
	if n, err := c.Read(buf); err != nil || string(buf[:n]) != "hi" {
		t.Fatalf("first conn lost its registration: %q, %v", buf[:n], err)
	}
}

func TestOpenCancelledClosesHubTerminal(t *testing.T) {
	d := &fakeDoor{openErr: context.Canceled}
	m := NewMux()
	if _, err := Open(context.Background(), d, m, "t1", "box", 24, 80, nil); err == nil {
		t.Fatal("want error")
	}
	if got := d.calls; len(got) != 2 || got[1] != "term.close" {
		t.Fatalf("calls %v, want term.open then term.close", got)
	}
}

func TestOpenHubRefusalDoesNotClose(t *testing.T) {
	d := &fakeDoor{openErr: &door.Error{Message: "already open"}}
	Open(context.Background(), d, NewMux(), "t1", "box", 24, 80, nil)
	if len(d.calls) != 1 {
		t.Fatalf("calls %v, want only term.open", d.calls)
	}
}
