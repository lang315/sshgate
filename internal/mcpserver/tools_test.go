package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshx"
	"github.com/lang315/sshgate/internal/sshx/sshtest"
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

func TestLayoutExecNote(t *testing.T) {
	got := layoutExec(0, "a\n", "b", map[string]int{"token": 1, "private_key": 1, "password": 2})
	want := "exit code: 0\nstdout:\na\nstderr:\nb\nnote: sshgate redacted 4 values (private_key ×1, password ×2, token ×1); the values are withheld from AI clients\n"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if got := layoutExec(0, "", "", map[string]int{"secret": 1}); got != "exit code: 0\nnote: sshgate redacted 1 value (secret ×1); the value is withheld from AI clients\n" {
		t.Fatalf("singular: %q", got)
	}
	for _, none := range []map[string]int{nil, {}} {
		if got := layoutExec(0, "a\n", "", none); got != "exit code: 0\nstdout:\na\n" {
			t.Fatalf("no note expected: %q", got)
		}
	}
}

// The cap keeps the first and last 32 KiB. A private key whose BEGIN line
// falls in the dropped middle while its body lands in the kept tail would
// no longer match after the cap, so FormatExec must mask first.
func TestFormatExecMasksBeforeCap(t *testing.T) {
	body := "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKj"
	rest := body + "\n-----END PRIVATE KEY-----\n"
	stdout := strings.Repeat("a", 40000) + "\n-----BEGIN PRIVATE KEY-----\n" + rest + strings.Repeat("b", config.DefaultOutputCap/2-len(rest))
	if capped := config.CapOutput(stdout, config.DefaultOutputCap); !strings.Contains(capped, body) || strings.Contains(capped, "BEGIN") {
		t.Fatal("test premise broken: the cap no longer splits the key from its BEGIN line")
	}
	got := FormatExec(sshx.ExecResult{Stdout: stdout}, config.NewRedactor())
	if strings.Contains(got, body) {
		t.Fatal("key body reached the AI")
	}
	if !strings.HasSuffix(got, "note: sshgate redacted 1 value (private_key ×1); the value is withheld from AI clients\n") {
		t.Fatalf("note missing: ...%q", got[len(got)-200:])
	}
}

func TestRunExecStdin(t *testing.T) {
	srv := sshtest.Start(t)
	close(srv.Release)
	d := &Deps{CLI: &config.CLIConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", HasHost: true, TimeoutMs: 30000, MaxChars: 1000}, Insecure: true}
	reg := sshx.NewRegistry()
	defer reg.CloseAll()
	res, _ := runExec(context.Background(), d, reg, 1000, false, ExecInput{Command: "stdin-echo", Stdin: "one\ntwo\n"})
	if res.IsError || !strings.Contains(text(res), "stdout:\none\ntwo\n") {
		t.Fatalf("got %q", text(res))
	}
	res, _ = runExec(context.Background(), d, reg, 1000, true, ExecInput{Command: "cat", Stdin: "x"})
	if !res.IsError || text(res) != config.ErrStdinSudo.Error() {
		t.Fatalf("sudo: %q", text(res))
	}
	su := &Deps{CLI: &config.CLIConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", SuPassword: "su", HasHost: true, HasSuPassword: true, TimeoutMs: 30000, MaxChars: 1000}, Insecure: true}
	res, _ = runExec(context.Background(), su, reg, 1000, false, ExecInput{Command: "cat", Stdin: "x"})
	if !res.IsError || text(res) != config.ErrStdinSu.Error() {
		t.Fatalf("su: %q", text(res))
	}
}

func TestStandaloneStdinOnlyOnExec(t *testing.T) {
	d := &Deps{CLI: &config.CLIConfig{Host: "h", User: "u", HasHost: true, TimeoutMs: 60000, MaxChars: 1000}}
	s := inputSchemas(t, BuildServer(d, sshx.NewRegistry(), false, 1000))
	if !strings.Contains(s["exec"], `"stdin"`) || strings.Contains(s["sudo-exec"], `"stdin"`) {
		t.Fatalf("exec: %s\nsudo-exec: %s", s["exec"], s["sudo-exec"])
	}
}
