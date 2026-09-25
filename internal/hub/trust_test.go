package hub

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/lang315/ssh-mcp/internal/sshx/sshtest"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type openResult struct {
	Status      string `json:"status"`
	ID          string `json:"id"`
	Server      string `json:"server"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Fingerprint string `json:"fingerprint"`
	KeyType     string `json:"keyType"`
	KnownHosts  string `json:"knownHosts"`
	Pinned      string `json:"pinned"`
	Presented   string `json:"presented"`
}

// trustHub is an unlocked vault with one unpinned password server "box" on
// an in-process sshd, a UI door, and an empty temp known_hosts.
func trustHub(t *testing.T) (*Hub, *sshtest.Server, *rpc.Client, chan note) {
	t.Helper()
	srv := sshtest.Start(t)
	k, mk, err := config.NewKDF("pw")
	if err != nil {
		t.Fatal(err)
	}
	h, _ := newHubAt(t, &config.File{Version: 1, KDF: &k, Servers: []config.Server{
		{Name: "box", Host: srv.Host, Port: srv.Port, User: "u", Auth: "password"}}}, mk)
	h.o.KnownHostsPath = filepath.Join(t.TempDir(), "known_hosts")
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	c, _, notes := startTermDoor(t, h)
	return h, srv, c, notes
}

func openTerm(t *testing.T, c *rpc.Client, id string, trust map[string]string) openResult {
	t.Helper()
	p := map[string]any{"id": id, "server": "box", "rows": 24, "cols": 80}
	if trust != nil {
		p["trustHostKey"] = trust
	}
	var r openResult
	if err := c.Call(context.Background(), "term.open", p, &r); err != nil {
		t.Fatalf("term.open %s: %v", id, err)
	}
	return r
}

func trusting(r openResult) map[string]string {
	return map[string]string{"fingerprint": r.Fingerprint, "keyType": r.KeyType}
}

func TestTrustPinsExactlyTheConfirmedKey(t *testing.T) {
	h, srv, c, notes := trustHub(t)
	before, _ := os.ReadFile(h.o.StorePath)
	r := openTerm(t, c, "t1", nil)
	if r.Status != "hostKeyUnknown" || r.Fingerprint != srv.Fingerprint() || r.KeyType != "ssh-ed25519" ||
		r.Server != "box" || r.Host != srv.Host || r.Port != srv.Port || r.User != "u" || r.KnownHosts != "absent" {
		t.Fatalf("first open: %+v", r)
	}
	if after, _ := os.ReadFile(h.o.StorePath); !bytes.Equal(before, after) {
		t.Fatal("an unconfirmed key was recorded")
	}

	// A wrong fingerprint asks again with the real one and records nothing.
	w := openTerm(t, c, "t1", map[string]string{"fingerprint": "SHA256:wrong", "keyType": "ssh-ed25519"})
	if w.Status != "hostKeyUnknown" || w.Fingerprint != srv.Fingerprint() {
		t.Fatalf("wrong trust: %+v", w)
	}
	if after, _ := os.ReadFile(h.o.StorePath); !bytes.Equal(before, after) {
		t.Fatal("a wrong trust was recorded")
	}

	// The right fingerprint, same id: opens and pins key and algorithm.
	if o := openTerm(t, c, "t1", trusting(r)); o.Status != "open" || o.ID != "t1" {
		t.Fatalf("trusted open: %+v", o)
	}
	f, _ := config.Load(h.o.StorePath)
	if s, _ := f.FindServer("box"); s.HostKey != srv.Fingerprint() || s.HostKeyAlgo != "ssh-ed25519" {
		t.Fatalf("pin: %+v", s)
	}
	// A second tab reuses the trusted connection; the first stays open.
	if o := openTerm(t, c, "t2", nil); o.Status != "open" {
		t.Fatalf("second tab: %+v", o)
	}
	noNote(t, notes, "term.exit", "t1", 300*time.Millisecond)
}

func TestRotatedKeyIsAMismatch(t *testing.T) {
	h, srv, c, _ := trustHub(t)
	openTerm(t, c, "t1", trusting(openTerm(t, c, "t1", nil)))
	old := srv.Fingerprint()
	srv.RotateHostKey(t)
	h.Registry().Close("box") // drop the live connection so the next open redials
	r := openTerm(t, c, "t2", nil)
	if r.Status != "hostKeyMismatch" || r.Pinned != old || r.Presented != srv.Fingerprint() ||
		r.Host != srv.Host || r.Port != srv.Port || r.User != "u" {
		t.Fatalf("got %+v", r)
	}
}

func TestForgetOrDeleteThenOpenPromptsAgain(t *testing.T) {
	for _, method := range []string{"servers.forgetHostKey", "servers.delete"} {
		_, srv, c, notes := trustHub(t)
		openTerm(t, c, "t1", trusting(openTerm(t, c, "t1", nil)))
		if err := c.Call(context.Background(), method, map[string]string{"name": "box"}, nil); err != nil {
			t.Fatal(err)
		}
		waitNote(t, notes, "term.exit", "t1")
		if method == "servers.delete" { // same endpoint, added again: its pin went with it
			if err := save(c, "", config.ServerInput{Name: "box", Host: srv.Host, Port: srv.Port, User: "u", Auth: "password"}); err != nil {
				t.Fatal(err)
			}
		}
		if r := openTerm(t, c, "t2", nil); r.Status != "hostKeyUnknown" || r.Fingerprint != srv.Fingerprint() {
			t.Fatalf("%s: %+v", method, r)
		}
	}
}

// An edit that lands between the dial and the record must not pin the old
// endpoint's key on the new one.
func TestTrustRecordSkippedWhenEndpointMoved(t *testing.T) {
	h, srv, _, _ := trustHub(t)
	before, _ := os.ReadFile(h.o.StorePath)
	dc, err := h.Resolve("box")
	if err != nil {
		t.Fatal(err)
	}
	dc.HostKey, dc.HostKeyAlgo = srv.Fingerprint(), "ssh-ed25519"
	dc.Port++ // the dial ran against the old port
	if err := h.recordTrust("box", dc); !errors.Is(err, config.ErrPinSkipped) {
		t.Fatalf("got %v", err)
	}
	if after, _ := os.ReadFile(h.o.StorePath); !bytes.Equal(before, after) {
		t.Fatal("pinned a key for a moved endpoint")
	}
}

func TestKnownHostsHintReachesThePrompt(t *testing.T) {
	h, srv, c, _ := trustHub(t)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	other, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))}, other)
	if err := os.WriteFile(h.o.KnownHostsPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := openTerm(t, c, "t1", nil); r.KnownHosts != "different" {
		t.Fatalf("got %+v", r)
	}
}

// A trusted open whose pin cannot be recorded fails, and its connection is
// dropped: a trusted-but-unrecorded manager must not stay cached.
func TestTrustedOpenFailsWhenPinNotRecorded(t *testing.T) {
	h, srv, c, notes := trustHub(t)
	r := openTerm(t, c, "t1", nil)
	dc, err := h.Resolve("box")
	if err != nil {
		t.Fatal(err)
	}
	dc.HostKey, dc.HostKeyAlgo = r.Fingerprint, r.KeyType
	cached := h.Registry().Get("box", dc) // the manager the trusted open will reuse
	recordHostKey = func(string, string, string, int, string, string, []byte) error { return config.ErrPinSkipped }
	t.Cleanup(func() { recordHostKey = config.RecordHostKey })
	before, _ := os.ReadFile(h.o.StorePath)

	err = c.Call(context.Background(), "term.open",
		map[string]any{"id": "t1", "server": "box", "rows": 24, "cols": 80, "trustHostKey": trusting(r)}, nil)
	if err == nil {
		t.Fatal("trusted open succeeded without recording the pin")
	}
	if after, _ := os.ReadFile(h.o.StorePath); !bytes.Equal(before, after) {
		t.Fatal("store changed")
	}
	if h.Registry().Get("box", dc) == cached {
		t.Fatal("trusted manager still cached")
	}
	noNote(t, notes, "term.exit", "t1", 300*time.Millisecond)
	// The id was released and the server is still unpinned.
	if o := openTerm(t, c, "t1", nil); o.Status != "hostKeyUnknown" || o.Fingerprint != srv.Fingerprint() {
		t.Fatalf("after failed trust: %+v", o)
	}
}

// Two trusted opens of one key can both pass the unpinned check before
// either records; the second record must not fail (and close the shared
// connection under the first tab).
func TestSecondTrustOfTheSameKeyRecordsFine(t *testing.T) {
	h, srv, _, _ := trustHub(t)
	dc, err := h.Resolve("box")
	if err != nil {
		t.Fatal(err)
	}
	dc.HostKey, dc.HostKeyAlgo = srv.Fingerprint(), "ssh-ed25519"
	for i := range 2 {
		if err := h.recordTrust("box", dc); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
}
