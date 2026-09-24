package sshx

import (
	"errors"
	"testing"

	"github.com/lang315/ssh-mcp/internal/sshx/sshtest"
)

func TestRegistryReusesSameConfig(t *testing.T) {
	r := NewRegistry()
	cfg := DialConfig{Host: "h", Port: 22, User: "u", Auth: "password", Password: "p"}
	m1 := r.Get("a", cfg)
	m2 := r.Get("a", cfg)
	if m1 != m2 {
		t.Fatal("same name+config should reuse manager")
	}
}

func TestRegistryReplacesOnConfigChange(t *testing.T) {
	r := NewRegistry()
	m1 := r.Get("a", DialConfig{Host: "h", Port: 22, User: "u", Auth: "password", Password: "p"})
	m2 := r.Get("a", DialConfig{Host: "h2", Port: 22, User: "u", Auth: "password", Password: "p"})
	if m1 == m2 {
		t.Fatal("config change should create a new manager")
	}
}

func TestRegistryReplacesOnPassphraseChange(t *testing.T) {
	r := NewRegistry()
	base := DialConfig{Host: "h", Port: 22, User: "u", Auth: "key", PrivateKey: "KEY", Passphrase: "old"}
	m1 := r.Get("a", base)
	changed := base
	changed.Passphrase = "new"
	m2 := r.Get("a", changed)
	if m1 == m2 {
		t.Fatal("passphrase change must create a new manager")
	}
}

func TestRegistryReplacesOnHostKeyChange(t *testing.T) {
	r := NewRegistry()
	base := DialConfig{Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:old"}
	m1 := r.Get("a", base)
	changed := base
	changed.HostKey = "SHA256:new"
	if r.Get("a", changed) == m1 {
		t.Fatal("host key change must create a new manager")
	}
}

// A manager that learned its pin by TOFU is kept when the caller comes back
// with that same pin from the store.
func TestRegistryKeepsManagerAfterLearningSamePin(t *testing.T) {
	srv := sshtest.Start(t)
	var learned string
	cfg := DialConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", Auth: "password", TimeoutMs: 30000,
		OnLearnHostKey: func(fp string) { learned = fp }}
	r := NewRegistry()
	t.Cleanup(r.CloseAll)
	m1 := r.Get("a", cfg)
	sess, err := m1.OpenSession()
	if err != nil {
		t.Fatal(err)
	}
	sess.Close()
	if learned == "" {
		t.Fatal("no host key learned")
	}
	pinned := cfg
	pinned.HostKey = learned
	if r.Get("a", pinned) != m1 {
		t.Fatal("manager that learned the same pin must be reused")
	}
}

// After TOFU, a redial on the same manager must verify the learned key, not
// trust on first use again.
func TestRedialVerifiesLearnedHostKey(t *testing.T) {
	srv := sshtest.Start(t)
	m := NewManager(DialConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", Auth: "password", TimeoutMs: 30000,
		OnLearnHostKey: func(string) {}})
	t.Cleanup(m.Close)
	sess, err := m.OpenSession()
	if err != nil {
		t.Fatal(err)
	}
	sess.Close()
	m.Close() // drop the client; the next call redials
	srv.RotateHostKey(t)
	if _, err := m.OpenSession(); !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("redial with a different host key must fail, got %v", err)
	}
}
