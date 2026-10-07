package mcpserver

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeHub serves a minimal MCP-door method table for the bridge, faithful to
// the real door: listServers/exec/sudoExec are request-only.
func fakeHub(t *testing.T, execResp map[string]any, execErr string) func(context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		s := rpc.NewServer()
		s.HandleRequest("listServers", func(context.Context, json.RawMessage) (any, error) {
			return []map[string]any{{"name": "vis", "host": "10.0.0.12", "locked": false}, {"name": "db", "host": "db.example", "locked": true}}, nil
		})
		execHandler := func(_ context.Context, raw json.RawMessage) (any, error) {
			if execErr != "" {
				return nil, &rpc.Error{Code: -32000, Message: execErr}
			}
			return execResp, nil
		}
		s.HandleRequest("exec", execHandler)
		s.HandleRequest("sudoExec", execHandler)
		go s.Serve(context.Background(), server, server)
		return client, nil
	}
}

func callTool(t *testing.T, srv *mcp.Server, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	go srv.Run(context.Background(), st)
	cl := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := cl.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func text(r *mcp.CallToolResult) string { return r.Content[0].(*mcp.TextContent).Text }

func TestBridgeExecSuccess(t *testing.T) {
	srv := BuildBridgeServer(fakeHub(t, map[string]any{"exitCode": 0, "stdout": "hi\n", "stderr": ""}, ""))
	res := callTool(t, srv, "exec", map[string]any{"server": "vis", "command": "echo hi"})
	if res.IsError || !strings.Contains(text(res), "exit code: 0") || !strings.Contains(text(res), "hi") {
		t.Fatalf("got %+v", text(res))
	}
}

func TestBridgeHubErrorVerbatim(t *testing.T) {
	srv := BuildBridgeServer(fakeHub(t, nil, "Denied by user: nope"))
	res := callTool(t, srv, "exec", map[string]any{"server": "vis", "command": "ls"})
	if !res.IsError || text(res) != "Denied by user: nope" {
		t.Fatalf("got %v %q", res.IsError, text(res))
	}
}

func TestBridgeHubDownMessage(t *testing.T) {
	srv := BuildBridgeServer(func(context.Context) (net.Conn, error) { return nil, net.ErrClosed })
	res := callTool(t, srv, "exec", map[string]any{"server": "vis", "command": "ls"})
	if !res.IsError || text(res) != "Open the app to approve commands" {
		t.Fatalf("got %q", text(res))
	}
	res = callTool(t, srv, "list-servers", nil)
	if !res.IsError || text(res) != "Open the app to approve commands" {
		t.Fatalf("got %q", text(res))
	}
}

func TestBridgeListServers(t *testing.T) {
	srv := BuildBridgeServer(fakeHub(t, nil, ""))
	res := callTool(t, srv, "list-servers", nil)
	want := "- vis (10.0.0.12)\n- db (db.example) [locked: unlock the app]\n"
	if res.IsError || text(res) != want {
		t.Fatalf("got %q", text(res))
	}
}

// TestBridgeExecCancelForwardsToHub verifies that when the MCP client
// cancels an in-flight exec call, the bridge sends the hub a cancel for the
// exact requestId it used for that exec, on the same connection.
func TestBridgeExecCancelForwardsToHub(t *testing.T) {
	started := make(chan string, 1)
	cancelled := make(chan string, 1)

	dial := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		s := rpc.NewServer()
		var mu sync.Mutex
		inflight := map[string]context.CancelFunc{}
		s.HandleRequest("exec", func(ctx context.Context, raw json.RawMessage) (any, error) {
			var p struct {
				RequestID string `json:"requestId"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
			}
			reqCtx, cancel := context.WithCancel(ctx)
			mu.Lock()
			inflight[p.RequestID] = cancel
			mu.Unlock()
			started <- p.RequestID
			<-reqCtx.Done()
			return nil, context.Canceled
		})
		s.Handle("cancel", func(_ context.Context, raw json.RawMessage) (any, error) {
			var p struct {
				RequestID string `json:"requestId"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
			}
			mu.Lock()
			if c, ok := inflight[p.RequestID]; ok {
				c()
			}
			mu.Unlock()
			cancelled <- p.RequestID
			return map[string]bool{"ok": true}, nil
		})
		go s.Serve(context.Background(), server, server)
		return client, nil
	}

	srv := BuildBridgeServer(dial)
	ct, st := mcp.NewInMemoryTransports()
	go srv.Run(context.Background(), st)
	cl := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := cl.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go cs.CallTool(ctx, &mcp.CallToolParams{Name: "exec", Arguments: map[string]any{"server": "vis", "command": "sleep 5"}})

	var reqID string
	select {
	case reqID = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("hub never saw exec")
	}

	cancel()

	select {
	case got := <-cancelled:
		if got != reqID {
			t.Fatalf("cancel requestId = %q, want %q", got, reqID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hub never saw cancel")
	}
}

// crashingHub dials fine but the hub reads the request and then hangs up
// without answering, simulating the app closing or crashing mid-request.
func crashingHub() func(context.Context) (net.Conn, error) {
	return func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			buf := make([]byte, 4096)
			server.Read(buf) // consume the request, then hang up unanswered
			server.Close()
		}()
		return client, nil
	}
}

