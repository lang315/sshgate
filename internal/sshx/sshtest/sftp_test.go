package sshtest

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func sftpClient(t *testing.T, s *Server) *sftp.Client {
	t.Helper()
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
	return c
}

func rootedServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := Start(t)
	root := t.TempDir()
	s.ServeSFTP(root)
	return s, root
}

func TestSFTPRootedHomeAndFiles(t *testing.T) {
	s, root := rootedServer(t)
	c := sftpClient(t, s)
	home, err := c.RealPath(".")
	if err != nil || home != "/home" {
		t.Fatalf("home %q %v", home, err)
	}
	f, err := c.OpenFile("/home/a.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("hi"))
	f.Close()
	if b, _ := os.ReadFile(filepath.Join(root, "home", "a.txt")); string(b) != "hi" {
		t.Fatalf("on disk %q", b)
	}
	if _, err := c.OpenFile("/home/a.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL); err == nil {
		t.Fatal("O_EXCL create of an existing file succeeded")
	}
	// Absolute paths and .. stay inside the root.
	if _, err := c.Stat("/../../etc/passwd"); err == nil {
		t.Fatal("escaped the root")
	}
	os.Symlink("/etc", filepath.Join(root, "home", "esc"))
	if _, err := c.Stat("/home/esc/passwd"); err == nil {
		t.Fatal("followed a symlink out of the root")
	}
	if fi, err := c.Lstat("/home/esc"); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("lstat link %v %v", fi, err)
	}
}

func TestSFTPRenameSemantics(t *testing.T) {
	s, root := rootedServer(t)
	c := sftpClient(t, s)
	os.WriteFile(filepath.Join(root, "home", "a"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(root, "home", "b"), []byte("b"), 0o644)
	if err := c.Rename("/home/a", "/home/b"); err == nil {
		t.Fatal("plain rename replaced an existing file")
	}
	if err := c.PosixRename("/home/a", "/home/b"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "home", "b")); string(b) != "a" {
		t.Fatalf("b = %q", b)
	}
	if err := c.Link("/home/b", "/home/c"); err != nil {
		t.Fatal(err)
	}
	if err := c.Link("/home/b", "/home/c"); err == nil {
		t.Fatal("link onto an existing name succeeded")
	}
}

func TestSFTPGateBlocksTransfers(t *testing.T) {
	s, root := rootedServer(t)
	gate := make(chan struct{})
	s.GateSFTP(gate)
	os.WriteFile(filepath.Join(root, "home", "g"), []byte("gated"), 0o644)
	c := sftpClient(t, s)
	f, err := c.Open("/home/g")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(f); got <- b }()
	select {
	case <-got:
		t.Fatal("read finished without a gate token")
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	if b := <-got; !bytes.Equal(b, []byte("gated")) {
		t.Fatalf("read %q", b)
	}
}

func TestSFTPHostileListing(t *testing.T) {
	s, _ := rootedServer(t)
	s.HostileSFTP("x/..", `a\b`, "nul\x00", "\xff", "A", "a")
	c := sftpClient(t, s)
	fis, err := c.ReadDir("/hostile")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, fi := range fis {
		names = append(names, fi.Name())
	}
	// The client applies path.Base: "x/.." arrives as "..".
	want := []string{"..", `a\b`, "nul\x00", "\xff", "A", "a"}
	if len(names) != len(want) {
		t.Fatalf("names %q", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names %q, want %q", names, want)
		}
	}
	if fi, err := c.Stat("/hostile/A"); err != nil || fi.Size() != 1<<40 {
		t.Fatalf("hostile size %v %v", fi, err)
	}
}

func TestSFTPRefusedWithoutRoot(t *testing.T) {
	s := Start(t)
	cc := &ssh.ClientConfig{User: "u", Auth: []ssh.AuthMethod{ssh.Password("x")}, HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	conn, err := ssh.Dial("tcp", s.Addr(), cc)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sftp.NewClient(conn); err == nil {
		t.Fatal("sftp subsystem accepted with no root")
	}
}
