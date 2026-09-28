package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/hub"
	"github.com/lang315/sshgate/internal/mcpserver"
	"github.com/lang315/sshgate/internal/sshx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func route(args []string) (string, []string) {
	if len(args) > 0 {
		switch args[0] {
		case "web":
			return "web", args[1:]
		case "hub":
			return "hub", args[1:]
		case "version", "--version":
			return "version", nil
		}
	}
	return "mcp", args
}

func storePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "sshgate", "servers.json")
}

// buildDeps builds Deps for --host (standalone CLI) mode only, from args
// already parsed by the caller: it never reads the on-disk vault. Bridge
// mode (no --host) does not call this.
func buildDeps(m map[string]*string) (*mcpserver.Deps, bool, int, error) {
	_, insecure := m["insecureIgnoreHostKey"]
	if insecure {
		fmt.Fprintln(os.Stderr, "WARNING: --insecureIgnoreHostKey disables SSH host key verification (MITM risk)")
	}
	_, disableSudo := m["disableSudo"]
	maxChars := config.ParseMaxChars(m["maxChars"])
	cli, err := config.BuildCLIConfig(m)
	if err != nil {
		return nil, false, 0, err
	}
	cli.MaxChars = maxChars
	return &mcpserver.Deps{CLI: &cli, Insecure: insecure}, disableSudo, maxChars, nil
}

func runMCP(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	m := config.ParseArgv(args)
	if _, hasHost := m["host"]; !hasHost {
		fmt.Fprintln(os.Stderr, "sshgate bridge on stdio (forwarding to sshgate hub)")
		return mcpserver.BuildBridgeServer(hub.DialMCPDoor).Run(ctx, &mcp.StdioTransport{})
	}

	d, disableSudo, maxChars, err := buildDeps(m)
	if err != nil {
		return err
	}
	reg := sshx.NewRegistry()
	defer reg.CloseAll()
	srv := mcpserver.BuildServer(d, reg, disableSudo, maxChars)
	fmt.Fprintln(os.Stderr, "sshgate running on stdio (standalone --host mode)")
	return srv.Run(ctx, &mcp.StdioTransport{})
}

func main() {
	mode, rest := route(os.Args[1:])
	var err error
	switch mode {
	case "web":
		// Kept as a route so an old habit fails loudly instead of starting a bridge.
		err = fmt.Errorf("sshgate web was removed; manage hosts in the desktop app")
	case "hub":
		err = runHub(rest)
	case "version":
		fmt.Println("sshgate", mcpserver.Version)
	default:
		err = runMCP(rest)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
