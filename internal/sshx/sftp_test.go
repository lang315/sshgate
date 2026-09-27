package sshx

import (
	"errors"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/sshx/sshtest"
	"github.com/pkg/sftp"
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
	b, err := m.SFTP()
	if err != nil {
		t.Fatal(err)
	}
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

// TestManagerSFTPSetupTimesOut checks that a server which accepts the sftp
// subsystem and then goes silent doesn't hang SFTP() forever.
func TestManagerSFTPSetupTimesOut(t *testing.T) {
	orig := sftpSetupTimeout
	sftpSetupTimeout = 200 * time.Millisecond
	t.Cleanup(func() { sftpSetupTimeout = orig })

	m, s := sftpManager(t, t.TempDir())
	s.StallSFTP()

	start := time.Now()
	if _, err := m.SFTP(); err == nil {
		t.Fatal("want an error from a stalled handshake")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("SFTP() took %s to time out", elapsed)
	}
}

// TestManagerSFTPRedialsAfterChannelDeath kills only the shared SFTP client's
// channel (the SSH connection stays up) and checks the manager notices and
// opens a fresh one on the next SFTP() call. This exercises the c.Wait()
// watcher in SFTP(); deleting it makes this test hang past its deadline.
func TestManagerSFTPRedialsAfterChannelDeath(t *testing.T) {
	m, _ := sftpManager(t, t.TempDir())
	a, err := m.SFTP()
	if err != nil {
		t.Fatal(err)
	}
	a.Close() // kills the sftp channel only

	deadline := time.Now().Add(2 * time.Second)
	var b *sftp.Client
	for {
		b, err = m.SFTP()
		if err == nil && b != a {
			if _, gerr := b.Getwd(); gerr == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("SFTP() did not recover after channel death: b=%v err=%v", b, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestManagerSFTPRedialsAfterConnectionDeath kills the underlying SSH
// connection directly (no Manager.Close()) and checks SFTP() redials and
// opens a fresh client on the new connection. This exercises the stale-client
// check in SFTP() (m.sftp.on != m.client); deleting it makes this test reuse
// a client on a dead connection and fail the Getwd check.
func TestManagerSFTPRedialsAfterConnectionDeath(t *testing.T) {
	m, _ := sftpManager(t, t.TempDir())
	a, err := m.SFTP()
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	c := m.client
	m.mu.Unlock()
	c.Close()

	deadline := time.Now().Add(2 * time.Second)
	var b *sftp.Client
	for {
		b, err = m.SFTP()
		if err == nil && b != a {
			if _, gerr := b.Getwd(); gerr == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("SFTP() did not redial after connection death: b=%v err=%v", b, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
