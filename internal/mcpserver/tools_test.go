package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshx"
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

func TestFormatExec(t *testing.T) {
	red := config.NewRedactor("hunter2")
	res := sshx.ExecResult{Stdout: "out with hunter2 secret\n", Stderr: "err line\n", ExitCode: 3}
	got := FormatExec(res, red)

	if !strings.Contains(got, "exit code: 3\n") {
		t.Fatalf("missing exit code line: %q", got)
	}
	if !strings.Contains(got, "stdout:\nout with *** secret\n") {
		t.Fatalf("stdout section not redacted/formatted: %q", got)
	}
	if !strings.Contains(got, "stderr:\nerr line\n") {
		t.Fatalf("stderr section missing: %q", got)
	}
	if strings.Contains(got, "hunter2") {
		t.Fatalf("secret was not redacted: %q", got)
	}

	// A huge stderr must not evict stdout: each stream is capped
	// independently against config.DefaultOutputCap.
	hugeStderr := strings.Repeat("e", config.DefaultOutputCap*2)
	res2 := sshx.ExecResult{Stdout: "small stdout", Stderr: hugeStderr, ExitCode: 0}
	got2 := FormatExec(res2, config.NewRedactor())
	if !strings.Contains(got2, "stdout:\nsmall stdout\n") {
		t.Fatalf("small stdout evicted by huge stderr: missing full stdout section")
	}
	if len(got2) >= len(hugeStderr)+len("small stdout")+1 {
		t.Fatalf("stderr was not capped: total len %d", len(got2))
	}
}
