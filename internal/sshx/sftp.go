package sshx

import (
	"errors"
	"io"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// ErrNoSFTP: the server refused the sftp subsystem.
var ErrNoSFTP = errors.New("server has no SFTP subsystem")

// sftpSetupTimeout bounds newSFTP's handshake; a var so tests can shorten it.
var sftpSetupTimeout = 30 * time.Second

// sftpConn is the shared SFTP client and the SSH client it runs on.
type sftpConn struct {
	on *ssh.Client
	c  *sftp.Client
}

// newSFTP opens the sftp subsystem on its own channel. Unlike
// sftp.NewClient, it closes the session when the subsystem is refused.
func newSFTP(client *ssh.Client) (*sftp.Client, error) {
	s, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	w, err := s.StdinPipe()
	if err != nil {
		s.Close()
		return nil, err
	}
	r, err := s.StdoutPipe()
	if err != nil {
		s.Close()
		return nil, err
	}
	// A server that accepts the subsystem (or the channel) and then goes
	// silent would otherwise block RequestSubsystem/NewClientPipe until the
	// whole SSH connection closes. Bound the handshake and fail closed.
	watchdog := time.AfterFunc(sftpSetupTimeout, func() { s.Close() })
	if err := s.RequestSubsystem("sftp"); err != nil {
		s.Close()
		if !watchdog.Stop() {
			return nil, errors.New("SFTP setup timed out")
		}
		if err.Error() == "ssh: subsystem request failed" {
			return nil, ErrNoSFTP
		}
		return nil, err
	}
	c, err := sftp.NewClientPipe(r, sessionCloser{w, s})
	if !watchdog.Stop() {
		s.Close()
		if err == nil {
			c.Close()
		}
		return nil, errors.New("SFTP setup timed out")
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	go func() { c.Wait(); s.Close() }()
	return c, nil
}

// sessionCloser makes sftp.Client.Close close the whole channel. Closing
// only stdin sends EOF, and Close then waits for the server to close the
// channel, which a stalled server never does. A channel close is answered by
// the peer's SSH layer, not the sftp process (RFC 4254 5.3), so this one
// returns. The session may then be closed twice (see newSFTP); the second
// Close just returns an error nobody reads.
type sessionCloser struct {
	io.Writer
	s *ssh.Session
}

func (w sessionCloser) Close() error { return w.s.Close() }

// SFTP returns the shared client for listings and single operations. It
// belongs to the current *ssh.Client: a redial replaces it, and it is
// dropped when its own channel dies. Closes happen outside m.mu.
func (m *Manager) SFTP() (*sftp.Client, error) {
	m.mu.Lock()
	if err := m.ensure(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	var stale *sftp.Client
	if m.sftp != nil && m.sftp.on != m.client {
		stale, m.sftp = m.sftp.c, nil
	}
	if s := m.sftp; s != nil {
		m.mu.Unlock()
		return s.c, nil
	}
	client := m.client
	m.mu.Unlock()
	if stale != nil {
		go stale.Close()
	}
	c, err := newSFTP(client)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	switch {
	case m.client != client:
		m.mu.Unlock()
		go c.Close()
		return nil, errors.New("connection closed while opening SFTP")
	case m.sftp != nil: // another caller won the race
		keep := m.sftp.c
		m.mu.Unlock()
		go c.Close()
		return keep, nil
	}
	m.sftp = &sftpConn{on: client, c: c}
	m.mu.Unlock()
	go func() {
		c.Wait()
		m.mu.Lock()
		if m.sftp != nil && m.sftp.c == c {
			m.sftp = nil
		}
		m.mu.Unlock()
	}()
	return c, nil
}

// NewSFTP opens a client on its own channel for one job; the caller closes it.
func (m *Manager) NewSFTP() (*sftp.Client, error) {
	m.mu.Lock()
	err := m.ensure()
	client := m.client
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return newSFTP(client)
}
