// Package sshtest is an in-process SSH server for tests that must not need
// Docker. It accepts any password or public key. A "shell" echoes its input
// back until stdin closes, then exits 0. An "exec" reports its command on
// Execs, waits for Release to be closed, writes the command to stdout, and
// exits 0.
package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

type Server struct {
	Host               string
	Port               int
	Execs              chan string   // commands received via exec
	Release            chan struct{} // close to let blocked execs finish
	HostKeyFingerprint string
	done               chan struct{}
	mu                 sync.Mutex
	cfg                *ssh.ServerConfig
}

// Listen starts a server without the testing package. stop closes it.
func Listen() (*Server, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	s := &Server{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port,
		Execs: make(chan string, 16), Release: make(chan struct{}), done: make(chan struct{})}
	if err := s.rotate(); err != nil {
		ln.Close()
		return nil, nil, err
	}
	var once sync.Once
	stop := func() { once.Do(func() { close(s.done); ln.Close() }) }
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			cfg := s.cfg
			s.mu.Unlock()
			go s.serveConn(nc, cfg)
		}
	}()
	return s, stop, nil
}

func Start(t testing.TB) *Server {
	t.Helper()
	s, stop, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return s
}

// rotate generates a fresh host key and installs a new ssh.ServerConfig,
// updating HostKeyFingerprint under s.mu so it always reflects the key new
// connections see.
func (s *Server) rotate() error {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return err
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback:  func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil },
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil },
	}
	cfg.AddHostKey(signer)
	s.mu.Lock()
	s.cfg = cfg
	s.HostKeyFingerprint = ssh.FingerprintSHA256(signer.PublicKey())
	s.mu.Unlock()
	return nil
}

// RotateHostKey makes new connections see a fresh host key, as a MITM would.
func (s *Server) RotateHostKey(t testing.TB) {
	t.Helper()
	if err := s.rotate(); err != nil {
		t.Fatal(err)
	}
}

// Fingerprint returns the current host key fingerprint. Safe to call
// concurrently with RotateHostKey.
func (s *Server) Fingerprint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.HostKeyFingerprint
}

func (s *Server) serveConn(nc net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		nc.Close()
		return
	}
	go func() { <-s.done; conn.Close() }()
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		if nch.ChannelType() != "session" {
			nch.Reject(ssh.UnknownChannelType, "")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go s.session(ch, creqs)
	}
}

func (s *Server) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	finish := func() {
		ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
		ch.Close()
	}
	for req := range reqs {
		switch req.Type {
		case "pty-req", "window-change", "env":
			req.Reply(true, nil)
		case "shell":
			req.Reply(true, nil)
			go func() { io.Copy(ch, ch); finish() }()
		case "exec":
			var p struct{ Cmd string }
			ssh.Unmarshal(req.Payload, &p)
			req.Reply(true, nil)
			go func() {
				select {
				case s.Execs <- p.Cmd:
				case <-s.done:
					return
				}
				select {
				case <-s.Release:
				case <-s.done:
					return
				}
				io.WriteString(ch, p.Cmd)
				finish()
			}()
		default:
			req.Reply(false, nil)
		}
	}
}
