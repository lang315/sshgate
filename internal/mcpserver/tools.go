package mcpserver

import (
	"context"
	"fmt"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ExecInput struct {
	Server      string `json:"server" jsonschema:"connection name; empty uses the default server"`
	Command     string `json:"command" jsonschema:"shell command to execute"`
	Description string `json:"description,omitempty" jsonschema:"optional description of the command"`
}
type ListInput struct{}

func textErr(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}
func textOK(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

func runExec(ctx context.Context, d *Deps, reg *sshx.Registry, maxChars int, sudo bool, in ExecInput) (*mcp.CallToolResult, error) {
	cmd, err := config.SanitizeCommand(in.Command, maxChars)
	if err != nil {
		return textErr(err.Error()), nil
	}
	cmd, err = config.AppendDescription(cmd, in.Description)
	if err != nil {
		return textErr(err.Error()), nil
	}
	dc, err := d.Resolve(in.Server)
	if err != nil {
		return textErr(err.Error()), nil
	}
	mgr := reg.Get(nameOr(in.Server), dc)
	red := config.NewRedactor(dc.Password, dc.SuPassword, dc.SudoPassword, dc.Passphrase)
	var out string
	if sudo {
		out, err = mgr.ExecSudo(ctx, cmd)
	} else {
		out, err = mgr.Exec(ctx, cmd)
	}
	if err != nil {
		return textErr(red.Redact(err.Error())), nil
	}
	return textOK(red.Redact(out)), nil
}

func nameOr(s string) string {
	if s == "" {
		return "(default)"
	}
	return s
}

func BuildServer(d *Deps, reg *sshx.Registry, disableSudo bool, maxChars int) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "SSH MCP Server", Version: "2.0.0"}, nil)

	mcp.AddTool(s, &mcp.Tool{Name: "exec", Description: "Execute a shell command on the remote SSH server and return the output."},
		func(ctx context.Context, req *mcp.CallToolRequest, in ExecInput) (*mcp.CallToolResult, any, error) {
			res, err := runExec(ctx, d, reg, maxChars, false, in)
			return res, nil, err
		})

	if !disableSudo {
		mcp.AddTool(s, &mcp.Tool{Name: "sudo-exec", Description: "Execute a shell command using sudo on the remote SSH server."},
			func(ctx context.Context, req *mcp.CallToolRequest, in ExecInput) (*mcp.CallToolResult, any, error) {
				res, err := runExec(ctx, d, reg, maxChars, true, in)
				return res, nil, err
			})
	}

	mcp.AddTool(s, &mcp.Tool{Name: "list-servers", Description: "List configured SSH connection names (no secrets)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, any, error) {
			var b string
			for _, n := range d.ServerNames() {
				lock := ""
				if d.IsLocked(n) {
					lock = " [locked]"
				}
				b += fmt.Sprintf("- %s%s\n", n, lock)
			}
			if b == "" {
				b = "(no servers configured)"
			}
			return textOK(b), nil, nil
		})

	return s
}
