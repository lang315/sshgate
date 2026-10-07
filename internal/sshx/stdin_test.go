package sshx

import (
	"context"
	"errors"
	"testing"

	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

func TestExecStdinPipesInput(t *testing.T) {
	srv := sshtest.Start(t)
	close(srv.Release)
	m := fakeManager(t, srv)
	res, err := m.ExecStdin(context.Background(), "stdin-echo", "line1\nline2\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "line1\nline2\r\n" || res.ExitCode != 0 {
		t.Fatalf("got %+v", res)
	}
}

// The su shell frames commands over its own stdin; stdin cannot share it.
func TestExecStdinRefusedWithSuPassword(t *testing.T) {
	m := NewManager(DialConfig{Host: "127.0.0.1", Port: 1, User: "u", Password: "p", Auth: "password", SuPassword: "su", Insecure: true, TimeoutMs: 1000})
	defer m.Close()
	if _, err := m.ExecStdin(context.Background(), "cat", "x"); !errors.Is(err, ErrStdinUnsupported) {
		t.Fatalf("got %v", err)
	}
}
