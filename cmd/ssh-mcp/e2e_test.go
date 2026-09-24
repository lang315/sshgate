package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/hub"
	"github.com/lang315/ssh-mcp/internal/sshx"
	"github.com/lang315/ssh-mcp/internal/sshx/sshtest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ssh-mcp")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// TestEndToEndApprovalFlow proves the whole approval flow end to end: a real
// MCP client spawns the built ssh-mcp binary in bridge mode, which forwards
// to an in-process hub over the MCP door socket, which runs the approved
// command over SSH against an in-process fake sshd (internal/sshx/sshtest),
// no Docker required.
func TestEndToEndApprovalFlow(t *testing.T) {
	srv := sshtest.Start(t)
	close(srv.Release) // no exec in this test needs to stay pending
	bin := buildBinary(t)

	// Vault with a password server: the host key must be pinned first, so
	// connect once directly (as the app would) to learn it.
	dir := t.TempDir()
	store := filepath.Join(dir, "servers.json")
	k, mk, err := config.NewKDF("pw")
	if err != nil {
		t.Fatal(err)
	}
	f := &config.File{Version: 1, KDF: &k, Servers: []config.Server{{Name: "box", Host: srv.Host, Port: srv.Port, User: "test", Auth: "password", AIVisible: true}}}
	enc, err := config.Encrypt(mk, "box/encPassword", config.AADFor(f, f.Servers[0], "encPassword"), "testpass")
	if err != nil {
		t.Fatal(err)
	}
	f.Servers[0].EncPassword = enc
	if err := config.Save(store, f, mk); err != nil {
		t.Fatal(err)
	}
	m := sshx.NewManager(sshx.DialConfig{Host: srv.Host, Port: srv.Port, User: "test", Password: "testpass", Auth: "password", TimeoutMs: 30000,
		OnLearnHostKey: func(fp string) { _ = config.RecordHostKey(store, "box", fp, mk) }})
	if _, err := m.Exec(context.Background(), "true"); err != nil {
		t.Fatal(err)
	}
	m.Close()

	// Isolate the hub's MCP door socket path to a fresh, short directory so
	// this test never touches the real per-user hub socket. The bridge
	// subprocess below inherits this process's environment, so it dials the
	// same socket.
	smDir, err := os.MkdirTemp("/tmp", "sm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(smDir) })
	t.Setenv("TMPDIR", smDir)
	t.Setenv("XDG_RUNTIME_DIR", smDir)

	auditPath := filepath.Join(dir, "audit.jsonl")
	audit, err := broker.OpenAudit(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.New(hub.Options{StorePath: store, Audit: audit})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	ln, err := hub.ListenMCPDoor()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.ServeMCPDoor(ctx, ln, h)

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code-test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(bin)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// 1. list-servers shows the visible server.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list-servers"})
	if err != nil || res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "box") {
		t.Fatalf("list: %v %+v", err, res)
	}

	// 2. exec is approved -> output returned, redacted.
	go func() {
		b := h.Broker()
		for len(b.Pending()) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		r := b.Pending()[0]
		if r.Client != "claude-code-test" {
			t.Errorf("client name not forwarded: %q", r.Client)
		}
		b.Decide(r.ID, broker.Decision{Outcome: broker.Allowed})
	}()
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "exec", Arguments: map[string]any{"server": "box", "command": "echo hello; echo testpass"}})
	if err != nil || res.IsError {
		t.Fatalf("exec: %v %+v", err, res)
	}
	txt := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(txt, "hello") || !strings.Contains(txt, "***") || strings.Contains(txt, "testpass") || !strings.Contains(txt, "exit code: 0") {
		t.Fatalf("output wrong or leaked secret: %q", txt)
	}

	auditBytes, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("audit file missing: %v", err)
	}
	if strings.Contains(string(auditBytes), "testpass") {
		t.Fatalf("audit log leaked secret: %s", auditBytes)
	}

	// 3. denied -> reason returned.
	go func() {
		b := h.Broker()
		for len(b.Pending()) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		b.Decide(b.Pending()[0].ID, broker.Decision{Outcome: broker.Denied, Reason: "not today"})
	}()
	res, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "exec", Arguments: map[string]any{"server": "box", "command": "rm -rf /"}})
	if !res.IsError || res.Content[0].(*mcp.TextContent).Text != "Denied by user: not today" {
		t.Fatalf("deny: %+v", res)
	}

	// 4. hub down -> the fixed message.
	cancel()
	ln.Close()
	cs2, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: exec.Command(bin)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs2.Close()
	res, _ = cs2.CallTool(context.Background(), &mcp.CallToolParams{Name: "exec", Arguments: map[string]any{"server": "box", "command": "ls"}})
	if !res.IsError || res.Content[0].(*mcp.TextContent).Text != "Open the app to approve commands" {
		t.Fatalf("hub down: %+v", res)
	}
}
