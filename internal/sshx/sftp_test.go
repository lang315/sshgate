package sshx

import (
	"errors"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

func sftpManager(t *testing.T, root string) (*Manager, *sshtest.Server) {
	t.Helper()
	s := sshtest.Start(t)
	if root != "" {
		s.ServeSFTP(root)
	}
	m := NewManager(DialConfig{Host: s.Host, Port: s.Port, User: "u", Password: "p", Auth: "password",
		HostKey: s.Fingerprint(), StrictHostKey: true})
	t.Cleanup(m.Close)
	return m, s
}

func TestManagerSFTPSharedAndFresh(t *testing.T) {
	m, _ := sftpManager(t, t.TempDir())
	a, err := m.SFTP()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.SFTP()
	if a != b {
		t.Fatal("SFTP() did not reuse the shared client")
	}
	if home, err := a.RealPath("."); err != nil || home != "/home" {
		t.Fatalf("home %q %v", home, err)
	}
	j, err := m.NewSFTP()
	if err != nil {
		t.Fatal(err)
	}
	if j == a {
		t.Fatal("NewSFTP returned the shared client")
	}
	j.Close()
	if _, err := a.Getwd(); err != nil {
		t.Fatalf("closing a job client broke the shared one: %v", err)
	}
}

func TestManagerSFTPNoSubsystem(t *testing.T) {
	m, _ := sftpManager(t, "")
	if _, err := m.SFTP(); !errors.Is(err, ErrNoSFTP) {
		t.Fatalf("err %v, want ErrNoSFTP", err)
	}
	if _, err := m.NewSFTP(); !errors.Is(err, ErrNoSFTP) {
		t.Fatalf("err %v, want ErrNoSFTP", err)
	}
}

func TestManagerSFTPDroppedWithConnection(t *testing.T) {
	m, _ := sftpManager(t, t.TempDir())
	a, err := m.SFTP()
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := a.Getwd(); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shared client still works after Close")
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, err := m.SFTP() // redials
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("a closed client was reused after redial")
	}
}
