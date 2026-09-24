package hub

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

// newEncryptedHub writes a vault with master password "pw" and one
// AI-visible encrypted password server "vis" with a pinned host key. The hub
// starts locked and is closed on cleanup.
func newEncryptedHub(t *testing.T, o Options) (*Hub, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	k, mk, err := config.NewKDF("pw")
	if err != nil {
		t.Fatal(err)
	}
	f := &config.File{Version: 1, KDF: &k}
	s := config.Server{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "password", HostKey: "SHA256:abc", AIVisible: true}
	s.EncPassword, err = config.Encrypt(mk, "vis/encPassword", config.AADFor(f, s, "encPassword"), "s3cr3t-pw")
	if err != nil {
		t.Fatal(err)
	}
	f.Servers = []config.Server{s}
	if err := config.Save(path, f, mk); err != nil {
		t.Fatal(err)
	}
	audit, err := broker.OpenAudit(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { audit.Close() })
	o.StorePath, o.Audit = path, audit
	fe := &fakeExec{}
	o.Dialer = func(sshx.DialConfig) Executor { return fe }
	h, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h, path
}

func TestIdleLockLocksAfterQuietPeriod(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: 200 * time.Millisecond})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !h.Locked() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !h.Locked() {
		t.Fatal("vault did not auto-lock")
	}
}

func TestIdleLockHeldOffByActivity(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: 300 * time.Millisecond})
	h.Unlock("pw")
	for i := 0; i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
		h.touch()
	}
	if h.Locked() {
		t.Fatal("locked despite activity")
	}
}

func TestIdleLockHeldOffByPendingRequest(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: 200 * time.Millisecond, ApprovalExpiry: time.Minute})
	h.Unlock("pw")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Exec(ctx, ExecRequest{Server: "vis", Command: "ls"})
	waitPending(t, h.Broker(), 1)
	time.Sleep(600 * time.Millisecond)
	if h.Locked() {
		t.Fatal("locked while a request was pending")
	}
}

func TestIdleLockDisabledWhenNegative(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: -1})
	h.Unlock("pw")
	time.Sleep(300 * time.Millisecond)
	if h.Locked() {
		t.Fatal("negative IdleLock must disable auto-lock")
	}
}
