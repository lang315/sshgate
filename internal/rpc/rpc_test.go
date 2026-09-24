package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
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

// TestServeDispatchesNotificationsInOrder covers fix-round-1 item 1:
// notifications (no id) must be dispatched synchronously on Serve's read
// loop, in the order received, so e.g. terminal keystrokes are never
// reordered. The Client type has no send-side notify, so this writes raw
// JSON-RPC lines directly to exercise Serve's dispatch order.
func TestServeDispatchesNotificationsInOrder(t *testing.T) {
	r, w := io.Pipe()
	s := NewServer()
	var mu sync.Mutex
	var got []int
	s.Handle("tick", func(ctx context.Context, p json.RawMessage) (any, error) {
		var n int
		json.Unmarshal(p, &n)
		mu.Lock()
		got = append(got, n)
		mu.Unlock()
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx, r, io.Discard)

	go func() {
		for i := 0; i < 200; i++ {
			line, _ := json.Marshal(struct {
				JSONRPC string `json:"jsonrpc"`
				Method  string `json:"method"`
				Params  int    `json:"params"`
			}{"2.0", "tick", i})
			w.Write(append(line, '\n'))
		}
	}()

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 200 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only got %d/200 notifications", n)
		case <-time.After(5 * time.Millisecond):
		}
	}

	mu.Lock()
	defer mu.Unlock()
	for i, v := range got {
		if v != i {
			t.Fatalf("out of order at index %d: got %d, want %d", i, v, i)
		}
	}
}

// TestServeReturnsPromptlyOnCtxCancel covers fix-round-1 item 3: Serve must
// return when ctx is done even if the reader never sends anything.
func TestServeReturnsPromptlyOnCtxCancel(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close() // let the leaked scanner goroutine observe EOF

	s := NewServer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done before Serve is called

	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, r, io.Discard) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Serve did not return promptly on ctx cancel")
	}
}

// TestServeCalledTwiceErrors covers fix-round-1 item 4: a Server serves
// exactly one connection.
func TestServeCalledTwiceErrors(t *testing.T) {
	s := NewServer()
	if err := s.Serve(context.Background(), strings.NewReader(""), io.Discard); err != nil {
		t.Fatalf("first Serve: %v", err)
	}
	if err := s.Serve(context.Background(), strings.NewReader(""), io.Discard); err == nil {
		t.Fatal("second Serve call should return an error")
	}
}

// TestClientCloseUnblocksPendingCall covers fix-round-1 items 5 and 6:
// Close must close the reader (not just the writer) so readLoop exits and a
// pending Call unblocks and cleans up its waiting entry.
func TestClientCloseUnblocksPendingCall(t *testing.T) {
	srvR, _, cliR, cliW := pipePair()
	go io.Copy(io.Discard, srvR) // drain so Call's write doesn't block

	c := NewClient(cliR, cliW, nil)
	errCh := make(chan error, 1)
	go func() { errCh <- c.Call(context.Background(), "noop", nil, nil) }()
	time.Sleep(50 * time.Millisecond) // let Call register itself as waiting

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("want error after Close, got nil")
		}
	case <-time.After(time.Second):
		t.Fatal("pending Call did not unblock after Close")
	}
}
