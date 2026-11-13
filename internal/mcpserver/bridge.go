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

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	hubDownMsg    = "Open the app to approve commands"
	hubCrashedMsg = "App closed or crashed"
	hubNoStdinMsg = "The running sshgate app is older than this bridge and does not support stdin; restart the app"
)

type bridgeExecInput struct {
	Server      string `json:"server" jsonschema:"connection name from list-servers"`
	Command     string `json:"command" jsonschema:"shell command to execute; requires human approval in the app"`
	Description string `json:"description,omitempty" jsonschema:"one sentence on what the command does and why; shown to the approver next to the command (marked unverified) and recorded in the audit log, never executed; at most 500 bytes, no control characters"`
	TimeoutSec  int    `json:"timeoutSec,omitempty" jsonschema:"execution timeout in seconds, 1-600, default 60"`
	Stdin       string `json:"stdin,omitempty" jsonschema:"text piped to the command's standard input, shown in full to the approver and recorded in the audit log; use it to write files (cat > path) or apply a unified diff (git apply); at most 256 KiB; newlines, tabs and carriage returns allowed, other control characters rejected"`
}

// bridgeSudoInput is sudo-exec's input: no stdin, since sudo -S reads the
// password from stdin.
type bridgeSudoInput struct {
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

// callHub makes one door call with the progress ticker and cancel handling
// shared by every approval-gated tool. It returns nil on success (out is
// filled), else the result to hand the AI. noMethod, when set, is the text for
// a hub that does not know the method (-32601).
func callHub(ctx context.Context, req *mcp.CallToolRequest, c *rpc.Client, method string, params map[string]any, out any, noMethod string) *mcp.CallToolResult {
	// Progress notifications keep MCP clients that reset their timeout on
	// progress alive while the human decides. The ticker goroutine is joined
	// (not just signalled) right after c.Call returns, so it never leaks and
	// never fires once the call has finished.
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

	callErr := c.Call(ctx, method, params, out)
	notifying.Store(false)
	close(stop)
	<-done // ticker goroutine has fully exited before we proceed

	if ctx.Err() != nil {
		// Client cancelled while we were waiting: withdraw the pending
		// request from the hub before returning.
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		id, _ := params["requestId"].(string)
		_ = c.Call(cctx, "cancel", map[string]string{"requestId": id}, nil)
		return textErr("cancelled by client")
	}
	var rpcErr *rpc.Error
	if noMethod != "" && errors.As(callErr, &rpcErr) && rpcErr.Code == -32601 {
		return textErr(noMethod)
	}
	if callErr != nil {
		return hubCallErr(callErr)
	}
	return nil
}

type bridgeUploadInput struct {
	Server      string   `json:"server" jsonschema:"connection name from list-servers"`
	Local       []string `json:"local" jsonschema:"files or folders to send, relative to your project folder (the working directory this tool runs in); at most 100"`
	Remote      string   `json:"remote" jsonschema:"existing remote folder to copy into, absolute path"`
	Overwrite   bool     `json:"overwrite,omitempty" jsonschema:"replace remote files that already exist; default false skips them"`
	Description string   `json:"description,omitempty" jsonschema:"one sentence on what the transfer is for; shown to the approver and recorded in the audit log; at most 500 bytes"`
	TimeoutSec  int      `json:"timeoutSec,omitempty" jsonschema:"transfer timeout in seconds, 1-3600, default 600"`
}

type bridgeDownloadInput struct {
	Server      string   `json:"server" jsonschema:"connection name from list-servers"`
	Remote      []string `json:"remote" jsonschema:"remote files or folders to fetch, absolute paths; at most 100"`
	Local       string   `json:"local" jsonschema:"existing folder to copy into, relative to your project folder; . is the project folder itself"`
	Overwrite   bool     `json:"overwrite,omitempty" jsonschema:"replace local files that already exist; default false skips them"`
	Description string   `json:"description,omitempty" jsonschema:"one sentence on what the transfer is for; shown to the approver and recorded in the audit log; at most 500 bytes"`
	TimeoutSec  int      `json:"timeoutSec,omitempty" jsonschema:"transfer timeout in seconds, 1-3600, default 600"`
}

type transferRefusal struct {
	Path  string         `json:"path"`
	Kinds map[string]int `json:"kinds"`
}

type transferOut struct {
	Files        int               `json:"files"`
	Bytes        int64             `json:"bytes"`
	Skipped      int               `json:"skipped"`
	SkippedNames []string          `json:"skippedNames"`
	Refused      []transferRefusal `json:"refused"`
	RefusedCount int               `json:"refusedCount"`
	Errors       []string          `json:"errors"`
	ErrorCount   int               `json:"errorCount"`
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// layoutTransfer is what the AI reads after a transfer ran.
func layoutTransfer(op string, r transferOut) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%sed %d %s, %d bytes\n", op, r.Files, plural(r.Files, "file", "files"), r.Bytes)
	if r.Skipped > 0 {
		fmt.Fprintf(&b, "skipped %d existing %s (not overwritten): %s\n", r.Skipped, plural(r.Skipped, "file", "files"), strings.Join(r.SkippedNames, ", "))
	}
	if r.RefusedCount > 0 {
		fmt.Fprintf(&b, "refused %d %s holding a recognised secret (not saved):\n", r.RefusedCount, plural(r.RefusedCount, "file", "files"))
		for _, f := range r.Refused {
			var kinds []string
			for _, k := range config.RedactKinds {
				if n := f.Kinds[k]; n > 0 {
					kinds = append(kinds, fmt.Sprintf("%s ×%d", k, n))
				}
			}
			fmt.Fprintf(&b, "  %s (%s)\n", f.Path, strings.Join(kinds, ", "))
		}
	}
	if r.ErrorCount > 0 {
		fmt.Fprintf(&b, "%d %s:\n", r.ErrorCount, plural(r.ErrorCount, "error", "errors"))
		for _, e := range r.Errors {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}
	return b.String()
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
				// Stdin goes on its own door method: a hub that predates it
				// answers "method not found" rather than dropping the field
				// and running the command without its stdin.
				method := method
				if in.Stdin != "" {
					method, params["stdin"] = "execStdin", in.Stdin
				}

				var out struct {
					ExitCode int            `json:"exitCode"`
					Stdout   string         `json:"stdout"`
					Stderr   string         `json:"stderr"`
					Redacted map[string]int `json:"redacted"`
				}
				noMethod := ""
				if method == "execStdin" {
					noMethod = hubNoStdinMsg
				}
				if r := callHub(ctx, req, c, method, params, &out, noMethod); r != nil {
					return r, nil
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
		"Each call runs in a fresh non-interactive shell: the working directory, environment variables and activated virtualenvs do not carry over, and ~/.bashrc is usually not read. " +
		"The shell depends on the server's OS. Linux and macOS: sh or bash; write `cd /app && ./run.sh` as one command. " +
		"Windows (OpenSSH): cmd.exe or Windows PowerShell 5.1, whichever the server's admin set; a remote path like `/C:/...` from upload, download or git-push, or `C:\\` in output, means Windows. " +
		"In PowerShell 5.1 `&&`, `cat > file` and `/dev/null` do not work: join steps with `;` and check `$LASTEXITCODE`; any failing program reports exit code 1 there. On a Windows server use upload to write a file. " +
		"(On a server configured with a su password, commands run as root inside one persistent root shell instead.) " +
		"The result is `exit code:` followed by `stdout:` and `stderr:` sections; a non-zero exit code is a normal result, not a tool error. Each stream is capped at 64 KiB (the middle is cut). Saved secrets are masked, and values that look like secrets (keys, passwords, tokens) are masked too, as `[REDACTED:<kind>]`, with a final `note:` line saying how many; there is no way to get them unmasked. " +
		"On Linux and macOS, to write a file, pass its content as `stdin` with `cat > path`; to edit one, pass a unified diff as `stdin` with `git apply` (or `patch -p1`). Never write back output that contained masked values (`***` or `[REDACTED:…]`): the file would be corrupted. `stdin` is not available on servers configured with a su password. " +
		"The call fails if the app is closed, no vault exists yet, the vault is locked, the server is not visible to AI or has no pinned host key, the human denies it, or 5 requests are already waiting."}, execTool("exec"))
	sudoExec := execTool("sudoExec")
	mcp.AddTool(s, &mcp.Tool{Name: "sudo-exec", Description: "Run a shell command with sudo on a saved SSH server through the sshgate desktop app. " +
		"The command runs as `sudo -S` with the server's saved sudo password, or as `sudo -n` when none is saved (which fails if sudo asks for a password). " +
		"Linux and macOS only. Approval, fresh-shell, output and failure rules are the same as exec."},
		func(ctx context.Context, req *mcp.CallToolRequest, in bridgeSudoInput) (*mcp.CallToolResult, any, error) {
			return sudoExec(ctx, req, bridgeExecInput{Server: in.Server, Command: in.Command, Description: in.Description, TimeoutSec: in.TimeoutSec})
		})
	const transferOld = "The running sshgate app is older than this bridge and does not support file transfer; restart the app"
	transfer := func(ctx context.Context, req *mcp.CallToolRequest, op, server, description string, overwrite bool, timeoutSec int, paths map[string]any) (*mcp.CallToolResult, any, error) {
		wd, err := os.Getwd()
		if err != nil {
			return textErr("cannot read the project folder: " + err.Error()), nil, nil
		}
		res, err := withHub(ctx, func(c *rpc.Client) (*mcp.CallToolResult, error) {
			params := map[string]any{"requestId": randID(), "client": clientName(req), "server": server, "root": wd,
				"overwrite": overwrite, "description": description, "timeoutSec": timeoutSec}
			for k, v := range paths {
				params[k] = v
			}
			var out transferOut
			if r := callHub(ctx, req, c, op, params, &out, transferOld); r != nil {
				return r, nil
			}
			return textOK(layoutTransfer(op, out)), nil
		})
		return res, nil, err
	}
	mcp.AddTool(s, &mcp.Tool{Name: "upload", Description: "Copy files or folders from your project folder (this tool's working directory) into an existing folder on a saved SSH server, through the sshgate desktop app. Binary files and folders are fine. Each call waits for a human to approve it, unless the server has auto-allow on. Existing remote files are skipped unless overwrite is true. Symlinks you name are refused; symlinks inside folders are skipped. Not available on servers configured with a su password. Remote paths on a Windows server look like `/C:/Users/me/app`. For a small text file on Linux or macOS, exec with stdin (`cat > path`) also works."},
		func(ctx context.Context, req *mcp.CallToolRequest, in bridgeUploadInput) (*mcp.CallToolResult, any, error) {
			return transfer(ctx, req, "upload", in.Server, in.Description, in.Overwrite, in.TimeoutSec,
				map[string]any{"local": in.Local, "remote": []string{in.Remote}})
		})
	mcp.AddTool(s, &mcp.Tool{Name: "download", Description: "Copy files or folders from a saved SSH server into an existing folder inside your project folder, through the sshgate desktop app. Approval, auto-allow and overwrite rules are as for upload. Each downloaded file is checked for recognised secrets (private keys, passwords, tokens, the server's saved secrets); a file holding one is not saved and is listed as refused with its kinds. Not available on servers configured with a su password."},
		func(ctx context.Context, req *mcp.CallToolRequest, in bridgeDownloadInput) (*mcp.CallToolResult, any, error) {
			return transfer(ctx, req, "download", in.Server, in.Description, in.Overwrite, in.TimeoutSec,
				map[string]any{"remote": in.Remote, "local": []string{in.Local}})
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list-servers", Description: "List the saved SSH servers the user has made visible to AI, one per line as `- name (host)`, where host is the server's IP address or hostname; to find a server by its IP, match the host and use its name. " +
		"A server marked `[locked: unlock the app]` cannot run commands until the user unlocks the app. " +
		"Use these names as the `server` argument of every sshgate tool; servers the user has not made visible never appear. Needs no approval."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, any, error) {
			res, err := withHub(ctx, func(c *rpc.Client) (*mcp.CallToolResult, error) {
				var list []struct {
					Name   string `json:"name"`
					Host   string `json:"host"`
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
					fmt.Fprintf(&b, "- %s (%s)%s\n", entry.Name, entry.Host, lock)
				}
				return textOK(b.String()), nil
			})
			return res, nil, err
		})
	return s
}
