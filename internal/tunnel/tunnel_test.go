package tunnel

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

func client(t *testing.T) *ssh.Client {
	t.Helper()
	s := sshtest.Start(t)
	c, err := ssh.Dial("tcp", s.Addr(), &ssh.ClientConfig{User: "u", Auth: []ssh.AuthMethod{ssh.Password("x")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func echo(t *testing.T) (host string, port int) {
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
	a := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", a.Port
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	return ln.Addr().String()
}

func ping(t *testing.T, c net.Conn) {
	t.Helper()
	c.Write([]byte("ping"))
	b := make([]byte, 4)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, b); err != nil || string(b) != "ping" {
		t.Fatalf("got %q %v", b, err)
	}
}

func waitConns(t *testing.T, f *Forward, n int) {
	t.Helper()
	for i := 0; i < 200 && f.Conns() != n; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if f.Conns() != n {
		t.Fatalf("conns %d, want %d", f.Conns(), n)
	}
}

func TestLocal(t *testing.T) {
	c := client(t)
	h, p := echo(t)
	f, err := Local(c, freePort(t), net.JoinHostPort(h, strconv.Itoa(p)), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	conn, err := net.Dial("tcp", f.Addr())
	if err != nil {
		t.Fatal(err)
	}
	ping(t, conn)
	waitConns(t, f, 1)
	f.Close() // ends the open connection too
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection survived Close")
	}
	<-f.Done()
	if f.Err() != nil || f.Total() != 1 {
		t.Fatalf("err %v total %d", f.Err(), f.Total())
	}
}

func TestLocalPortInUse(t *testing.T) {
	c := client(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	if _, err := Local(c, ln.Addr().String(), "127.0.0.1:1", nil); err == nil {
		t.Fatal("bound a port in use")
	}
}

func TestRemote(t *testing.T) {
	c := client(t)
	h, p := echo(t)
	f, err := Remote(c, freePort(t), net.JoinHostPort(h, strconv.Itoa(p)), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	conn, err := net.Dial("tcp", f.Addr()) // sshtest listens on this machine's loopback
	if err != nil {
		t.Fatal(err)
	}
	ping(t, conn)
	conn.Close()
}

func socksConnect(t *testing.T, proxy string, req []byte) (net.Conn, byte) {
	t.Helper()
	c, err := net.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte{5, 1, 0})
	r := make([]byte, 2)
	io.ReadFull(c, r)
	if r[0] != 5 || r[1] != 0 {
		t.Fatalf("method reply %v", r)
	}
	c.Write(req)
	rep := make([]byte, 10)
	if _, err := io.ReadFull(c, rep); err != nil {
		t.Fatal(err)
	}
	return c, rep[1]
}

func TestDynamic(t *testing.T) {
	c := client(t)
	_, p := echo(t)
	f, err := Dynamic(c, freePort(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	port := binary.BigEndian.AppendUint16(nil, uint16(p))
	// IPv4.
	conn, code := socksConnect(t, f.Addr(), append([]byte{5, 1, 0, 1, 127, 0, 0, 1}, port...))
	if code != 0 {
		t.Fatalf("ipv4 reply %d", code)
	}
	ping(t, conn)
	conn.Close()
	// Domain name, resolved by the server side.
	name := "localhost"
	conn, code = socksConnect(t, f.Addr(), append(append([]byte{5, 1, 0, 3, byte(len(name))}, name...), port...))
	if code != 0 {
		t.Fatalf("domain reply %d", code)
	}
	ping(t, conn)
	conn.Close()
	// BIND is not supported.
	conn, code = socksConnect(t, f.Addr(), append([]byte{5, 2, 0, 1, 127, 0, 0, 1}, port...))
	conn.Close()
	if code != 7 {
		t.Fatalf("bind reply %d, want 7", code)
	}
}

func TestDoneWhenClientDies(t *testing.T) {
	c := client(t)
	f, err := Remote(c, freePort(t), "127.0.0.1:1", nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	select {
	case <-f.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("remote forward outlived its client")
	}
	if f.Err() == nil {
		t.Fatal("no error after the client died")
	}
}
