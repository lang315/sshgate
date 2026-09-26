package hub

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

type filesFixture struct {
	h     *Hub
	c     *rpc.Client
	w     io.Writer // raw door input, for notifications
	notes chan note
	srv   *sshtest.Server
	root  string // the SFTP root on disk; the server's /home is <root>/home
	store string
}

// filesHub: an unlocked vault with "fs" (pinned) and "new" (no pin), both
// key auth against one in-process SSH server that serves SFTP.
func filesHub(t *testing.T) filesFixture {
	t.Helper()
	srv := sshtest.Start(t)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "home"), 0o755)
	srv.ServeSFTP(root)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "id")
	os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600)
	f := &config.File{Version: 1, Servers: []config.Server{
		{Name: "fs", Host: srv.Host, Port: srv.Port, User: "u", Auth: "key", KeyPath: keyPath, HostKey: srv.Fingerprint()},
		{Name: "new", Host: srv.Host, Port: srv.Port, User: "u", Auth: "key", KeyPath: keyPath},
	}}
	var mk []byte
	f.KDF, mk = testVault(t)
	h, store := newHubAt(t, f, mk)
	unlockForTest(h)
	c, w, notes := startTermDoor(t, h)
	return filesFixture{h: h, c: c, w: w, notes: notes, srv: srv, root: root, store: store}
}

// waitNote returns the params of the next notification method for id,
// skipping everything else.
func waitNote(t *testing.T, notes chan note, method, id string) map[string]any {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case n := <-notes:
			if n.method != method {
				continue
			}
			var p map[string]any
			json.Unmarshal(n.params, &p)
			if p["id"] == id {
				return p
			}
		case <-deadline:
			t.Fatalf("no %s for %s", method, id)
		}
	}
}

func fileRecords(t *testing.T, store string) []map[string]any {
	t.Helper()
	_, recs := readAudit(t, store)
	var out []map[string]any
	for _, r := range recs {
		if r["kind"] == "file" {
			out = append(out, r)
		}
	}
	return out
}

func TestFilesListMkdirRename(t *testing.T) {
	fx := filesHub(t)
	ctx := context.Background()
	os.WriteFile(filepath.Join(fx.root, "home", "a.txt"), []byte("abc"), 0o644)
	var l struct {
		Status  string `json:"status"`
		Path    string `json:"path"`
		Entries []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"entries"`
	}
	if err := fx.c.Call(ctx, "files.list", map[string]any{"server": "fs", "path": ""}, &l); err != nil {
		t.Fatal(err)
	}
	if l.Status != "listed" || l.Path != "/home" || len(l.Entries) != 1 || l.Entries[0].Name != "a.txt" {
		t.Fatalf("%+v", l)
	}
	if err := fx.c.Call(ctx, "files.mkdir", map[string]any{"server": "fs", "path": "/home/d"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := fx.c.Call(ctx, "files.rename", map[string]any{"server": "fs", "from": "/home/a.txt", "to": "/home/d/b.txt"}, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(fx.root, "home", "d", "b.txt")); string(b) != "abc" {
		t.Fatal("rename")
	}
	recs := fileRecords(t, fx.store)
	if len(recs) != 2 || recs[0]["action"] != "mkdir" || recs[1]["action"] != "rename" ||
		recs[1]["from"] != "/home/a.txt" || recs[1]["to"] != "/home/d/b.txt" || recs[0]["server"] != "fs" {
		t.Fatalf("audit %v", recs)
	}
}

func TestFilesStrictParams(t *testing.T) {
	fx := filesHub(t)
	ctx := context.Background()
	for _, raw := range []string{
		`{"server":"fs","Path":"/etc"}`,            // case variant
		`{"server":"fs","path":"","extra":1}`,      // unknown key
		`{"server":"fs","server":"new","path":""}`, // duplicate key
		`["fs"]`, // not an object
	} {
		var re *rpc.Error
		err := fx.c.Call(ctx, "files.list", json.RawMessage(raw), nil)
		if !errors.As(err, &re) || re.Code != -32602 {
			t.Errorf("%s: want invalid params, got %v", raw, err)
		}
	}
}

func TestFilesNeedUnlockAndPaths(t *testing.T) {
	fx := filesHub(t)
	ctx := context.Background()
	if err := fx.c.Call(ctx, "files.mkdir", map[string]any{"server": "fs", "path": "rel"}, nil); err == nil {
		t.Fatal("relative mkdir accepted")
	}
	if err := fx.c.Call(ctx, "files.rename", map[string]any{"server": "fs", "from": "/home", "to": "/x"}, nil); err == nil {
		t.Fatal("renamed home")
	}
	fx.h.Lock()
	err := fx.c.Call(ctx, "files.list", map[string]any{"server": "fs", "path": ""}, nil)
	if err == nil || !strings.Contains(err.Error(), ErrLocked.Error()) {
		t.Fatalf("locked list: %v", err)
	}
}

func TestFilesListTrustsAnUnpinnedHost(t *testing.T) {
	fx := filesHub(t)
	ctx := context.Background()
	var r map[string]any
	if err := fx.c.Call(ctx, "files.list", map[string]any{"server": "new", "path": ""}, &r); err != nil {
		t.Fatal(err)
	}
	if r["status"] != "hostKeyUnknown" {
		t.Fatalf("%v", r)
	}
	trust := map[string]any{"fingerprint": r["fingerprint"], "keyType": r["keyType"]}
	if err := fx.c.Call(ctx, "files.list", map[string]any{"server": "new", "path": "", "trustHostKey": trust}, &r); err != nil {
		t.Fatal(err)
	}
	if r["status"] != "listed" {
		t.Fatalf("%v", r)
	}
	f, _ := config.Load(fx.store)
	if s, _ := f.FindServer("new"); s.HostKey != fx.srv.Fingerprint() {
		t.Fatalf("pin %q", s.HostKey)
	}
}

func TestFilesPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	fx := filesHub(t)
	d := filepath.Join(fx.root, "home", "shut")
	os.Mkdir(d, 0o000)
	t.Cleanup(func() { os.Chmod(d, 0o755) })
	err := fx.c.Call(context.Background(), "files.list", map[string]any{"server": "fs", "path": "/home/shut"}, nil)
	if err == nil || err.Error() != "permission denied" {
		t.Fatalf("err %v", err)
	}
}
