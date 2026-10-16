package door

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// fakeHub answers requests from a table and can send notifications.
type fakeHub struct {
	in  *bufio.Reader
	out io.Writer
	mu  sync.Mutex
}

func (f *fakeHub) send(v any) {
	b, _ := json.Marshal(v)
	f.mu.Lock()
	f.out.Write(append(b, '\n'))
	f.mu.Unlock()
}

func pair(t *testing.T, onNote func(string, json.RawMessage)) (*Client, *fakeHub, func()) {
	cr, hw := io.Pipe() // hub writes, client reads
	hr, cw := io.Pipe() // client writes, hub reads
	c := New(cr, cw, onNote)
	f := &fakeHub{in: bufio.NewReader(hr), out: hw}
	return c, f, func() { hw.Close(); hr.Close() }
}

func (f *fakeHub) next(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	line, err := f.in.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCallResult(t *testing.T) {
	c, f, done := pair(t, nil)
	defer done()
	go func() {
		m := f.next(t)
		f.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(m["id"]), "result": map[string]int{"protocol": 12}})
	}()
	var got struct{ Protocol int }
	if err := c.Call(context.Background(), "hello", map[string]any{}, &got); err != nil || got.Protocol != 12 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestCallError(t *testing.T) {
	c, f, done := pair(t, nil)
	defer done()
	go func() {
		m := f.next(t)
		f.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(m["id"]), "error": map[string]any{"code": -32000, "message": "wrong master password"}})
	}()
	err := c.Call(context.Background(), "unlock", map[string]string{"password": "x"}, nil)
	var de *Error
	if !errors.As(err, &de) || de.Message != "wrong master password" || de.Code != -32000 {
		t.Fatalf("err = %v", err)
	}
}

func TestConcurrentCallsMatchByID(t *testing.T) {
	c, f, done := pair(t, nil)
	defer done()
	go func() {
		a, b := f.next(t), f.next(t)
		// answer in reverse order
		f.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(b["id"]), "result": json.RawMessage(b["method"])})
		f.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(a["id"]), "result": json.RawMessage(a["method"])})
	}()
	var wg sync.WaitGroup
	for _, m := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var got string
			if err := c.Call(context.Background(), m, nil, &got); err != nil || got != m {
				t.Errorf("%s: got %q, %v", m, got, err)
			}
		}()
	}
	wg.Wait()
}

func TestNotificationsInOrder(t *testing.T) {
	var mu sync.Mutex
	var got []string
	c, f, done := pair(t, func(method string, _ json.RawMessage) {
		mu.Lock()
		got = append(got, method)
		mu.Unlock()
	})
	defer done()
	for _, m := range []string{"pending", "locked", "audit.appended"} {
		f.send(map[string]any{"jsonrpc": "2.0", "method": m, "params": map[string]any{}})
	}
	f.send(map[string]any{"jsonrpc": "2.0", "method": "end"})
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 4 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 4 || got[0] != "pending" || got[1] != "locked" || got[2] != "audit.appended" {
		t.Fatalf("got %q", got)
	}
	_ = c
}

func TestEOFFailsPendingCalls(t *testing.T) {
	c, f, done := pair(t, nil)
	go func() { f.next(t); done() }() // read the call, then die
	err := c.Call(context.Background(), "status", nil, nil)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done not closed")
	}
	if err := c.Call(context.Background(), "status", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after EOF: %v", err)
	}
}

func TestNotifyHasNoID(t *testing.T) {
	c, f, done := pair(t, nil)
	defer done()
	go c.Notify("term.ack", map[string]any{"id": "t1", "n": 5})
	m := f.next(t)
	if _, ok := m["id"]; ok {
		t.Fatalf("notification carries an id: %s", m)
	}
	if string(m["method"]) != `"term.ack"` {
		t.Fatalf("method %s", m["method"])
	}
}