// TestBridgeHubCrashMidCall covers fix-round-1 R34: a transport failure
// (dropped connection, not a hub-issued error) must never leak its raw text
// ("io: read/write on closed pipe" etc.) to the AI.
func TestBridgeHubCrashMidCall(t *testing.T) {
	srv := BuildBridgeServer(crashingHub())

	res := callTool(t, srv, "exec", map[string]any{"server": "vis", "command": "ls"})
	if !res.IsError || text(res) != "App closed or crashed" {
		t.Fatalf("exec: got %v %q", res.IsError, text(res))
	}

	res = callTool(t, srv, "list-servers", nil)
	if !res.IsError || text(res) != "App closed or crashed" {
		t.Fatalf("list-servers: got %v %q", res.IsError, text(res))
	}
}

// TestBridgeExecNoDoubleCap covers fix-round-1 R33: the hub already
// redacts and caps each stream, so the bridge must lay the text out as-is.
// Re-capping an already-capped, already-larger-than-DefaultOutputCap stream
// would slice through its "[truncated N bytes]" marker.
func TestBridgeExecNoDoubleCap(t *testing.T) {
	marker := "[truncated 434464 bytes]"
	head := strings.Repeat("a", config.DefaultOutputCap)
	tail := strings.Repeat("b", config.DefaultOutputCap)
	stdout := head + "\n… " + marker + " …\n" + tail // already > DefaultOutputCap

	srv := BuildBridgeServer(fakeHub(t, map[string]any{"exitCode": 0, "stdout": stdout, "stderr": ""}, ""))
	res := callTool(t, srv, "exec", map[string]any{"server": "vis", "command": "cat big"})
	if res.IsError {
		t.Fatalf("unexpected error: %q", text(res))
	}
	if !strings.Contains(text(res), marker) {
		t.Fatalf("marker not found verbatim: %q", text(res))
	}
	if !strings.Contains(text(res), stdout) {
		t.Fatalf("hub output was re-capped by the bridge: got %d bytes, want the full %d-byte stdout verbatim", len(text(res)), len(stdout))
	}
}

// The hub already masked and counted; the bridge only lays the counts out
// as the final note line, and adds none when the hub sent none.
func TestBridgeExecRedactedNote(t *testing.T) {
	srv := BuildBridgeServer(fakeHub(t, map[string]any{"exitCode": 0, "stdout": "DB_PASSWORD=[REDACTED:password]\n", "stderr": "",
		"redacted": map[string]int{"password": 1, "private_key": 2}}, ""))
	res := callTool(t, srv, "exec", map[string]any{"server": "vis", "command": "cat .env"})
	want := "exit code: 0\nstdout:\nDB_PASSWORD=[REDACTED:password]\nnote: sshgate redacted 3 values (private_key ×2, password ×1); the values are withheld from AI clients\n"
	if res.IsError || text(res) != want {
		t.Fatalf("got %q", text(res))
	}
	srv = BuildBridgeServer(fakeHub(t, map[string]any{"exitCode": 0, "stdout": "hi\n", "stderr": ""}, ""))
	if res := callTool(t, srv, "exec", map[string]any{"server": "vis", "command": "echo hi"}); strings.Contains(text(res), "note:") {
		t.Fatalf("note without redacted: %q", text(res))
	}
}

func TestBridgeExecForwardsStdin(t *testing.T) {
	got := make(chan map[string]any, 1)
	dial := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		s := rpc.NewServer()
		s.HandleRequest("exec", func(_ context.Context, raw json.RawMessage) (any, error) {
			var p map[string]any
			json.Unmarshal(raw, &p)
			got <- p
			return map[string]any{"exitCode": 0, "stdout": "", "stderr": ""}, nil
		})
		go s.Serve(context.Background(), server, server)
		return client, nil
	}
	res := callTool(t, BuildBridgeServer(dial), "exec", map[string]any{"server": "vis", "command": "cat > f", "stdin": "a\nb\n"})
	if res.IsError {
		t.Fatalf("got %q", text(res))
	}
	if p := <-got; p["stdin"] != "a\nb\n" || p["command"] != "cat > f" {
		t.Fatalf("door params %v", p)
	}
}

// inputSchemas maps each tool name to its input schema as JSON.
func inputSchemas(t *testing.T, srv *mcp.Server) map[string]string {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	go srv.Run(context.Background(), st)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	lt, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, tool := range lt.Tools {
		b, _ := json.Marshal(tool.InputSchema)
		out[tool.Name] = string(b)
	}
	return out
}

func TestBridgeStdinOnlyOnExec(t *testing.T) {
	s := inputSchemas(t, BuildBridgeServer(fakeHub(t, nil, "")))
	if !strings.Contains(s["exec"], `"stdin"`) || strings.Contains(s["sudo-exec"], `"stdin"`) {
		t.Fatalf("exec: %s\nsudo-exec: %s", s["exec"], s["sudo-exec"])
	}
}
