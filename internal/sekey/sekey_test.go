package sekey_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/lang315/sshgate/internal/sekey"
	"github.com/lang315/sshgate/internal/sekey/sekeytest"
	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

func newSigner(t *testing.T) (ssh.Signer, ssh.PublicKey) {
	t.Helper()
	h := sekeytest.Build(t)
	blob, pub, err := sekey.Create(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	spub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return sekey.NewSigner(h, blob, spub), spub
}

func TestCreateThenSignVerifies(t *testing.T) {
	s, pub := newSigner(t)
	data := []byte("hello")
	sig, err := s.Sign(rand.Reader, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Verify(data, sig); err != nil {
		t.Fatal(err)
	}
}

func TestSignerDialsSSHTest(t *testing.T) {
	srv := sshtest.Start(t)
	close(srv.Release) // let the exec answer at once
	s, _ := newSigner(t)
	c, err := ssh.Dial("tcp", srv.Addr(), &ssh.ClientConfig{
		User: "u", Auth: []ssh.AuthMethod{ssh.PublicKeys(s)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(),
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
	out, err := sess.Output("echo ok")
	if err != nil || !strings.Contains(string(out), "ok") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestExitCodesMapToErrors(t *testing.T) {
	h := sekeytest.Build(t)
	blob, _, err := sekey.Create(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	for code, want := range map[string]error{"3": sekey.ErrWrongMac, "5": sekey.ErrUnavailable} {
		t.Setenv("FAKE_EXIT", code)
		if _, _, err := sekey.Sign(context.Background(), h, blob, []byte("x")); err != want {
			t.Fatalf("exit %s: got %v want %v", code, err, want)
		}
	}
	t.Setenv("FAKE_EXIT", "1")
	_, _, err = sekey.Sign(context.Background(), h, blob, []byte("x"))
	if err == nil || err == sekey.ErrWrongMac || err == sekey.ErrUnavailable {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), base64.StdEncoding.EncodeToString(blob)) || strings.Contains(err.Error(), "fake failure") {
		t.Fatalf("error leaks stderr: %v", err)
	}
}

func TestAuthorizedKeyRoundTrips(t *testing.T) {
	_, ecpub, err := sekey.Create(context.Background(), sekeytest.Build(t))
	if err != nil {
		t.Fatal(err)
	}
	line := sekey.AuthorizedKey(ecpub, "k1")
	got, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || comment != "k1" {
		t.Fatalf("%v %q", err, comment)
	}
	want, _ := ssh.NewPublicKey(ecpub)
	if string(got.Marshal()) != string(want.Marshal()) {
		t.Fatal("key differs")
	}
}
