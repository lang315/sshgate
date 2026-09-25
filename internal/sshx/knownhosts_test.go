package sshx

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestKnownHostsHint(t *testing.T) {
	k1, k2 := testKey(t), testKey(t)
	file := filepath.Join(t.TempDir(), "known_hosts")
	lines := knownhosts.Line([]string{"plain.example:2222"}, k1) + "\n" +
		knownhosts.Line([]string{knownhosts.HashHostname("hashed.example")}, k1) + "\n"
	if err := os.WriteFile(file, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		host string
		port int
		key  ssh.PublicKey
		want string
	}{
		{"plain.example", 2222, k1, "match"},
		{"plain.example", 2222, k2, "different"},
		{"plain.example", 22, k1, "absent"},
		{"hashed.example", 22, k1, "match"},
		{"hashed.example", 22, k2, "different"},
		{"other.example", 22, k1, "absent"},
	} {
		if got := KnownHostsHint(file, c.host, c.port, c.key); got != c.want {
			t.Errorf("%s:%d: got %q, want %q", c.host, c.port, got, c.want)
		}
	}
	if got := KnownHostsHint(filepath.Join(t.TempDir(), "missing"), "plain.example", 2222, k1); got != "absent" {
		t.Errorf("missing file: got %q", got)
	}
}
