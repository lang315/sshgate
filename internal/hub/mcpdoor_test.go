package hub

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

// isolateDoor points SocketPath at a fresh short directory so tests never
// touch the user's real hub socket. /tmp keeps the path under sun_path limits.
func isolateDoor(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMPDIR", dir)
	t.Setenv("XDG_RUNTIME_DIR", dir)
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
	go allowFirst(h.Broker())
	var res ExecResponse
	err := c.Call(context.Background(), "exec", map[string]any{"client": "t", "server": "vis", "command": "echo hi"}, &res)
	if err != nil || res.Stdout != "hi\n" {
		t.Fatalf("exec: %v %+v", err, res)
	}
	go allowFirst(h.Broker())
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
	for len(b.Pending()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
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
	for len(b.Pending()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for len(b.Pending()) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(b.Pending()) != 0 {
		t.Fatal("pending survived connection close")
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
