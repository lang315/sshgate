package hub

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

// isolateDoor points SocketPath at a fresh short directory so tests never
// touch the user's real hub socket. /tmp keeps the path under sun_path limits.
func isolateDoor(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMPDIR", dir)
	t.Setenv("XDG_RUNTIME_DIR", dir)
	return dir
}

func startDoor(t *testing.T, h *Hub) *rpc.Client {
	t.Helper()
	isolateDoor(t)
	ln, err := ListenMCPDoor()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go ServeMCPDoor(context.Background(), ln, h)
	conn, err := DialMCPDoor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return rpc.NewClient(conn, conn, nil)
}

func TestMCPDoorRejectsUIOnlyMethods(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c := startDoor(t, h)
	for _, m := range []string{"unlock", "lock", "decide", "denyAll", "pending", "term.open", "servers"} {
		err := c.Call(context.Background(), m, map[string]any{"password": "x"}, nil)
		if err == nil || !strings.Contains(err.Error(), "method not found") {
			t.Fatalf("%s: want method not found, got %v", m, err)
		}
	}
}

func TestMCPDoorListAndExec(t *testing.T) {
	fe := &fakeExec{res: sshx.ExecResult{Stdout: "hi\n"}}
	h, _ := newHub(t, fe)
	c := startDoor(t, h)
	var list []ServerInfo
	if err := c.Call(context.Background(), "listServers", nil, &list); err != nil || len(list) != 2 {
		t.Fatalf("list: %v %+v", err, list)
	}
	go allowFirst(t, h.Broker())
	var res ExecResponse
	err := c.Call(context.Background(), "exec", map[string]any{"client": "t", "server": "vis", "command": "echo hi"}, &res)
	if err != nil || res.Stdout != "hi\n" {
		t.Fatalf("exec: %v %+v", err, res)
	}
	go allowFirst(t, h.Broker())
	if err := c.Call(context.Background(), "sudoExec", map[string]any{"server": "vis", "command": "id"}, &res); err != nil {
		t.Fatal(err)
	}
	if len(fe.calls) != 2 || fe.calls[0] != "echo hi" || fe.calls[1] != "sudo:id" {
		t.Fatalf("calls = %v", fe.calls)
	}
}

// Long expiry so only cancel/close, not the approval timer, can withdraw.
func TestMCPDoorCancelWithdrawsPending(t *testing.T) {
	h, _ := newHubExpiry(t, &fakeExec{}, time.Minute)
	c := startDoor(t, h)
	errc := make(chan error, 1)
	go func() {
		errc <- c.Call(context.Background(), "exec", map[string]any{"requestId": "r1", "server": "vis", "command": "ls"}, nil)
	}()
	b := h.Broker()
	waitPending(t, b, 1)
	if err := c.Call(context.Background(), "cancel", map[string]any{"requestId": "r1"}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("cancelled exec should error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exec did not return after cancel")
	}
	if len(b.Pending()) != 0 {
		t.Fatal("pending not withdrawn")
	}
}

func TestMCPDoorConnectionCloseWithdrawsPending(t *testing.T) {
	h, _ := newHubExpiry(t, &fakeExec{}, time.Minute)
	isolateDoor(t)
	ln, err := ListenMCPDoor()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go ServeMCPDoor(context.Background(), ln, h)
	conn, err := DialMCPDoor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := rpc.NewClient(conn, conn, nil)
	go c.Call(context.Background(), "exec", map[string]any{"server": "vis", "command": "ls"}, nil)
	b := h.Broker()
	waitPending(t, b, 1)
	conn.Close()
	if !waitFor(t, "pending withdrawn on connection close", func() bool { return len(b.Pending()) == 0 }) {
		t.FailNow()
	}
}

func TestListenMCPDoorRefusesSecondHub(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c := startDoor(t, h) // first hub, listening and serving
	if ln2, err := ListenMCPDoor(); err == nil || !strings.Contains(err.Error(), "another ssh-mcp hub is already running") {
		if ln2 != nil {
			ln2.Close()
		}
		t.Fatalf("second listen: want already-running error, got %v", err)
	}
	var list []ServerInfo
	if err := c.Call(context.Background(), "listServers", nil, &list); err != nil || len(list) != 2 {
		t.Fatalf("first hub broken after second listen: %v %+v", err, list)
	}
	conn, err := DialMCPDoor(context.Background()) // socket file still in place
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func TestListenMCPDoorReplacesStaleSocket(t *testing.T) {
	isolateDoor(t)
	ln, err := ListenMCPDoor()
	if err != nil {
		t.Fatal(err)
	}
	p, _ := SocketPath()
	// Simulate a crashed hub: close the listener but leave the socket file.
	if ul, ok := ln.(interface{ SetUnlinkOnClose(bool) }); ok {
		ul.SetUnlinkOnClose(false)
	}
	ln.Close()
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no stale socket left on this platform: %v", err)
	}
	ln, err = ListenMCPDoor()
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	ln.Close()
}

// An id-less exec must never reach the broker: run inline on the read loop it
// would block disconnect handling and cancel for the whole approval wait.
func TestMCPDoorIgnoresNotificationExec(t *testing.T) {
	h, _ := newHubExpiry(t, &fakeExec{}, time.Minute)
	isolateDoor(t)
	ln, err := ListenMCPDoor()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go ServeMCPDoor(context.Background(), ln, h)
	conn, err := DialMCPDoor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, m := range []string{"exec", "sudoExec"} {
		if _, err := conn.Write([]byte(`{"jsonrpc":"2.0","method":"` + m + `","params":{"server":"vis","command":"ls"}}` + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	c := rpc.NewClient(conn, conn, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// The server reads lines in order and drops a notification to a
	// request-only method inline on the read loop, so once this answers the
	// notifications are already gone: no sleep needed.
	var list []ServerInfo
	if err := c.Call(ctx, "listServers", nil, &list); err != nil {
		t.Fatalf("read loop blocked: %v", err)
	}
	if n := len(h.Broker().Pending()); n != 0 {
		t.Fatalf("notification exec reached the broker: %d pending", n)
	}
}

func TestMCPDoorCancelBeforeExecRefusesExec(t *testing.T) {
	h, _ := newHubExpiry(t, &fakeExec{}, time.Minute)
	c := startDoor(t, h)
	if err := c.Call(context.Background(), "cancel", map[string]any{"requestId": "early"}, nil); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() {
		errc <- c.Call(context.Background(), "exec", map[string]any{"requestId": "early", "server": "vis", "command": "ls"}, nil)
	}()
	select {
	case err := <-errc:
		var re *rpc.Error
		if !errors.As(err, &re) || !strings.Contains(re.Message, "canceled") {
			t.Fatalf("want cancelled error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exec was submitted despite an earlier cancel")
	}
	if n := len(h.Broker().Pending()); n != 0 {
		t.Fatalf("pending = %d", n)
	}
	// The early cancel is consumed: the same id can run again.
	go allowFirst(t, h.Broker())
	if err := c.Call(context.Background(), "exec", map[string]any{"requestId": "early", "server": "vis", "command": "ls"}, nil); err != nil {
		t.Fatalf("reused id: %v", err)
	}
}
