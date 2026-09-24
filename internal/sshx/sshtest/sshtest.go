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
	"testing"

	"golang.org/x/crypto/ssh"
)

type Server struct {
	Host    string
	Port    int
	Execs   chan string   // commands received via exec
	Release chan struct{} // close to let blocked execs finish
	done    chan struct{}
}

func Start(t testing.TB) *Server {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback:  func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil },
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil },
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port,
		Execs: make(chan string, 16), Release: make(chan struct{}), done: make(chan struct{})}
	t.Cleanup(func() { close(s.done); ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serveConn(nc, cfg)
		}
	}()
	return s
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
