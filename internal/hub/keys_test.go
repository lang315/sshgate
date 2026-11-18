package hub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/lang315/sshgate/internal/sekey/sekeytest"
	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

// keyHub is an unlocked hub with the fake helper, an audit file, and one
// agent server "fk" pinned to an in-process SSH server.
func keyHub(t *testing.T) (h *Hub, dir string, srv *sshtest.Server) {
	t.Helper()
	srv = sshtest.Start(t)
	dir = t.TempDir()
	path := filepath.Join(dir, "servers.json")
	kdf, mk := testVault(t)
	f := &config.File{Version: 1, KDF: kdf, Servers: []config.Server{
		{Name: "fk", Host: srv.Host, Port: srv.Port, User: "u", Auth: "agent", HostKey: srv.Fingerprint()},
	}}
	if err := config.Save(path, f, mk); err != nil {
		t.Fatal(err)
	}
	audit, err := broker.OpenAudit(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { audit.Close() })
	h, err = New(Options{StorePath: path, Audit: audit, KeyHelper: sekeytest.Build(t)})
	if err != nil {
		t.Fatal(err)
	}
	unlockForTest(h)
	t.Cleanup(h.Registry().CloseAll)
	return h, dir, srv
}

func keyServer(srv *sshtest.Server, auth, keyName string) map[string]any {
	return map[string]any{"original": "fk", "server": map[string]any{
		"name": "fk", "host": srv.Host, "port": srv.Port, "user": "u", "auth": auth, "keyPath": "", "keyName": keyName}, "autoAllow": "off"}
}

func auditText(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestKeysCreateListDelete(t *testing.T) {
	h, dir, srv := keyHub(t)
	c, _ := startUI(t, h)
	ctx := context.Background()

	var rawMade json.RawMessage
	if err := c.Call(ctx, "keys.create", map[string]any{"name": "k1"}, &rawMade); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawMade), "usedBy") {
		t.Fatalf("create reply has usedBy: %s", rawMade)
	}
	var made struct{ Name, Type, PublicKey, Created string }
	if err := json.Unmarshal(rawMade, &made); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(made.PublicKey, " k1") {
		t.Fatalf("public key has no name comment: %q", made.PublicKey)
	}
	if made.Name != "k1" || made.Type != "se-p256" || !strings.HasPrefix(made.PublicKey, "ecdsa-sha2-nistp256 ") || made.Created == "" {
		t.Fatalf("create: %+v", made)
	}
	type listed struct {
		Name, Type, PublicKey, Created string
		UsedBy                         []string
	}
	var rawList json.RawMessage
	if err := c.Call(ctx, "keys.list", map[string]any{}, &rawList); err != nil || !strings.Contains(string(rawList), `"usedBy":[]`) {
		t.Fatalf("list json: %v %s", err, rawList)
	}
	var keys []listed
	if err := c.Call(ctx, "keys.list", map[string]any{}, &keys); err != nil || len(keys) != 1 || keys[0].Name != "k1" || len(keys[0].UsedBy) != 0 {
		t.Fatalf("list: %v %+v", err, keys)
	}

	if err := c.Call(ctx, "servers.save", keyServer(srv, "keychain", "k1"), nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, "keys.list", map[string]any{}, &keys); err != nil || len(keys[0].UsedBy) != 1 || keys[0].UsedBy[0] != "fk" {
		t.Fatalf("list after save: %v %+v", err, keys)
	}
	var servers []struct{ Name, KeyName, KeyPublicKey string }
	if err := c.Call(ctx, "servers", map[string]any{}, &servers); err != nil || servers[0].KeyName != "k1" || servers[0].KeyPublicKey != made.PublicKey {
		t.Fatalf("servers: %v %+v", err, servers)
	}
	if err := c.Call(ctx, "keys.delete", map[string]any{"name": "k1"}, nil); err == nil || !strings.Contains(err.Error(), config.ErrKeyInUse.Error()) {
		t.Fatalf("delete in use: %v", err)
	}

	if err := c.Call(ctx, "servers.save", keyServer(srv, "agent", ""), nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, "keys.delete", map[string]any{"name": "k1"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, "keys.list", map[string]any{}, &keys); err != nil || len(keys) != 0 {
		t.Fatalf("list after delete: %v %+v", err, keys)
	}
	if err := c.Call(ctx, "keys.delete", map[string]any{"name": "k1"}, nil); err == nil || !strings.Contains(err.Error(), config.ErrNoKey.Error()) {
		t.Fatalf("delete twice: %v", err)
	}
	a := auditText(t, dir)
	for _, want := range []string{`"action":"keyCreate","key":"k1"`, `"action":"keyDelete","key":"k1"`} {
		if !strings.Contains(a, want) {
			t.Errorf("audit lacks %s:\n%s", want, a)
		}
	}
}

// The write stands even when the reload after it fails: the app must get the
// key (a retry would find the name taken) and status reports storeError.
func TestKeysCreateSurvivesReloadFailure(t *testing.T) {
	h, _, _ := keyHub(t)
	c, _ := startUI(t, h)
	loadStore = func(string) (*config.File, error) { return nil, errors.New("boom") }
	t.Cleanup(func() { loadStore = config.Load })
	var made struct{ Name, PublicKey string }
	if err := c.Call(context.Background(), "keys.create", map[string]any{"name": "k1"}, &made); err != nil {
		t.Fatalf("create after a failed reload: %v", err)
	}
	if made.Name != "k1" || made.PublicKey == "" {
		t.Fatalf("reply: %+v", made)
	}
	f, err := config.Load(h.o.StorePath)
	if err != nil || len(f.Keys) != 1 {
		t.Fatalf("key not on disk: %v", err)
	}
	h.mu.Lock()
	got := h.storeErr
	h.mu.Unlock()
	if got == nil {
		t.Fatal("storeErr not set")
	}
}

