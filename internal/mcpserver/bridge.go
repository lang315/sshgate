package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lang315/sshgate/internal/rpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	hubDownMsg    = "Open the app to approve commands"
	hubCrashedMsg = "App closed or crashed"
)

type bridgeExecInput struct {
	Server      string `json:"server" jsonschema:"connection name from list-servers"`
	Command     string `json:"command" jsonschema:"shell command to execute; requires human approval in the app"`
	Description string `json:"description,omitempty" jsonschema:"one sentence on what the command does and why; shown to the approver next to the command (marked unverified) and recorded in the audit log, never executed; at most 500 bytes, no control characters"`
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

// hubCallErr classifies a failed hub RPC. An *rpc.Error is the hub's own
// policy text (denied, locked, not found, ...) and must reach the AI
// verbatim. Anything else is a transport failure — a dropped connection, a
// crashed hub mid-request — whose raw text (e.g. "io: read/write on closed
// pipe") must never reach the AI; the underlying error is logged to stderr
// instead.
func hubCallErr(err error) *mcp.CallToolResult {
	var rpcErr *rpc.Error
	if errors.As(err, &rpcErr) {
		return textErr(rpcErr.Message)
	}
	fmt.Fprintln(os.Stderr, "sshgate bridge: hub transport error:", err)
	return textErr(hubCrashedMsg)
}

// BuildBridgeServer forwards the three AI tools to the hub's MCP door over a
// connection dial supplies. The bridge validates nothing and holds no
// secrets; the hub is the single policy point. Each tool call dials fresh,
// so the bridge itself is stateless.
func BuildBridgeServer(dial func(ctx context.Context) (net.Conn, error)) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "sshgate", Version: Version}, nil)

	withHub := func(ctx context.Context, fn func(c *rpc.Client) (*mcp.CallToolResult, error)) (*mcp.CallToolResult, error) {
		conn, err := dial(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sshgate bridge: dial hub:", err)
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

				var out struct {
					ExitCode int            `json:"exitCode"`
					Stdout   string         `json:"stdout"`
					Stderr   string         `json:"stderr"`
					Redacted map[string]int `json:"redacted"`
				}
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
					return hubCallErr(callErr), nil
				}
				// The hub already redacted and capped each stream and
				// counted what it masked; lay out the text as-is, no
				// reprocessing (double-capping would corrupt an in-flight
				// "[truncated N bytes]" marker).
				return textOK(layoutExec(out.ExitCode, out.Stdout, out.Stderr, out.Redacted)), nil
			})
			return res, nil, err
		}
	}

	mcp.AddTool(s, &mcp.Tool{Name: "exec", Description: "Run a shell command on a saved SSH server through the sshgate desktop app. " +
		"Each call waits until a human approves or denies it in the app; after 5 minutes without a decision it fails as expired, so combine related steps into one command. " +
		"Each call runs in a fresh non-interactive shell: the working directory, environment variables and activated virtualenvs do not carry over, and ~/.bashrc is usually not read, so write `cd /app && ./run.sh` as one command. " +
		"(On a server configured with a su password, commands run as root inside one persistent root shell instead.) " +
		"The result is `exit code:` followed by `stdout:` and `stderr:` sections; a non-zero exit code is a normal result, not a tool error. Each stream is capped at 64 KiB (the middle is cut). Saved secrets are masked, and values that look like secrets (keys, passwords, tokens) are masked too, as `[REDACTED:<kind>]`, with a final `note:` line saying how many; there is no way to get them unmasked. " +
		"The call fails if the app is closed, no vault exists yet, the vault is locked, the server is not visible to AI or has no pinned host key, the human denies it, or 5 requests are already waiting."}, execTool("exec"))
	mcp.AddTool(s, &mcp.Tool{Name: "sudo-exec", Description: "Run a shell command with sudo on a saved SSH server through the sshgate desktop app. " +
		"The command runs as `sudo -S` with the server's saved sudo password, or as `sudo -n` when none is saved (which fails if sudo asks for a password). " +
		"Approval, fresh-shell, output and failure rules are the same as exec."}, execTool("sudoExec"))
	mcp.AddTool(s, &mcp.Tool{Name: "list-servers", Description: "List the saved SSH servers the user has made visible to AI, one per line as `- name`. " +
		"A server marked `[locked: unlock the app]` cannot run commands until the user unlocks the app. " +
		"Use these names as the `server` argument of exec and sudo-exec; servers the user has not made visible never appear. Needs no approval."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, any, error) {
			res, err := withHub(ctx, func(c *rpc.Client) (*mcp.CallToolResult, error) {
				var list []struct {
					Name   string `json:"name"`
					Locked bool   `json:"locked"`
				}
				if err := c.Call(ctx, "listServers", nil, &list); err != nil {
					return hubCallErr(err), nil
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
