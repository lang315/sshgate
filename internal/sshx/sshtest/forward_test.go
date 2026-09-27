package sshtest

import (
	"io"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

func dial(t *testing.T, s *Server) *ssh.Client {
	t.Helper()
	c, err := ssh.Dial("tcp", s.Addr(), &ssh.ClientConfig{User: "u", Auth: []ssh.AuthMethod{ssh.Password("x")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().String()
}

func roundTrip(t *testing.T, c net.Conn) {
	t.Helper()
	defer c.Close()
	c.Write([]byte("ping"))
	b := make([]byte, 4)
	if _, err := io.ReadFull(c, b); err != nil || string(b) != "ping" {
		t.Fatalf("got %q %v", b, err)
	}
}

func TestDirectTCPIP(t *testing.T) {
	s := Start(t)
	c := dial(t, s)
	conn, err := c.Dial("tcp", echoServer(t))
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, conn)
}

func TestRemoteForward(t *testing.T) {
	s := Start(t)
	c := dial(t, s)
	ln, err := c.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		in, err := ln.Accept()
		if err != nil {
			return
		}
		io.Copy(in, in)
		in.Close()
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, conn)
}

func TestRemoteForwardRefusals(t *testing.T) {
	s := Start(t)
	c := dial(t, s)
	if _, err := c.Listen("tcp", "0.0.0.0:0"); err == nil {
		t.Fatal("non-loopback bind accepted")
	}
	s.RefuseForward()
	if _, err := c.Listen("tcp", "127.0.0.1:0"); err == nil {
		t.Fatal("RefuseForward ignored")
	}
}
