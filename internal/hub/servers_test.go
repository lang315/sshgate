package hub

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/rpc"
)

// waitNote waits for notification method for terminal id, skipping others.
func waitNote(t *testing.T, notes chan note, method, id string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case n := <-notes:
			var p struct {
				ID string `json:"id"`
			}
			json.Unmarshal(n.params, &p)
			if n.method == method && p.ID == id {
				return
			}
		case <-deadline:
			t.Fatalf("no %s for %s", method, id)
		}
	}
}

// noNote fails if notification method arrives for id within d.
func noNote(t *testing.T, notes chan note, method, id string, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case n := <-notes:
			var p struct {
				ID string `json:"id"`
			}
			json.Unmarshal(n.params, &p)
			if n.method == method && p.ID == id {
				t.Fatalf("unexpected %s for %s: %s", method, id, n.params)
			}
		case <-deadline:
			return
		}
	}
}

// inputFor returns server name as a ServerInput with every secret nil (kept).
func inputFor(t *testing.T, h *Hub, name string) config.ServerInput {
	t.Helper()
	for _, s := range h.serversForUI() {
		if s.Name == name {
			return config.ServerInput{Name: s.Name, Host: s.Host, Port: s.Port, User: s.User, Auth: s.Auth, KeyPath: s.KeyPath, AIVisible: s.AIVisible}
		}
	}
	t.Fatalf("no server %q", name)
	return config.ServerInput{}
}

func uiServerNamed(t *testing.T, c *rpc.Client, name string) (uiServer, bool) {
	t.Helper()
	var list []uiServer
	if err := c.Call(context.Background(), "servers", nil, &list); err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.Name == name {
			return s, true
		}
	}
	return uiServer{}, false
}

func save(c *rpc.Client, original string, in config.ServerInput) error {
	return c.Call(context.Background(), "servers.save", map[string]any{"original": original, "server": in}, nil)
}

func TestServersSaveValidation(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	ok := config.ServerInput{Name: "n", Host: "h", Port: 22, User: "u", Auth: "agent"}
	for _, tc := range []struct {
		edit func(*config.ServerInput)
		want string
	}{
		{func(s *config.ServerInput) { s.Name = "bad name" }, "name:"},
		{func(s *config.ServerInput) { s.Host = "" }, "host:"},
		{func(s *config.ServerInput) { s.Port = 70000 }, "port:"},
		{func(s *config.ServerInput) { s.User = "a b" }, "user:"},
		{func(s *config.ServerInput) { s.Auth = "x" }, "auth:"},
		{func(s *config.ServerInput) { s.Auth = "key" }, "keyPath:"},
	} {
		in := ok
		tc.edit(&in)
		var re *rpc.Error
		if err := save(c, "", in); !errors.As(err, &re) || re.Code != -32602 || !strings.HasPrefix(re.Message, tc.want) {
			t.Errorf("%s: got %v", tc.want, err)
		}
	}
}

func TestServersWritesNeedUnlockedVault(t *testing.T) {
	locked, _, _ := newEncHub(t, &fakeExec{})
	noVault, _ := newHubAt(t, nil, nil)
	in := config.ServerInput{Name: "x", Host: "h", Port: 22, User: "u", Auth: "agent"}
	for h, want := range map[*Hub]error{locked: ErrLocked, noVault: errNoVault} {
		if err := h.SaveServer("", in); !errors.Is(err, want) {
			t.Errorf("save: want %v, got %v", want, err)
		}
		if err := h.DeleteServer("vis"); !errors.Is(err, want) {
			t.Errorf("delete: want %v, got %v", want, err)
		}
		if err := h.ForgetHostKey("vis"); !errors.Is(err, want) {
			t.Errorf("forget: want %v, got %v", want, err)
		}
	}
}

