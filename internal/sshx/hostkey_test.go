package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testKey(t *testing.T) ssh.PublicKey {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer.PublicKey()
}

func TestTOFULearns(t *testing.T) {
	pk := testKey(t)
	var learned string
	cb := HostKeyCallback("", false, func(fp string) { learned = fp })
	if err := cb("h:22", &net.TCPAddr{}, pk); err != nil {
		t.Fatal(err)
	}
	if learned != Fingerprint(pk) {
		t.Fatalf("learned %q", learned)
	}
}

func TestPinnedMismatchRejected(t *testing.T) {
	pk := testKey(t)
	err := HostKeyCallback("sha256:WRONG", false, nil)("h:22", &net.TCPAddr{}, pk)
	var m *HostKeyMismatchError
	if !errors.As(err, &m) || !errors.Is(err, ErrHostKeyMismatch) || m.Pinned != "sha256:WRONG" ||
		m.Presented != Fingerprint(pk) || m.KeyType != ssh.KeyAlgoED25519 || m.Key == nil {
		t.Fatalf("got %v", err)
	}
}

func TestInsecureAcceptsAnyKeyEvenWithMismatchedPin(t *testing.T) {
	pk := testKey(t)
	// insecure=true must override even a mismatched pin
	cb := HostKeyCallback("sha256:DELIBERATELY-WRONG", true, nil)
	if err := cb("h:22", &net.TCPAddr{}, pk); err != nil {
		t.Fatalf("insecure=true must accept any key, got error: %v", err)
	}
}

func TestStrictRefusesUnpinnedKey(t *testing.T) {
	pk := testKey(t)
	err := HostKeyCallback("", false, nil)("h:22", &net.TCPAddr{}, pk)
	var u *HostKeyUnknownError
	if !errors.As(err, &u) || u.Fingerprint != Fingerprint(pk) || u.KeyType != ssh.KeyAlgoED25519 || u.Key == nil {
		t.Fatalf("got %v", err)
	}
}

func TestHostKeyAlgorithms(t *testing.T) {
	if got := hostKeyAlgorithms(""); got != nil {
		t.Fatalf("no pinned algo must negotiate as before, got %v", got)
	}
	if got := hostKeyAlgorithms(ssh.KeyAlgoED25519); len(got) != 1 || got[0] != ssh.KeyAlgoED25519 {
		t.Fatalf("ed25519: %v", got)
	}
	got := hostKeyAlgorithms(ssh.KeyAlgoRSA)
	if len(got) != 3 || got[0] != ssh.KeyAlgoRSASHA512 || got[1] != ssh.KeyAlgoRSASHA256 || got[2] != ssh.KeyAlgoRSA {
		t.Fatalf("rsa family: %v", got)
	}
}
