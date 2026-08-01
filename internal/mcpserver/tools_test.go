package mcpserver

import (
	"context"
	"testing"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

func TestRunExecSanitizeError(t *testing.T) {
	d := &Deps{CLI: &config.CLIConfig{Host: "h", User: "u", HasHost: true, TimeoutMs: 60000, MaxChars: 1000}}
	reg := sshx.NewRegistry()
	res, err := runExec(context.Background(), d, reg, 1000, false, ExecInput{Command: "   "})
	if err != nil {
		t.Fatalf("expected IsError result not go error, got %v", err)
	}
	if !res.IsError {
		t.Fatal("empty command should produce IsError result")
	}
}

func TestRunExecLockedServer(t *testing.T) {
	f := &config.File{Version: 1, Servers: []config.Server{{Name: "p", Host: "h", Port: 22, User: "u", Auth: "password", EncPassword: "x"}}}
	d := &Deps{File: f}
	reg := sshx.NewRegistry()
	res, _ := runExec(context.Background(), d, reg, 1000, false, ExecInput{Server: "p", Command: "ls"})
	if !res.IsError {
		t.Fatal("locked server should produce IsError result")
	}
}
