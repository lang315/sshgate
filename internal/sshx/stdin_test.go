package sshx

import (
	"context"
	"errors"
	"strings"
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

// A command that exits 0 without reading all of its stdin is a success:
// stdin is fed after the command starts, and a write that fails because
// the command already exited is not the command's error.
func TestExecStdinCommandExitsEarly(t *testing.T) {
	srv := sshtest.Start(t)
	close(srv.Release)
	m := fakeManager(t, srv)
	stop := make(chan struct{}) // Execs holds 16; drain it for 40 runs
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-srv.Execs:
			case <-stop:
				return
			}
		}
	}()
	for i := range 40 {
		stdin := "x"
		if i%2 == 1 {
			stdin = strings.Repeat("x", 256<<10)
		}
		res, err := m.ExecStdin(context.Background(), "noread", stdin) // sshtest never reads stdin for this command
		if err != nil || res.Stdout != "noread" || res.ExitCode != 0 {
			t.Fatalf("run %d: %+v %v", i, res, err)
		}
	}
}
