package sshx

import (
	"context"
	"testing"
)

// Uses a container whose root password is known. linuxserver image's `test`
// user is not root; this test uses `su` to root with a set root password.
func TestSuElevation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSHWithRoot(t) // helper below
	defer cleanup()
	m := NewManager(DialConfig{
		Host: host, Port: port, User: "test", Password: "testpass",
		SuPassword: "rootpass", Auth: "password", Insecure: true, TimeoutMs: 30000,
	})
	defer m.Close()
	out, err := m.Exec(context.Background(), "id -u")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(out, "0") {
		t.Fatalf("expected uid 0, got %q", out)
	}
}

func TestSuWrongPasswordFailsClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSHWithRoot(t)
	defer cleanup()
	m := NewManager(DialConfig{
		Host: host, Port: port, User: "test", Password: "testpass",
		SuPassword: "WRONG", Auth: "password", Insecure: true, TimeoutMs: 15000,
	})
	defer m.Close()
	if _, err := m.Exec(context.Background(), "id -u"); err == nil {
		t.Fatal("wrong su password must fail, not fall back to unprivileged")
	}
}
