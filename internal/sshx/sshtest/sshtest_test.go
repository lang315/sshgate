package sshtest

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestShellFlood(t *testing.T) {
	s := Start(t)
	c, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", s.Host, s.Port), &ssh.ClientConfig{
		User: "u", Auth: []ssh.AuthMethod{ssh.Password("p")}, HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	in, _ := sess.StdinPipe()
	out, _ := sess.StdoutPipe()
	if err := sess.RequestPty("xterm", 24, 80, nil); err != nil {
		t.Fatal(err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}
	io.WriteString(in, "flood 100000\n")

	var got []byte
	buf := make([]byte, 32768)
	for !bytes.Contains(got, []byte("FLOOD-DONE")) {
		n, err := out.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			t.Fatalf("read after %d bytes: %v", len(got), err)
		}
	}
	if n := bytes.Count(got, []byte("y")); n < 100000*78/80 {
		t.Fatalf("got %d bytes (%d y) before FLOOD-DONE", len(got), n)
	}
	if len(got) < 100000 {
		t.Fatalf("got %d bytes, want >= 100000", len(got))
	}

	// Echo still works after the flood.
	io.WriteString(in, "hi\n")
	got = got[:0]
	for !bytes.Contains(got, []byte("hi\n")) {
		n, err := out.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			t.Fatal(err)
		}
	}
}
