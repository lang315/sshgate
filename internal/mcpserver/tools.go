package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ExecInput struct {
	Server      string `json:"server" jsonschema:"leave empty: this server exposes only the connection given on its command line"`
	Command     string `json:"command" jsonschema:"shell command to execute"`
	Description string `json:"description,omitempty" jsonschema:"optional one-line note on what the command does; appended to the command as a shell comment (# ...), at most 500 bytes, no control characters"`
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
	var res sshx.ExecResult
	if sudo {
		res, err = mgr.ExecSudo(ctx, cmd)
	} else {
		res, err = mgr.Exec(ctx, cmd)
	}
	if err != nil {
		return textErr(red.Redact(err.Error())), nil
	}
	return textOK(FormatExec(res, red)), nil
}

// FormatExec renders an exec result for the AI: exit code first, then the
// two streams, each redacted (saved secrets, then RedactPatterns) and capped
// in one bounded pass, then the redaction note.
func FormatExec(res sshx.ExecResult, red *config.Redactor) string {
	stdout, stderr, counts := config.RedactCapStreams(red, res.Stdout, res.Stderr, config.DefaultOutputCap)
	return layoutExec(res.ExitCode, stdout, stderr, counts)
}

// layoutExec lays out exit code + stdout/stderr sections, and ends with the
// redaction note when redacted counts anything. It does no redaction and no
// capping: callers that already have redacted/capped text (e.g. the bridge,
// which forwards output the hub already processed) must call this directly
// instead of FormatExec, or the text gets capped twice and an in-flight
// "[truncated N bytes]" marker gets corrupted.
func layoutExec(exitCode int, stdout, stderr string, redacted map[string]int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "exit code: %d\n", exitCode)
	if stdout != "" {
		b.WriteString("stdout:\n")
		b.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			b.WriteString("\n")
		}
	}
	if stderr != "" {
		b.WriteString("stderr:\n")
		b.WriteString(stderr)
	}
	if note := redactionNote(redacted); note != "" {
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
		b.WriteString(note + "\n")
	}
	return b.String()
}

// redactionNote tells the AI how many values RedactPatterns masked, by kind
// in config.RedactKinds order; "" when none.
func redactionNote(counts map[string]int) string {
	var parts []string
	total := 0
	for _, k := range config.RedactKinds {
		if n := counts[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s ×%d", k, n))
			total += n
		}
	}
	switch total {
	case 0:
		return ""
	case 1:
		return "note: sshgate redacted 1 value (" + parts[0] + "); the value is withheld from AI clients"
	}
	return fmt.Sprintf("note: sshgate redacted %d values (%s); the values are withheld from AI clients", total, strings.Join(parts, ", "))
}

func nameOr(s string) string {
	if s == "" {
		return "(default)"
	}
	return s
}

func BuildServer(d *Deps, reg *sshx.Registry, disableSudo bool, maxChars int) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "sshgate", Version: Version}, nil)

	mcp.AddTool(s, &mcp.Tool{Name: "exec", Description: "Run a shell command on the SSH server given on this sshgate server's command line. It runs immediately, without human approval. " +
		"If a su password was configured, the command runs as root inside one persistent root shell; otherwise each call runs in a fresh non-interactive shell, so `cd` and environment changes do not carry over between calls. " +
		"The result is `exit code:` followed by `stdout:` and `stderr:` sections; a non-zero exit code is a normal result. Each stream is capped at 64 KiB (the middle is cut). Configured secrets are masked, and values that look like secrets (keys, passwords, tokens) are masked too, as `[REDACTED:<kind>]`, with a final `note:` line saying how many. " +
		"Commands containing control characters, or longer than the configured maximum length, are rejected."},
		func(ctx context.Context, req *mcp.CallToolRequest, in ExecInput) (*mcp.CallToolResult, any, error) {
			res, err := runExec(ctx, d, reg, maxChars, false, in)
			return res, nil, err
		})

	if !disableSudo {
		mcp.AddTool(s, &mcp.Tool{Name: "sudo-exec", Description: "Run a shell command with sudo on the SSH server given on this sshgate server's command line, without human approval. " +
			"It runs as `sudo -S` with the configured sudo password, or `sudo -n` when none is configured (which fails if sudo asks for a password). Output and rejection rules are the same as exec."},
			func(ctx context.Context, req *mcp.CallToolRequest, in ExecInput) (*mcp.CallToolResult, any, error) {
				res, err := runExec(ctx, d, reg, maxChars, true, in)
				return res, nil, err
			})
	}

	mcp.AddTool(s, &mcp.Tool{Name: "list-servers", Description: "List the connection this sshgate server exposes: the one given on its command line, shown as `(default)`. Returns names only, never secrets."},
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
