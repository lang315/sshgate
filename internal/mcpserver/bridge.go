package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/lang315/ssh-mcp/internal/sshx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const hubDownMsg = "Open the app to approve commands"

type bridgeExecInput struct {
	Server      string `json:"server" jsonschema:"connection name from list-servers"`
	Command     string `json:"command" jsonschema:"shell command to execute; requires human approval in the app"`
	Description string `json:"description,omitempty" jsonschema:"what this command does; shown to the human as unverified"`
	TimeoutSec  int    `json:"timeoutSec,omitempty" jsonschema:"execution timeout in seconds, 1-600, default 60"`
}

func clientName(req *mcp.CallToolRequest) string {
	if req != nil && req.Session != nil {
		if ip := req.Session.InitializeParams(); ip != nil && ip.ClientInfo != nil {
			return ip.ClientInfo.Name
		}
	}
	return "unknown"
}

func randID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// BuildBridgeServer forwards the three AI tools to the hub's MCP door over a
// connection dial supplies. The bridge validates nothing and holds no
// secrets; the hub is the single policy point. Each tool call dials fresh,
// so the bridge itself is stateless.
func BuildBridgeServer(dial func(ctx context.Context) (net.Conn, error)) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "SSH MCP Server", Version: "3.0.0"}, nil)

	withHub := func(ctx context.Context, fn func(c *rpc.Client) (*mcp.CallToolResult, error)) (*mcp.CallToolResult, error) {
		conn, err := dial(ctx)
		if err != nil {
			return textErr(hubDownMsg), nil
		}
		defer conn.Close()
		return fn(rpc.NewClient(conn, conn, nil))
	}

	execTool := func(method string) func(context.Context, *mcp.CallToolRequest, bridgeExecInput) (*mcp.CallToolResult, any, error) {
		return func(ctx context.Context, req *mcp.CallToolRequest, in bridgeExecInput) (*mcp.CallToolResult, any, error) {
			res, err := withHub(ctx, func(c *rpc.Client) (*mcp.CallToolResult, error) {
				id := randID()
				params := map[string]any{
					"requestId": id, "client": clientName(req), "server": in.Server,
					"command": in.Command, "description": in.Description, "timeoutSec": in.TimeoutSec,
				}

				// Progress notifications keep MCP clients that reset their
				// timeout on progress alive while the human decides. The
				// ticker goroutine is joined (not just signalled) right
				// after c.Call returns, so it never leaks and never fires
				// once the call has finished.
				var notifying atomic.Bool
				notifying.Store(true)
				stop, done := make(chan struct{}), make(chan struct{})
				if tok := req.Params.GetProgressToken(); tok != nil && req.Session != nil {
					go func() {
						defer close(done)
						t := time.NewTicker(5 * time.Second)
						defer t.Stop()
						for {
							select {
							case <-t.C:
								if notifying.Load() {
									_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: tok, Message: "waiting for approval in the app"})
								}
							case <-stop:
								return
							}
						}
					}()
				} else {
					close(done)
				}

				var out sshx.ExecResult
				callErr := c.Call(ctx, method, params, &out)
				notifying.Store(false)
				close(stop)
				<-done // ticker goroutine has fully exited before we proceed

				if ctx.Err() != nil {
					// Client cancelled while we were waiting: withdraw the
					// pending request from the hub before returning.
					cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					_ = c.Call(cctx, "cancel", map[string]string{"requestId": id}, nil)
					return textErr("cancelled by client"), nil
				}
				if callErr != nil {
					return textErr(callErr.Error()), nil
				}
				return textOK(FormatExec(out, config.NewRedactor())), nil
			})
			return res, nil, err
		}
	}

	mcp.AddTool(s, &mcp.Tool{Name: "exec", Description: "Run a shell command on a saved SSH server. A human must approve it in the ssh-mcp app first."}, execTool("exec"))
	mcp.AddTool(s, &mcp.Tool{Name: "sudo-exec", Description: "Run a shell command with sudo on a saved SSH server. A human must approve it in the ssh-mcp app first."}, execTool("sudoExec"))
	mcp.AddTool(s, &mcp.Tool{Name: "list-servers", Description: "List the SSH servers the user has made visible to AI, with lock status."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, any, error) {
			res, err := withHub(ctx, func(c *rpc.Client) (*mcp.CallToolResult, error) {
				var list []struct {
					Name   string `json:"name"`
					Locked bool   `json:"locked"`
				}
				if err := c.Call(ctx, "listServers", nil, &list); err != nil {
					return textErr(err.Error()), nil
				}
				if len(list) == 0 {
					return textOK("(no servers are visible to AI; enable 'Visible to AI' on a server in the app)"), nil
				}
				var b strings.Builder
				for _, entry := range list {
					lock := ""
					if entry.Locked {
						lock = " [locked: unlock the app]"
					}
					fmt.Fprintf(&b, "- %s%s\n", entry.Name, lock)
				}
				return textOK(b.String()), nil
			})
			return res, nil, err
		})
	return s
}
