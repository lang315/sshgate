package files

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

// remote starts a test SFTP server rooted in a temp dir and returns a client,
// the server, and the root on disk (the server's /home is <root>/home).
func remote(t *testing.T) (*sftp.Client, *sshtest.Server, string) {
	t.Helper()
	s := sshtest.Start(t)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "home"), 0o755)
	s.ServeSFTP(root)
	cc := &ssh.ClientConfig{User: "u", Auth: []ssh.AuthMethod{ssh.Password("x")}, HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	conn, err := ssh.Dial("tcp", s.Addr(), cc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, s, root
}

func write(t *testing.T, p, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, mode)
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
