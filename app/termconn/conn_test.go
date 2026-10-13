package termconn

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"
)

// fakeDoor records notifications and answers term.open.
type fakeDoor struct {
	mu    sync.Mutex
	notes []struct {
		method string
		params map[string]any
	}
	openStatus string
	fail       bool
}

func (f *fakeDoor) Call(_ context.Context, method string, params, result any) error {
	if method == "term.open" {
		st := f.openStatus
		if st == "" {
			st = "open"
		}
		b, _ := json.Marshal(map[string]any{"status": st})
		return json.Unmarshal(b, result)
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
	c, err := Open(context.Background(), d, m, "t1", "box", 24, 80)
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

func TestExitEndsRead(t *testing.T) {
	d := &fakeDoor{}
	c, m := open(t, d)
	go note(m, "term.exit", map[string]any{"id": "t1", "code": 0, "reason": "closed"})
	if _, err := c.Read(make([]byte, 8)); err != io.EOF {
		t.Fatalf("err %v, want EOF", err)
	}
	if c.ExitReason() != "closed" {
		t.Fatalf("reason %q", c.ExitReason())
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
	now = now.Add(2 * time.Millisecond) // 1001 ms after the mark
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
	for _, x := range d.notes {
		if x.method == "term.close" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("term.close sent %d times", n)
	}
	note(m, "term.data", map[string]any{"id": "t1", "data": []byte("late")}) // dropped, no panic
	if _, err := c.Read(make([]byte, 4)); err != io.EOF {
		t.Fatalf("read after close: %v", err)
	}
}

func TestDroppedCounts(t *testing.T) {
	d := &fakeDoor{}
	c, m := open(t, d)
	note(m, "term.dropped", map[string]any{"id": "t1", "bytes": 7})
	if c.Dropped() != 7 {
		t.Fatalf("dropped %d", c.Dropped())
	}
}

func TestOpenRefusedStatus(t *testing.T) {
	d := &fakeDoor{openStatus: "hostKeyUnknown"}
	_, err := Open(context.Background(), d, NewMux(), "t1", "box", 24, 80)
	oe, ok := err.(*OpenError)
	if !ok || oe.Status != "hostKeyUnknown" {
		t.Fatalf("err %v", err)
	}
}