func TestServersSaveSecretSemanticsAndRename(t *testing.T) {
	h, _, _ := newEncHub(t, &fakeExec{})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	c, _ := startUI(t, h)
	p := func(s string) *string { return &s }
	secrets := func(name string) [4]string {
		t.Helper()
		dc, err := h.Resolve(name)
		if err != nil {
			t.Fatal(err)
		}
		return [4]string{dc.Password, dc.SuPassword, dc.SudoPassword, dc.Passphrase}
	}
	base := config.ServerInput{Name: "n1", Host: "h", Port: 22, User: "u", Auth: "password"}
	in := base
	in.Password, in.SuPassword, in.SudoPassword, in.KeyPassphrase = p("p1"), p("s1"), p("d1"), p("k1")
	if err := save(c, "", in); err != nil {
		t.Fatal(err)
	}
	if got := secrets("n1"); got != [4]string{"p1", "s1", "d1", "k1"} {
		t.Fatalf("create: %v", got)
	}
	if err := save(c, "n1", base); err != nil { // all nil: kept
		t.Fatal(err)
	}
	if got := secrets("n1"); got != [4]string{"p1", "s1", "d1", "k1"} {
		t.Fatalf("nil must keep: %v", got)
	}
	in.Password, in.SuPassword, in.SudoPassword, in.KeyPassphrase = p(""), p("s2"), p(""), p("k2")
	if err := save(c, "n1", in); err != nil {
		t.Fatal(err)
	}
	if got := secrets("n1"); got != [4]string{"", "s2", "", "k2"} {
		t.Fatalf("clear/set: %v", got)
	}
	s, _ := uiServerNamed(t, c, "n1")
	if s.HasPassword || !s.HasSuPassword || s.HasSudoPassword || !s.HasKeyPassphrase {
		t.Fatalf("has* flags: %+v", s)
	}
	renamed := base
	renamed.Name = "n2"
	if err := save(c, "n1", renamed); err != nil {
		t.Fatal(err)
	}
	if got := secrets("n2"); got != [4]string{"", "s2", "", "k2"} {
		t.Fatalf("rename must re-encrypt: %v", got)
	}
	if _, ok := uiServerNamed(t, c, "n1"); ok {
		t.Fatal("old name still listed")
	}
}

func TestServersSaveNewPortDropsPinAndUnsuppliedSecrets(t *testing.T) {
	h, _, _ := newEncHub(t, &fakeExec{})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	c, _ := startUI(t, h)
	in := inputFor(t, h, "enc")
	su := "su-new"
	in.Port, in.SuPassword = 2222, &su
	if err := save(c, "enc", in); err != nil {
		t.Fatal(err)
	}
	s, _ := uiServerNamed(t, c, "enc")
	if s.HostKey != "" || s.HostKeyAlgo != "" || s.HasPassword || !s.HasSuPassword {
		t.Fatalf("got %+v", s)
	}
}

func TestServersSaveClosesTabsOnlyOnDialChange(t *testing.T) {
	h := fakeServerHub(t, "pw")
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	c, _, notes := startTermDoor(t, h)
	if err := c.Call(context.Background(), "term.open", map[string]any{"id": "t1", "server": "fk", "rows": 24, "cols": 80}, nil); err != nil {
		t.Fatal(err)
	}
	in := inputFor(t, h, "fk")
	in.AIVisible = true
	if err := save(c, "fk", in); err != nil {
		t.Fatal(err)
	}
	noNote(t, notes, "term.exit", "t1", 300*time.Millisecond)
	in.User = "u2"
	if err := save(c, "fk", in); err != nil {
		t.Fatal(err)
	}
	waitNote(t, notes, "term.exit", "t1")
}

func TestServersForgetAndDeleteCloseTheConnection(t *testing.T) {
	for _, method := range []string{"servers.forgetHostKey", "servers.delete"} {
		h := fakeServerHub(t, "pw")
		if err := h.Unlock("pw"); err != nil {
			t.Fatal(err)
		}
		c, _, notes := startTermDoor(t, h)
		ctx := context.Background()
		if err := c.Call(ctx, "term.open", map[string]any{"id": "t1", "server": "fk", "rows": 24, "cols": 80}, nil); err != nil {
			t.Fatal(err)
		}
		if err := c.Call(ctx, method, map[string]string{"name": "fk"}, nil); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		waitNote(t, notes, "term.exit", "t1")
		s, listed := uiServerNamed(t, c, "fk")
		switch method {
		case "servers.forgetHostKey":
			if !listed || s.HostKey != "" {
				t.Fatalf("forget: %v %+v", listed, s)
			}
			if err := c.Call(ctx, method, map[string]string{"name": "fk"}, nil); err != nil {
				t.Fatalf("forgetting an unpinned server must succeed: %v", err)
			}
		case "servers.delete":
			if listed {
				t.Fatal("deleted server still listed")
			}
			if err := c.Call(ctx, method, map[string]string{"name": "fk"}, nil); err == nil || !strings.Contains(err.Error(), "not found") {
				t.Fatalf("second delete: %v", err)
			}
		}
	}
}

