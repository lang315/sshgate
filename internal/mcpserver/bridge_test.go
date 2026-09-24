package mcpserver

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeHub serves a minimal MCP-door method table for the bridge, faithful to
// the real door: listServers/exec/sudoExec are request-only.
func fakeHub(t *testing.T, execResp map[string]any, execErr string) func(context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		s := rpc.NewServer()
		s.HandleRequest("listServers", func(context.Context, json.RawMessage) (any, error) {
			return []map[string]any{{"name": "vis", "locked": false}}, nil
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
	if res.IsError || !strings.Contains(text(res), "vis") {
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
