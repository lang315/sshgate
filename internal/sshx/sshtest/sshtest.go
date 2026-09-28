// Package sshtest is an in-process SSH server for tests that must not need
// Docker. It accepts any password or public key. A "shell" echoes its input
// back until stdin closes, then exits 0; an input line "flood <N>" also
// makes it write N bytes of 80-column "yyyy" lines, then "FLOOD-DONE". An "exec" reports its command on
// Execs, waits for Release to be closed, writes the command to stdout, and
// exits 0. It serves direct-tcpip and loopback tcpip-forward channels.
package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	hostKey            ssh.PublicKey
	sftpRoot           string        // "" refuses the sftp subsystem
	sftpGate           chan struct{} // nil: ungated; else each SFTP ReadAt/WriteAt takes one value
	sftpHostile        []string      // names listed in /hostile (see sftp.go)
	sftpStall          bool          // accept the subsystem request but never serve it
	refuseFwd          bool          // tcpip-forward always fails
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
	s.hostKey = signer.PublicKey()
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

// PublicKey returns the current host key. Safe to call concurrently with
// RotateHostKey.
func (s *Server) PublicKey() ssh.PublicKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostKey
}

// Addr is host:port for ssh.Dial.
func (s *Server) Addr() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// ServeSFTP turns on the sftp subsystem, rooted in root (home is <root>/home).
// The home directory is created immediately so a caller may write into it
// before any SFTP connection opens.
func (s *Server) ServeSFTP(root string) {
	os.MkdirAll(filepath.Join(root, "home"), 0o755)
	s.mu.Lock()
	s.sftpRoot = root
	s.mu.Unlock()
}

// GateSFTP makes each SFTP ReadAt/WriteAt wait for one value from gate (nil: no gate).
func (s *Server) GateSFTP(gate chan struct{}) { s.mu.Lock(); s.sftpGate = gate; s.mu.Unlock() }

// HostileSFTP sets the names /hostile lists.
func (s *Server) HostileSFTP(names ...string) { s.mu.Lock(); s.sftpHostile = names; s.mu.Unlock() }

// StallSFTP makes the sftp subsystem request succeed but never serve the
// channel, so the client's setup handshake hangs. For testing setup timeouts.
func (s *Server) StallSFTP() { s.mu.Lock(); s.sftpStall = true; s.mu.Unlock() }

func (s *Server) serveConn(nc net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		nc.Close()
		return
	}
	go func() { <-s.done; conn.Close() }()
	go s.globalRequests(conn, reqs)
	for nch := range chans {
		switch nch.ChannelType() {
		case "session":
			ch, creqs, err := nch.Accept()
			if err != nil {
				continue
			}
			go s.session(ch, creqs)
		case "direct-tcpip":
			go directTCPIP(nch)
		default:
			nch.Reject(ssh.UnknownChannelType, "")
		}
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
			go func() { shell(ch); finish() }()
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
		case "subsystem":
			var p struct{ Name string }
			ssh.Unmarshal(req.Payload, &p)
			s.mu.Lock()
			root := s.sftpRoot
			stall := s.sftpStall
			s.mu.Unlock()
			if p.Name != "sftp" || root == "" {
				req.Reply(false, nil)
				continue
			}
			req.Reply(true, nil)
			if stall {
				continue // accepted, but never read from or written to
			}
			go s.serveSFTP(ch, root)
		default:
			req.Reply(false, nil)
		}
	}
}

// shell echoes input until EOF. A complete line "flood <N>" (after the
// echo) writes N bytes of output, blocking on the SSH window like any
// writer, then "FLOOD-DONE". Input is read serially, so anything typed
// during a flood echoes after it.
func shell(ch ssh.Channel) {
	buf := make([]byte, 4096)
	var line []byte
	for {
		n, err := ch.Read(buf)
		if n > 0 {
			ch.Write(buf[:n])
			for _, b := range buf[:n] {
				if b != '\r' && b != '\n' {
					if len(line) < 256 {
						line = append(line, b)
					}
					continue
				}
				f := strings.Fields(string(line))
				line = line[:0]
				if len(f) == 2 && f[0] == "flood" {
					if size, err := strconv.Atoi(f[1]); err == nil && size >= 0 {
						flood(ch, size)
					}
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func flood(w io.Writer, n int) {
	row := strings.Repeat("y", 78) + "\r\n"
	block := []byte(strings.Repeat(row, 32768/len(row)))
	for n > 0 {
		b := block[:min(n, len(block))]
		if _, err := w.Write(b); err != nil {
			return
		}
		n -= len(b)
	}
	io.WriteString(w, "\r\nFLOOD-DONE\r\n")
}
