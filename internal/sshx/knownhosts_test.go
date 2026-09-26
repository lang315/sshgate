package sshx

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
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

func pub(t *testing.T, k any, err error) ssh.PublicKey {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	p, err := ssh.NewPublicKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestKnownHostKey(t *testing.T) {
	edPub, _, err := ed25519.GenerateKey(nil)
	ed := pub(t, edPub, err)
	ed2Pub, _, err := ed25519.GenerateKey(nil)
	ed2 := pub(t, ed2Pub, err)
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ec := pub(t, &ecKey.PublicKey, err)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	rs := pub(t, &rsaKey.PublicKey, err)
	caPub, _, err := ed25519.GenerateKey(nil)
	ca := pub(t, caPub, err)

	line := func(addr string, k ssh.PublicKey) string {
		return knownhosts.Line([]string{knownhosts.Normalize(addr)}, k) + "\n"
	}
	kh := filepath.Join(t.TempDir(), "known_hosts")
	body := line("multi:22", rs) + line("multi:22", ec) + line("multi:22", ed) +
		line("noed:22", rs) + line("noed:22", ec) +
		line("h:2222", ed) +
		line("gone:22", ed2) + line("gone:22", rs) +
		line("dead:22", ed2) +
		"@revoked * " + string(ssh.MarshalAuthorizedKey(ed2)) +
		"@cert-authority *.corp " + string(ssh.MarshalAuthorizedKey(ca)) +
		line("web.corp:22", rs)
	if err := os.WriteFile(kh, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "none")
	for _, tc := range []struct {
		host     string
		port     int
		algo, fp string
	}{
		{"multi", 22, ssh.KeyAlgoED25519, Fingerprint(ed)},
		{"noed", 22, ec.Type(), Fingerprint(ec)},
		{"h", 2222, ssh.KeyAlgoED25519, Fingerprint(ed)},
		{"h", 22, "", ""},
		{"gone", 22, ssh.KeyAlgoRSA, Fingerprint(rs)}, // the revoked ed25519 key is passed over
		{"dead", 22, "", ""},
		{"absent", 22, "", ""},
		{"web.corp", 22, ssh.KeyAlgoRSA, Fingerprint(rs)}, // the CA key is never a host key
		{"only.corp", 22, "", ""},
	} {
		algo, fp, ok := KnownHostKey([]string{missing, kh}, tc.host, tc.port)
		if algo != tc.algo || fp != tc.fp || ok != (tc.fp != "") {
			t.Errorf("%s:%d: got %q %q %v, want %q %q", tc.host, tc.port, algo, fp, ok, tc.algo, tc.fp)
		}
	}
	if _, _, ok := KnownHostKey([]string{missing}, "multi", 22); ok {
		t.Fatal("no readable file must give no key")
	}

	unreadable := filepath.Join(t.TempDir(), "unreadable")
	if err := os.WriteFile(unreadable, []byte("ignored"), 0o000); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 { // root can read a 0000 file
		if algo, fp, ok := KnownHostKey([]string{unreadable, kh}, "multi", 22); !ok || algo != ssh.KeyAlgoED25519 || fp != Fingerprint(ed) {
			t.Errorf("unreadable file: got %q %q %v, want %q %q true", algo, fp, ok, ssh.KeyAlgoED25519, Fingerprint(ed))
		}
	}
}

// OpenSSH skips a line it cannot parse; so must the import.
func TestKnownHostKeySkipsBadLines(t *testing.T) {
	edPub, _, err := ed25519.GenerateKey(nil)
	ed := pub(t, edPub, err)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	rs := pub(t, &rsaKey.PublicKey, err)
	caPub, _, err := ed25519.GenerateKey(nil)
	ca := pub(t, caPub, err)
	edLine := string(ssh.MarshalAuthorizedKey(ed))
	dir := t.TempDir()
	bad, good := filepath.Join(dir, "bad"), filepath.Join(dir, "good")
	for p, body := range map[string]string{
		bad: "garbage\n|1|notbase64|x " + edLine + "[multi " + edLine + "multi ssh-rsa " + edLine[len("ssh-ed25519 "):] +
			"@cert-authority multi " + string(ssh.MarshalAuthorizedKey(ca)),
		good: knownhosts.Line([]string{"multi"}, rs) + "\n" + knownhosts.Line([]string{"multi"}, ed), // no trailing newline
	} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if algo, fp, ok := KnownHostKey([]string{bad, good}, "multi", 22); !ok || algo != ssh.KeyAlgoED25519 || fp != Fingerprint(ed) {
		t.Fatalf("got %q %q %v, want %q %q", algo, fp, ok, ssh.KeyAlgoED25519, Fingerprint(ed))
	}
	if algo, fp, ok := KnownHostKey([]string{good, bad}, "multi", 22); !ok || fp != Fingerprint(ed) {
		t.Fatalf("reversed: got %q %q %v", algo, fp, ok)
	}
	mixed := filepath.Join(dir, "mixed")
	if err := os.WriteFile(mixed, []byte("garbage\n"+knownhosts.Line([]string{"multi"}, ed)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := KnownHostsHint(mixed, "multi", 22, ed); got != "match" {
		t.Fatalf("hint: got %q, want match", got)
	}
}
