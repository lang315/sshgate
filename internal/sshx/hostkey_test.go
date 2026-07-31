package sshx

import (
	"net"
	"testing"

	"crypto/rand"
	"crypto/ed25519"
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
	cb := HostKeyCallback("sha256:WRONG", false, nil)
	if err := cb("h:22", &net.TCPAddr{}, pk); err == nil {
		t.Fatal("mismatched pin must be rejected")
	}
}
