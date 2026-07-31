package sshx

import (
	"strings"
	"testing"
)

func TestWrapSudoNoPassword(t *testing.T) {
	got := WrapSudoNoPassword("ls '/tmp'")
	if got != `sudo -n sh -c 'ls '\''/tmp'\'''` {
		t.Fatalf("got %q", got)
	}
}

func TestWrapSudoWithPasswordNoSecretInline(t *testing.T) {
	got := WrapSudoWithPassword("whoami")
	if strings.Contains(got, "printf") {
		t.Fatal("password must not be piped inline anymore")
	}
	if !strings.Contains(got, "-S") || !strings.Contains(got, "</dev/null") {
		t.Fatalf("got %q", got)
	}
}

func TestFrameSuCommand(t *testing.T) {
	if FrameSuCommand("id", "NONCE") != "id; echo NONCE:$?" {
		t.Fatalf("got %q", FrameSuCommand("id", "NONCE"))
	}
}
