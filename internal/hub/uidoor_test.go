package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/rpc"
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
	c, notes := startUIRaw(t, h)
	var hello struct {
		Protocol int `json:"protocol"`
	}
	if err := c.Call(context.Background(), "hello", nil, &hello); err != nil || hello.Protocol != ProtocolVersion {
		t.Fatalf("hello: %v %+v", err, hello)
	}
	if err := c.Call(context.Background(), "unlock", map[string]string{"password": "pw"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(context.Background(), "lock", nil, nil); err != nil {
		t.Fatal(err)
	}
	if reason := waitLockedNote(t, notes); reason != "manual" {
		t.Fatalf("reason = %q, want manual", reason)
	}
	// Locking an already-locked vault sends nothing.
	if err := c.Call(context.Background(), "lock", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-notes:
		t.Fatalf("unexpected second notification %s %s", n.method, n.params)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestUIDoorLockedNotificationIdleReason(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: 200 * time.Millisecond})
	c, notes := startUIRaw(t, h)
	if err := c.Call(context.Background(), "unlock", map[string]string{"password": "pw"}, nil); err != nil {
		t.Fatal(err)
	}
	if reason := waitLockedNote(t, notes); reason != "idle" {
		t.Fatalf("reason = %q, want idle", reason)
	}
}

// status is polled by the app and must not count as UI input.
func TestUIDoorStatusDoesNotHoldOffIdleLock(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: 300 * time.Millisecond})
	c, _ := startUI(t, h)
	if err := c.Call(context.Background(), "unlock", map[string]string{"password": "pw"}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !h.Locked() && time.Now().Before(deadline) {
		if err := c.Call(context.Background(), "status", nil, nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !h.Locked() {
		t.Fatal("polling status held off the idle lock")
	}
}

type uiNote struct {
	method string
	params json.RawMessage
}

// startUIRaw is startUI but keeps notification params.
func startUIRaw(t *testing.T, h *Hub) (*rpc.Client, chan uiNote) {
	t.Helper()
	uiR, hubW := io.Pipe()
	hubR, uiW := io.Pipe()
	go ServeUIDoor(context.Background(), h, hubR, hubW)
	notes := make(chan uiNote, 16)
	c := rpc.NewClient(uiR, uiW, func(m string, p json.RawMessage) { notes <- uiNote{m, p} })
	t.Cleanup(func() { uiW.Close(); hubW.Close() })
	return c, notes
}

// waitLockedNote waits for a "locked" notification and returns its reason.
func waitLockedNote(t *testing.T, notes chan uiNote) string {
	t.Helper()
	select {
	case n := <-notes:
		if n.method != "locked" {
			t.Fatalf("want locked notification, got %s", n.method)
		}
		var p struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(n.params, &p); err != nil {
			t.Fatal(err)
		}
		return p.Reason
	case <-time.After(3 * time.Second):
		t.Fatal("no locked notification")
	}
	return ""
}
