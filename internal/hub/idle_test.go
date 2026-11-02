package hub

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshx"
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
	if o.Dialer == nil {
		fe := &fakeExec{}
		o.Dialer = func(sshx.DialConfig) Executor { return fe }
	}
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
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
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
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
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
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if h.Locked() {
		t.Fatal("negative IdleLock must disable auto-lock")
	}
}

// waitLocked waits up to 3 s for the vault to lock.
func waitLocked(h *Hub) bool {
	deadline := time.Now().Add(3 * time.Second)
	for !h.Locked() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	return h.Locked()
}

// AI requests are not UI input: failing ones must not reset the idle clock.
func TestIdleLockNotHeldOffByFailingAIRequests(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: 300 * time.Millisecond})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	// Keep calling for the whole wait, so only AI traffic separates the
	// vault from the idle deadline.
	deadline := time.Now().Add(3 * time.Second)
	for !h.Locked() && time.Now().Before(deadline) {
		if _, err := h.Exec(context.Background(), ExecRequest{Server: "nope", Command: "ls"}); err == nil {
			t.Fatal("exec on a missing server succeeded")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !h.Locked() {
		t.Fatal("failing AI requests held off the idle lock")
	}
}

// blockingExec blocks every command until release is closed.
type blockingExec struct{ started, release chan struct{} }

func (b *blockingExec) Exec(ctx context.Context, cmd string) (sshx.ExecResult, error) {
	close(b.started)
	<-b.release
	return sshx.ExecResult{}, nil
}
func (b *blockingExec) ExecStdin(ctx context.Context, cmd, _ string) (sshx.ExecResult, error) {
	return b.Exec(ctx, cmd)
}
func (b *blockingExec) ExecSudo(ctx context.Context, cmd string) (sshx.ExecResult, error) {
	return b.Exec(ctx, cmd)
}

func TestIdleLockHeldOffByRunningCommand(t *testing.T) {
	be := &blockingExec{started: make(chan struct{}), release: make(chan struct{})}
	h, _ := newEncryptedHub(t, Options{IdleLock: 200 * time.Millisecond, ApprovalExpiry: time.Minute,
		Dialer: func(sshx.DialConfig) Executor { return be }})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); errc <- err }()
	waitPending(t, h.Broker(), 1)
	allowFirst(t, h.Broker())
	select {
	case <-be.started:
	case <-time.After(3 * time.Second):
		t.Fatal("command did not start")
	}
	time.Sleep(600 * time.Millisecond)
	if h.Locked() {
		t.Fatal("locked while a command was running")
	}
	close(be.release)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if !waitLocked(h) {
		t.Fatal("did not lock after the command finished")
	}
}

// R17: xterm answers terminal queries by itself through term.write. Only
// writes the app marks as user input hold off the idle lock.
func TestIdleLockTermWriteNeedsUserFlag(t *testing.T) {
	for _, user := range []bool{false, true} {
		h, _ := newEncryptedHub(t, Options{IdleLock: 300 * time.Millisecond})
		_, w, _ := startTermDoor(t, h)
		if err := h.Unlock("pw"); err != nil {
			t.Fatal(err)
		}
		p := map[string]any{"id": "t1", "data": "Gw=="}
		if user {
			p["user"] = true
		}
		for range 10 {
			time.Sleep(100 * time.Millisecond)
			sendNote(t, w, "term.write", p)
		}
		if h.Locked() == user {
			t.Fatalf("user=%v: locked=%v after 1 s of term.write", user, h.Locked())
		}
	}
}

// The idle clock and grant deadlines are wall clock, like the soft-lock
// limit: a monotonic reading stops while the machine sleeps, so time asleep
// would not count toward the idle lock, and a timed grant would outlive the
// deadline the UI shows (`until`, wall clock) by the time slept. Go cannot
// build a Time whose wall and monotonic readings disagree, so this checks
// what is stored: a value with no monotonic reading compares by wall clock.
func TestDeadlinesUseWallClock(t *testing.T) {
	wall := func(what string, h *Hub, get func() time.Time) {
		t.Helper()
		h.mu.Lock()
		v := get()
		h.mu.Unlock()
		if v.IsZero() || v != v.Round(0) {
			t.Fatalf("%s = %v: not a wall-clock time", what, v)
		}
	}
	h, _ := newEncryptedHub(t, Options{})
	last := func() time.Time { return h.lastActivity }
	wall("lastActivity at start", h, last)
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	wall("lastActivity after unlock", h, last)
	h.touch()
	wall("lastActivity after UI input", h, last)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	wall("grant deadline", h, func() time.Time { return h.grants["vis"].until })

	c, _ := newHubAt(t, nil, nil)
	if err := c.CreateVault("longenough"); err != nil {
		t.Fatal(err)
	}
	wall("lastActivity after vault.create", c, func() time.Time { return c.lastActivity })
}