func TestServersSaveDeniesPendingRequests(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: -1, ApprovalExpiry: time.Minute})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); errc <- err }()
	waitPending(t, h.Broker(), 1)
	if err := h.SaveServer("vis", inputFor(t, h, "vis")); err != nil {
		t.Fatal(err)
	}
	var de *DeniedError
	if err := <-errc; !errors.As(err, &de) || de.Reason != "server changed" {
		t.Fatalf("got %v", err)
	}
}

func TestServersDeleteDeniesPendingRequests(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: -1, ApprovalExpiry: time.Minute})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); errc <- err }()
	waitPending(t, h.Broker(), 1)
	if err := h.DeleteServer("vis"); err != nil {
		t.Fatal(err)
	}
	var de *DeniedError
	if err := <-errc; !errors.As(err, &de) || de.Reason != "server changed" {
		t.Fatalf("got %v", err)
	}
}

func TestServersDeleteAndForgetRejectMalformedParams(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	for _, m := range []string{"servers.delete", "servers.forgetHostKey"} {
		var re *rpc.Error
		if err := c.Call(context.Background(), m, []int{1}, nil); !errors.As(err, &re) || re.Code != -32602 || re.Message != "invalid params" {
			t.Errorf("%s: got %v", m, err)
		}
	}
}

// A refused reload (here, a tampered file) shows in status as a fixed
// message, never file contents, and clears once the file is good again.
func TestStatusReportsStoreError(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	storeError := func() (string, bool) {
		var st map[string]any
		if err := c.Call(context.Background(), "status", nil, &st); err != nil {
			t.Fatal(err)
		}
		s, ok := st["storeError"].(string)
		return s, ok
	}
	if s, ok := storeError(); ok {
		t.Fatalf("storeError on a good store: %q", s)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Servers[0].Host = "evil-host"
	f.Revision++
	b, _ := json.Marshal(f)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if s, _ := storeError(); s != errStoreReload.Error() {
		t.Fatalf("storeError after tamper = %q", s)
	}
	f.Servers[0].Host = "h"
	if err := config.Save(path, f, testMK); err != nil {
		t.Fatal(err)
	}
	if s, ok := storeError(); ok {
		t.Fatalf("storeError after fix: %q", s)
	}
}

// A vault file deleted under a running hub is a store error, not "no vault":
// the last good copy is still served, writes fail, and a restored file
// clears the error.
func TestStatusReportsDeletedVault(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	status := func() map[string]any {
		var st map[string]any
		if err := c.Call(context.Background(), "status", nil, &st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if st := status(); st["storeError"] != errStoreReload.Error() || st["hasVault"] != true {
		t.Fatalf("status after delete: %v", st)
	}
	if got := h.ServersForMCP(); len(got) != 2 {
		t.Fatalf("in-memory servers after delete: %+v", got)
	}
	if err := h.SaveServer("", config.ServerInput{Name: "x", Host: "h", Port: 22, User: "u", Auth: "agent"}); err == nil {
		t.Fatal("write succeeded with the vault file missing")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a failed write left a file: %v", err)
	}
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if st := status(); st["storeError"] != nil {
		t.Fatalf("storeError after restore: %v", st["storeError"])
	}
}

// A write that succeeds on disk but whose reload fails reports the reload
// error to the caller, as vault.create does.
func TestWritesReturnReloadError(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	loadStore = func(string) (*config.File, error) { return nil, errors.New("boom") }
	t.Cleanup(func() { loadStore = config.Load })
	in := config.ServerInput{Name: "x", Host: "h", Port: 22, User: "u", Auth: "agent"}
	for what, err := range map[string]error{
		"save": h.SaveServer("", in), "forget": h.ForgetHostKey("vis"), "delete": h.DeleteServer("hid"),
	} {
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("%s: want the reload error, got %v", what, err)
		}
	}
}

// A vault the hub holds is not replaced by vault.create just because its
// file went missing.
func TestVaultCreateRefusedWhileVaultFileMissing(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := h.CreateVault("new-password"); err == nil || err.Error() != "a vault already exists" {
		t.Fatalf("vault.create with the file missing: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("vault.create wrote a file: %v", err)
	}
}
