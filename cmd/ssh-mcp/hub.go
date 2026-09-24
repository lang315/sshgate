package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/hub"
	"golang.org/x/term"
)

// runHub implements `ssh-mcp hub`: it opens the audit log next to the
// config store, listens on the MCP door for the bridge, and serves either
// the UI door on stdio (for a desktop app later) or, with --cli, a terminal
// approver that stands in for it.
func runHub(args []string) error {
	m := config.ParseArgv(args)
	_, cli := m["cli"]
	if _, ok := m["insecureIgnoreHostKey"]; ok {
		return errors.New("--insecureIgnoreHostKey is not supported by hub: AI commands always verify the pinned host key")
	}
	idle, err := parseIdleLock(m)
	if err != nil {
		return err
	}
	store := storePath()
	if p := m["store"]; p != nil && *p != "" {
		store = *p
	}
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		return err
	}
	audit, err := broker.OpenAudit(filepath.Join(filepath.Dir(store), "audit.jsonl"))
	if err != nil {
		return err
	}
	defer audit.Close()

	h, err := hub.New(hub.Options{StorePath: store, Audit: audit, IdleLock: idle})
	if err != nil {
		return err
	}
	defer h.Close()
	defer h.Registry().CloseAll()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ln, err := hub.ListenMCPDoor()
	if err != nil {
		return fmt.Errorf("mcp door: %w", err)
	}
	defer ln.Close()
	go hub.ServeMCPDoor(ctx, ln, h)
	fmt.Fprintf(os.Stderr, "ssh-mcp hub: MCP door at %s\n", ln.Addr())

	if cli {
		fd := int(os.Stdin.Fd())
		if term.IsTerminal(fd) {
			hub.ReadPassword = func() (string, error) {
				b, err := term.ReadPassword(fd)
				return string(b), err
			}
			// A SIGINT during the password prompt cancels ctx and
			// RunCLIApprover returns promptly, but the goroutine still
			// blocked inside term.ReadPassword's read() never gets to run
			// its own restore before main's os.Exit skips all defers.
			// Restore explicitly so the terminal isn't left echo-off.
			if st, err := term.GetState(fd); err == nil {
				defer term.Restore(fd, st)
			}
		}
		return cleanOnSignal(hub.RunCLIApprover(ctx, h, os.Stdin, os.Stdout))
	}
	return cleanOnSignal(hub.ServeUIDoor(ctx, h, os.Stdin, os.Stdout))
}

// parseIdleLock reads --idleLock=<duration>, a dev/test override of the
// idle auto-lock. Absent means the hub default (0).
func parseIdleLock(m map[string]*string) (time.Duration, error) {
	v, ok := m["idleLock"]
	if !ok {
		return 0, nil
	}
	if v == nil {
		return 0, errors.New("--idleLock: want --idleLock=<duration>, e.g. --idleLock=3s")
	}
	d, err := time.ParseDuration(*v)
	if err != nil || d < time.Second {
		return 0, fmt.Errorf("--idleLock: want a duration of at least 1s (e.g. 3s), got %q", *v)
	}
	return d, nil
}

// cleanOnSignal maps ctx's own cancellation (SIGINT/SIGTERM, via ctx from
// signal.NotifyContext above) to a clean exit: the door/approver loops
// return ctx.Err() straight through when ctx is done, but that's expected
// shutdown, not a failure worth main's "fatal:" and exit 1. Any other error
// is returned unchanged.
func cleanOnSignal(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
