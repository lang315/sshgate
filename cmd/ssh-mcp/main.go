package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/mcpserver"
	"github.com/lang315/ssh-mcp/internal/sshx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func route(args []string) (string, []string) {
	if len(args) > 0 && args[0] == "web" {
		return "web", args[1:]
	}
	return "mcp", args
}

func storePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ssh-mcp", "servers.json")
}

func buildDeps(args []string, env func(string) string) (*mcpserver.Deps, bool, int, error) {
	m := config.ParseArgv(args)
	_, insecure := m["insecureIgnoreHostKey"]
	_, disableSudo := m["disableSudo"]
	maxChars := config.ParseMaxChars(m["maxChars"])
	d := &mcpserver.Deps{Insecure: insecure}

	if _, hasHost := m["host"]; hasHost {
		cli, err := config.BuildCLIConfig(m)
		if err != nil {
			return nil, false, 0, err
		}
		cli.MaxChars = maxChars
		d.CLI = &cli
	}

	if f, err := config.Load(storePath()); err == nil {
		d.File = f
		if pw, ok, err := config.ResolveMasterPassword(env); err != nil {
			return nil, false, 0, err
		} else if ok && f.KDF != nil {
			mk, err := f.KDF.DeriveKey(pw)
			if err != nil {
				return nil, false, 0, err
			}
			if !f.KDF.Verify(mk) {
				return nil, false, 0, fmt.Errorf("wrong master password")
			}
			if err := f.VerifyMAC(mk); err != nil {
				return nil, false, 0, err
			}
			d.MasterKey = mk
		}
	}
	if d.CLI == nil && d.File == nil {
		return nil, false, 0, fmt.Errorf("no --host and no config store found")
	}
	return d, disableSudo, maxChars, nil
}

func runMCP(args []string) error {
	d, disableSudo, maxChars, err := buildDeps(args, os.Getenv)
	if err != nil {
		return err
	}
	reg := sshx.NewRegistry()
	defer reg.CloseAll()
	srv := mcpserver.BuildServer(d, reg, disableSudo, maxChars)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	fmt.Fprintln(os.Stderr, "SSH MCP Server running on stdio")
	return srv.Run(ctx, &mcp.StdioTransport{})
}

func main() {
	mode, rest := route(os.Args[1:])
	var err error
	switch mode {
	case "web":
		err = runWeb(rest) // implemented in Phase D
	default:
		err = runMCP(rest)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
