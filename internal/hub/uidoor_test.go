package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/rpc"
)

func startUI(t *testing.T, h *Hub) (*rpc.Client, chan string) {
	t.Helper()
	uiR, hubW := io.Pipe()
	hubR, uiW := io.Pipe()
	go ServeUIDoor(context.Background(), h, hubR, hubW)
	notes := make(chan string, 16)
	c := rpc.NewClient(uiR, uiW, func(m string, _ json.RawMessage) { notes <- m })
	t.Cleanup(func() { uiW.Close(); hubW.Close() })
	return c, notes
}

func TestUIDoorStatusServersAndDecide(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHub(t, fe)
	c, notes := startUI(t, h)

	var st struct {
		Locked   bool `json:"locked"`
		HasStore bool `json:"hasStore"`
	}
	if err := c.Call(context.Background(), "status", nil, &st); err != nil || st.Locked || !st.HasStore {
		t.Fatalf("status: %v %+v", err, st)
	}
	var servers []map[string]any
	if err := c.Call(context.Background(), "servers", nil, &servers); err != nil || len(servers) != 3 {
		t.Fatalf("servers: %v %+v", err, servers)
	}
	for _, s := range servers {
		for _, k := range []string{"encPassword", "password", "encSuPassword"} {
			if _, has := s[k]; has {
				t.Fatalf("servers leaked %s", k)
			}
		}
	}

	errc := make(chan error, 1)
	go func() { _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); errc <- err }()
	select {
	case m := <-notes:
		if m != "pending" {
			t.Fatalf("want pending, got %s", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no pending notification")
	}
	var pend []map[string]any
	c.Call(context.Background(), "pending", nil, &pend)
	if len(pend) != 1 {
		t.Fatalf("pending = %v", pend)
	}
	if err := c.Call(context.Background(), "decide", map[string]any{"id": pend[0]["id"], "outcome": "denied", "reason": "r"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err == nil || err.Error() != "Denied by user: r" {
		t.Fatalf("got %v", err)
	}
	if m := <-notes; m != "decided" {
		t.Fatalf("want decided, got %s", m)
	}
}

func TestUIDoorDenyAll(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	for range 3 {
		go h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	}
	waitPending(t, h.Broker(), 3)
	if err := c.Call(context.Background(), "denyAll", map[string]string{"reason": "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(h.Broker().Pending()) != 0 {
		t.Fatal("not cleared")
	}
}

// TestUIDoorDecideRejectsInvalidOutcome checks the controller ruling that
// "decide" only accepts allowed/denied/sent_to_tab: withdrawn and expired
// are broker-internal outcomes, never something a human decides.
func TestUIDoorDecideRejectsInvalidOutcome(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	for _, outcome := range []string{"withdrawn", "expired", "bogus"} {
		err := c.Call(context.Background(), "decide", map[string]any{"id": "x", "outcome": outcome}, nil)
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != -32602 {
			t.Fatalf("outcome %q: want -32602, got %v", outcome, err)
		}
	}
}

func TestUIDoorHelloAndLockedNotification(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: -1})
	c, notes := startUI(t, h)
	var hello struct {
		Protocol int `json:"protocol"`
	}
	if err := c.Call(context.Background(), "hello", nil, &hello); err != nil || hello.Protocol != 1 {
		t.Fatalf("hello: %v %+v", err, hello)
	}
	if err := c.Call(context.Background(), "unlock", map[string]string{"password": "pw"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(context.Background(), "lock", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-notes:
		if m != "locked" {
			t.Fatalf("want locked notification, got %s", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no locked notification")
	}
}
