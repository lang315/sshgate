package hub

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

type fakeExec struct {
	calls []string
	res   sshx.ExecResult
	err   error
}

func (f *fakeExec) Exec(ctx context.Context, cmd string) (sshx.ExecResult, error) {
	f.calls = append(f.calls, cmd)
	return f.res, f.err
}
func (f *fakeExec) ExecSudo(ctx context.Context, cmd string) (sshx.ExecResult, error) {
	f.calls = append(f.calls, "sudo:"+cmd)
	return f.res, f.err
}

// newHub writes a vault with key-auth servers: "vis" (AIVisible, pinned
// host key), "nokey" (AIVisible, no pin) and "hid" (hidden). Key-auth
// servers have no encrypted fields, so the vault has no KDF and is never
// "locked".
func newHub(t *testing.T, fe *fakeExec) (*Hub, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	f := &config.File{Version: 1, Servers: []config.Server{
		{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true},
		{Name: "nokey", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true},
		{Name: "hid", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc"},
	}}
	if err := config.Save(path, f, nil); err != nil {
		t.Fatal(err)
	}
	audit, err := broker.OpenAudit(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { audit.Close() })
	h, err := New(Options{StorePath: path, Audit: audit, ApprovalExpiry: 200 * time.Millisecond,
		Dialer: func(sshx.DialConfig) Executor { return fe }})
	if err != nil {
		t.Fatal(err)
	}
	return h, path
}

func decideFirst(b *broker.Broker, d broker.Decision) {
	for len(b.Pending()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	b.Decide(b.Pending()[0].ID, d)
}

func allowFirst(b *broker.Broker) { decideFirst(b, broker.Decision{Outcome: broker.Allowed}) }

func TestServersForMCPOnlyVisible(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	names := []string{}
	for _, s := range h.ServersForMCP() {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "vis,nokey" {
		t.Fatalf("got %v", names)
	}
}

func TestHiddenAndMissingAreByteIdentical(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	_, e1 := h.Exec(context.Background(), ExecRequest{Server: "hid", Command: "ls"})
	_, e2 := h.Exec(context.Background(), ExecRequest{Server: "nope", Command: "ls"})
	if e1 == nil || e2 == nil {
		t.Fatal("both must error")
	}
	want1, want2 := `server "hid" not found`, `server "nope" not found`
	if e1.Error() != want1 || e2.Error() != want2 {
		t.Fatalf("got %q / %q", e1, e2)
	}
}

func TestNoHostKeyRefusedWithoutDialing(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHub(t, fe)
	_, err := h.Exec(context.Background(), ExecRequest{Server: "nokey", Command: "ls"})
	if !errors.Is(err, ErrNoHostKey) {
		t.Fatalf("want ErrNoHostKey, got %v", err)
	}
	if len(fe.calls) != 0 {
		t.Fatal("must not dial")
	}
}

func TestAllowedRunsAndReturnsStreams(t *testing.T) {
	fe := &fakeExec{res: sshx.ExecResult{Stdout: "out\n", Stderr: "err\n", ExitCode: 2}}
	h, _ := newHub(t, fe)
	go allowFirst(h.Broker())
	res, err := h.Exec(context.Background(), ExecRequest{Client: "t", Server: "vis", Command: "ls", Description: "list"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 2 || res.Stdout != "out\n" || res.Stderr != "err\n" {
		t.Fatalf("got %+v", res)
	}
	if len(fe.calls) != 1 || fe.calls[0] != "ls # list" {
		t.Fatalf("calls = %v", fe.calls)
	}
}

func TestDeniedAndExpiredAreDistinct(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHub(t, fe)
	go decideFirst(h.Broker(), broker.Decision{Outcome: broker.Denied, Reason: "nope"})
	_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	var de *DeniedError
	if !errors.As(err, &de) || de.Reason != "nope" || err.Error() != "Denied by user: nope" {
		t.Fatalf("got %v", err)
	}
	_, err = h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}) // nobody decides → expiry 200ms
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
	if len(fe.calls) != 0 {
		t.Fatal("nothing should have executed")
	}
}

func TestApprovedAfterCancelDoesNotExecute(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHub(t, fe)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := h.Exec(ctx, ExecRequest{Server: "vis", Command: "ls"}); errc <- err }()
	b := h.Broker()
	for len(b.Pending()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	id := b.Pending()[0].ID
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if err := b.Decide(id, broker.Decision{Outcome: broker.Allowed}); !errors.Is(err, broker.ErrNotFound) {
		t.Fatalf("decide after cancel: %v", err)
	}
	if len(fe.calls) != 0 {
		t.Fatal("executed after cancel")
	}
}

func TestTimeoutSecClamped(t *testing.T) {
	if clampTimeout(0) != 60 || clampTimeout(-5) != 1 || clampTimeout(9999) != 600 || clampTimeout(120) != 120 {
		t.Fatal("clamp wrong")
	}
}

func TestReloadPicksUpRevisionChange(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Servers[2].AIVisible = true // "hid" becomes visible
	if err := config.Save(path, f, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(h.ServersForMCP()) != 3 {
		t.Fatalf("reload missed change: %+v", h.ServersForMCP())
	}
}

func TestEventsReachSink(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	kinds := make(chan string, 2)
	h.setEventSink(func(e broker.Event) { kinds <- e.Kind })
	go allowFirst(h.Broker())
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); err != nil {
		t.Fatal(err)
	}
	// "decided" fires on the deciding goroutine and may land after Exec returns.
	if k1, k2 := <-kinds, <-kinds; k1 != "pending" || k2 != "decided" {
		t.Fatalf("events = %s,%s", k1, k2)
	}
}

func TestLockedVaultRefusesEncryptedServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	k, mk, err := config.NewKDF("pw")
	if err != nil {
		t.Fatal(err)
	}
	f := &config.File{Version: 1, KDF: &k}
	s := config.Server{Name: "enc", Host: "h", Port: 22, User: "u", Auth: "password", HostKey: "SHA256:abc", AIVisible: true}
	s.EncPassword, err = config.Encrypt(mk, "enc/encPassword", config.AADFor(f, s, "encPassword"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	f.Servers = []config.Server{s}
	if err := config.Save(path, f, mk); err != nil {
		t.Fatal(err)
	}
	fe := &fakeExec{res: sshx.ExecResult{Stdout: "secret"}}
	h, err := New(Options{StorePath: path, ApprovalExpiry: 200 * time.Millisecond, Dialer: func(sshx.DialConfig) Executor { return fe }})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Locked() || !h.ServersForMCP()[0].Locked {
		t.Fatal("want locked")
	}
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "enc", Command: "ls"}); err == nil || err.Error() != "Vault is locked; unlock it in the app" {
		t.Fatalf("got %v", err)
	}
	if err := h.Unlock("wrong"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	go allowFirst(h.Broker())
	res, err := h.Exec(context.Background(), ExecRequest{Server: "enc", Command: "ls"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Stdout, "secret") {
		t.Fatalf("secret not redacted: %q", res.Stdout)
	}
	h.Lock()
	if !h.Locked() {
		t.Fatal("Lock did not lock")
	}
}
