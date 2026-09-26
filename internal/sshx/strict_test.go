package sshx

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/sshx/sshtest"
	"golang.org/x/crypto/ssh"
)

func TestStrictDialNeverLearns(t *testing.T) {
	srv := sshtest.Start(t)
	learned := false
	m := NewManager(DialConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", Auth: "password", TimeoutMs: 30000,
		StrictHostKey: true, OnLearnHostKey: func(string) { learned = true }})
	t.Cleanup(m.Close)
	_, err := m.OpenSession()
	var u *HostKeyUnknownError
	if !errors.As(err, &u) || u.Fingerprint != srv.Fingerprint() {
		t.Fatalf("got %v", err)
	}
	if learned || m.currentConfig().HostKey != "" {
		t.Fatal("a strict dial learned a key")
	}
}

func TestPinnedAlgoLimitsNegotiation(t *testing.T) {
	srv := sshtest.Start(t)
	cfg := DialConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", Auth: "password", TimeoutMs: 30000,
		StrictHostKey: true, HostKey: srv.Fingerprint(), HostKeyAlgo: ssh.KeyAlgoED25519}
	m := NewManager(cfg)
	t.Cleanup(m.Close)
	sess, err := m.OpenSession()
	if err != nil {
		t.Fatal(err)
	}
	sess.Close()
	// sshtest has only an ed25519 key, so pinning another family leaves no common algorithm.
	cfg.HostKeyAlgo = ssh.KeyAlgoECDSA256
	m2 := NewManager(cfg)
	t.Cleanup(m2.Close)
	if _, err := m2.OpenSession(); err == nil || !strings.Contains(err.Error(), "no common algorithm") {
		t.Fatalf("want a negotiation failure, got %v", err)
	}
}

// A strict caller's empty pin means "unpinned": never filled from the cache.
func TestRegistryStrictIgnoresCachedPin(t *testing.T) {
	r := NewRegistry()
	pinned := DialConfig{Host: "h", Port: 22, User: "u", Auth: "agent", StrictHostKey: true, HostKey: "SHA256:x"}
	m1 := r.Get("a", pinned)
	unpinned := pinned
	unpinned.HostKey = ""
	if r.Get("a", unpinned) == m1 {
		t.Fatal("a strict caller with no pin reused a pinned manager")
	}
}

func TestRegistryCloseEndsTerminals(t *testing.T) {
	srv := sshtest.Start(t)
	r := NewRegistry()
	t.Cleanup(r.CloseAll)
	cfg := DialConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", Auth: "password", TimeoutMs: 30000,
		StrictHostKey: true, HostKey: srv.Fingerprint()}
	m1 := r.Get("a", cfg)
	exited := make(chan struct{})
	term, err := m1.OpenTerm(24, 80, func([]byte) {}, func(int, string) { close(exited) })
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	r.Close("a")
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal did not end when its manager was closed")
	}
	if r.Get("a", cfg) == m1 {
		t.Fatal("a closed manager was reused")
	}
}
