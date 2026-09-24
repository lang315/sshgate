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