func TestKeysStrictParams(t *testing.T) {
	h, _, _ := keyHub(t)
	c, _ := startUI(t, h)
	ctx := context.Background()
	for method, p := range map[string]any{
		"keys.list":   map[string]any{"x": 1},
		"keys.create": map[string]any{"name": "k", "x": 1},
		"keys.delete": map[string]any{"name": "k", "x": 1},
	} {
		var re *rpc.Error
		if err := c.Call(ctx, method, p, nil); !errors.As(err, &re) || re.Code != -32602 {
			t.Errorf("%s: %v", method, err)
		}
	}
}

func TestKeysRefusedWhileLocked(t *testing.T) {
	h, _, _ := keyHub(t)
	c, _ := startUI(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "lock", map[string]any{"stopAuto": true}, nil); err != nil {
		t.Fatal(err)
	}
	for method, p := range map[string]any{
		"keys.list": map[string]any{}, "keys.create": map[string]any{"name": "k1"}, "keys.delete": map[string]any{"name": "k1"},
	} {
		if err := c.Call(ctx, method, p, nil); err == nil || !strings.Contains(err.Error(), ErrLocked.Error()) {
			t.Errorf("%s while locked: %v", method, err)
		}
	}
}

func TestKeysCreateRefusesBeforeHelper(t *testing.T) {
	h, _, _ := keyHub(t)
	c, _ := startUI(t, h)
	ctx := context.Background()
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv("FAKE_MARKER", marker)
	if err := c.Call(ctx, "keys.create", map[string]any{"name": "dup"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil { // positive control: a valid create runs the helper
		t.Fatal("a valid create did not run the helper")
	}
	os.Remove(marker)
	if err := c.Call(ctx, "keys.create", map[string]any{"name": "dup"}, nil); err == nil || !strings.Contains(err.Error(), config.ErrKeyExists.Error()) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := c.Call(ctx, "keys.create", map[string]any{"name": "bad name!"}, nil); err == nil {
		t.Fatal("invalid name accepted")
	}
	if err := c.Call(ctx, "lock", map[string]any{"stopAuto": true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, "keys.create", map[string]any{"name": "other"}, nil); err == nil || !strings.Contains(err.Error(), ErrLocked.Error()) {
		t.Fatalf("locked: %v", err)
	}
	if err := c.Call(ctx, "keys.create", map[string]any{"name": "bad name!"}, nil); err == nil || !strings.Contains(err.Error(), ErrLocked.Error()) {
		t.Fatalf("invalid name on a locked hub: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the helper ran for a refused create")
	}
}

// The helper's blob goes vault -> hub memory -> helper stdin and nowhere else.
func TestKeyBlobNeverLeaves(t *testing.T) {
	h, dir, srv := keyHub(t)
	c, w, notes := startTermDoor(t, h)
	_ = w
	ctx := context.Background()
	var seen bytes.Buffer
	call := func(method string, p any) {
		t.Helper()
		var raw json.RawMessage
		if err := c.Call(ctx, method, p, &raw); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		seen.Write(raw)
	}
	call("keys.create", map[string]any{"name": "k1"})
	call("keys.list", map[string]any{})
	call("servers.save", keyServer(srv, "keychain", "k1"))
	call("servers", map[string]any{})
	call("term.open", map[string]any{"id": "t1", "server": "fk", "rows": 24, "cols": 80})
	if !strings.Contains(seen.String(), `"id":"t1"`) {
		t.Fatalf("term.open did not connect: %s", seen.String())
	}
	call("term.close", map[string]any{"id": "t1"})
	call("audit.read", map[string]any{})
	// keys.create writes its audit record before it replies, and the queue
	// drains in order: the "end" record's notification is the last one. Read
	// everything up to it.
	call("keys.create", map[string]any{"name": "end"})
	var gotEnd bool
	for deadline := time.After(10 * time.Second); !gotEnd; {
		select {
		case n := <-notes:
			seen.WriteString(n.method)
			seen.Write(n.params)
			gotEnd = gotEnd || n.method == "audit.appended" && strings.Contains(string(n.params), `"key":"end"`)
		case <-deadline:
			t.Fatal("the last audit notification never arrived")
		}
	}
	for len(notes) > 0 {
		n := <-notes
		seen.WriteString(n.method)
		seen.Write(n.params)
	}

	// the blob, read back from the vault the way the hub does
	mk := testMK
	f, err := config.Load(h.o.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	blob, _, err := config.KeyBlob(f, mk, "k1")
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(blob)
	for name, hay := range map[string][]byte{"replies and notifications": seen.Bytes(), "audit": []byte(auditText(t, dir))} {
		if bytes.Contains(hay, []byte(b64)) || bytes.Contains(hay, blob) || bytes.Contains(hay, []byte(base64.RawStdEncoding.EncodeToString(blob))) {
			t.Errorf("%s carry the key blob", name)
		}
	}
	if seen.Len() == 0 {
		t.Fatal("nothing captured")
	}
}
