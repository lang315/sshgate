package hub

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
)

// newHubAt writes f (unless nil) and starts a hub on it with an audit log
// next to the store and no idle lock.
func newHubAt(t *testing.T, f *config.File, mk []byte) (*Hub, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	if f != nil {
		if err := config.Save(path, f, mk); err != nil {
			t.Fatal(err)
		}
	}
	audit, err := broker.OpenAudit(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { audit.Close() })
	h, err := New(Options{StorePath: path, Audit: audit, IdleLock: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	t.Cleanup(h.Registry().CloseAll)
	return h, path
}

func TestVaultCreateOnEmptyStore(t *testing.T) {
	h, path := newHubAt(t, nil, nil)
	c, _ := startUI(t, h)
	ctx := context.Background()
	var st struct {
		Locked    bool   `json:"locked"`
		HasVault  bool   `json:"hasVault"`
		StorePath string `json:"storePath"`
	}
	if err := c.Call(ctx, "status", nil, &st); err != nil || st.HasVault || st.StorePath != path {
		t.Fatalf("status before: %v %+v", err, st)
	}
	var re *rpc.Error
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "short"}, nil); !errors.As(err, &re) || re.Code != -32602 {
		t.Fatalf("short password: want -32602, got %v", err)
	}
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	// Unlocked by the create itself: no unlock call, no second Argon2 run.
	if err := c.Call(ctx, "status", nil, &st); err != nil || !st.HasVault || st.Locked {
		t.Fatalf("status after: %v %+v", err, st)
	}
	f, err := config.Load(path)
	if err != nil || f.KDF == nil {
		t.Fatalf("no vault on disk: %v %+v", err, f)
	}
	h.mu.Lock()
	mk := bytes.Clone(h.deps.MasterKey)
	h.mu.Unlock()
	if !f.KDF.Verify(mk) || f.VerifyMAC(mk) != nil {
		t.Fatal("the hub's key does not open the new vault")
	}
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second create: %v", err)
	}
}

// A KDF-less file was never MAC'd: its servers are kept, their AI flags and
// pins are not.
func TestVaultCreateKeepsServersButNotAIVisibleOrPins(t *testing.T) {
	h, path := newHubAt(t, &config.File{Version: 1, Servers: []config.Server{
		{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true, HostKey: "SHA256:abc", HostKeyAlgo: "ssh-ed25519"}}}, nil)
	if err := h.CreateVault("longenough"); err != nil {
		t.Fatal(err)
	}
	if got := h.ServersForMCP(); len(got) != 0 {
		t.Fatalf("AI still sees %v", got)
	}
	f, _ := config.Load(path)
	if len(f.Servers) != 1 || f.Servers[0].Name != "a" || f.Servers[0].AIVisible || f.Servers[0].HostKey != "" || f.Servers[0].HostKeyAlgo != "" {
		t.Fatalf("kept servers: %+v", f.Servers)
	}
}

func TestVaultCreateRefusesEncryptedFieldsWithoutKDF(t *testing.T) {
	h, path := newHubAt(t, &config.File{Version: 1, Servers: []config.Server{
		{Name: "a", Host: "h", Port: 22, User: "u", Auth: "password", EncPassword: "bogus"}}}, nil)
	before, _ := os.ReadFile(path)
	if err := h.CreateVault("longenough"); err == nil || !strings.Contains(err.Error(), "encrypted fields") {
		t.Fatalf("got %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) || h.deps.MasterKey != nil {
		t.Fatal("a refused create changed the file or the hub")
	}
}

// The minimum is CreateVault's own rule, not only the UI door's.
func TestVaultCreateRefusesShortPassword(t *testing.T) {
	h, path := newHubAt(t, nil, nil)
	if err := h.CreateVault("1234567"); err == nil || !strings.Contains(err.Error(), "at least 8") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a refused create wrote the store: %v", err)
	}
}

// If the reload after the write fails, the key is not installed: the hub
// stays locked and the new password unlocks the vault once the store reads.
func TestVaultCreateReloadFailureLeavesHubLocked(t *testing.T) {
	h, path := newHubAt(t, &config.File{Version: 1, Servers: []config.Server{}}, nil)
	loadStore = func(string) (*config.File, error) { return nil, errors.New("boom") }
	t.Cleanup(func() { loadStore = config.Load })
	if err := h.CreateVault("longenough"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
	h.mu.Lock()
	mk := h.deps.MasterKey
	h.mu.Unlock()
	if mk != nil {
		t.Fatal("key installed despite the failed reload")
	}
	if f, err := config.Load(path); err != nil || f.KDF == nil {
		t.Fatalf("vault not on disk: %v", err)
	}
	loadStore = config.Load
	if err := h.Reload(); err != nil || !h.Locked() {
		t.Fatalf("after reload: %v locked=%v", err, h.Locked())
	}
	if err := h.Unlock("longenough"); err != nil || h.Locked() {
		t.Fatalf("unlock with the new password: %v", err)
	}
}

// vault.create derives the key exactly once (in NewKDF) and installs it
// directly: it never runs Unlock's derivation, a second Argon2 pass.
func TestVaultCreateDerivesKeyOnce(t *testing.T) {
	h, _ := newHubAt(t, nil, nil)
	var kdfRuns, derives int
	newKDF = func(pw string) (config.KDF, []byte, error) { kdfRuns++; return config.NewKDF(pw) }
	deriveKey = func(k *config.KDF, pw string) ([]byte, error) { derives++; return k.DeriveKey(pw) }
	t.Cleanup(func() { newKDF, deriveKey = config.NewKDF, (*config.KDF).DeriveKey })
	if err := h.CreateVault("longenough"); err != nil {
		t.Fatal(err)
	}
	if kdfRuns != 1 || derives != 0 || h.Locked() {
		t.Fatalf("NewKDF runs = %d, Unlock derivations = %d, locked = %v", kdfRuns, derives, h.Locked())
	}
	// The seam is live: Unlock does go through it.
	h.Lock()
	if err := h.Unlock("longenough"); err != nil || derives != 1 {
		t.Fatalf("unlock: %v, derivations = %d", err, derives)
	}
}

func TestDialChangedIgnoresTunnelsAndAIVisible(t *testing.T) {
	a := config.Server{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent"}
	b := a
	b.AIVisible = true
	b.Tunnels = []config.Tunnel{{ID: "x", Kind: "dynamic", ListenPort: 1080}}
	if dialChanged(a, b) {
		t.Fatal("tunnels or aiVisible counted as a dial change")
	}
	b.Port = 23
	if !dialChanged(a, b) {
		t.Fatal("port change missed")
	}
}
