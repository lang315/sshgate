package hub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	return newHubExpiry(t, fe, 200*time.Millisecond)
}

func newHubExpiry(t *testing.T, fe *fakeExec, expiry time.Duration) (*Hub, string) {
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
	h, err := New(Options{StorePath: path, Audit: audit, ApprovalExpiry: expiry,
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

// TestSetEventSinkReleaseIsPerInstall covers R27: release only clears the
// sink it installed, so an out-of-order release (an earlier session's,
// firing after a later one replaced it) can't clobber the current sink.
func TestSetEventSinkReleaseIsPerInstall(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	var got []string
	releaseA := h.setEventSink(func(e broker.Event) { got = append(got, "A:"+e.Kind) })
	releaseB := h.setEventSink(func(e broker.Event) { got = append(got, "B:"+e.Kind) })

	releaseA()
	h.emit(broker.Event{Kind: "pending"})
	if len(got) != 1 || got[0] != "B:pending" {
		t.Fatalf("want B still installed after releasing A, got %v", got)
	}

	releaseB()
	h.emit(broker.Event{Kind: "pending"})
	if len(got) != 1 {
		t.Fatalf("want sink cleared after releasing B, got %v", got)
	}
}

// newEncHub writes an encrypted vault: "enc" (AIVisible, pinned, password
// "s3cr3t-pw") and "agent" (no encrypted fields, no pin). The hub starts
// locked; the master password is "pw" and mk is returned.
func newEncHub(t *testing.T, fe *fakeExec) (h *Hub, path string, mk []byte) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "servers.json")
	k, mk, err := config.NewKDF("pw")
	if err != nil {
		t.Fatal(err)
	}
	f := &config.File{Version: 1, KDF: &k}
	s := config.Server{Name: "enc", Host: "h", Port: 22, User: "u", Auth: "password", HostKey: "SHA256:abc", AIVisible: true}
	s.EncPassword, err = config.Encrypt(mk, "enc/encPassword", config.AADFor(f, s, "encPassword"), "s3cr3t-pw")
	if err != nil {
		t.Fatal(err)
	}
	f.Servers = []config.Server{s, {Name: "agent", Host: "h", Port: 22, User: "u", Auth: "agent"}}
	if err := config.Save(path, f, mk); err != nil {
		t.Fatal(err)
	}
	audit, err := broker.OpenAudit(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { audit.Close() })
	h, err = New(Options{StorePath: path, Audit: audit, ApprovalExpiry: 200 * time.Millisecond,
		Dialer: func(sshx.DialConfig) Executor { return fe }})
	if err != nil {
		t.Fatal(err)
	}
	return h, path, mk
}

// readAudit returns the raw audit file and its parsed records.
func readAudit(t *testing.T, storePath string) (string, []map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(storePath), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var recs []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, m)
	}
	return string(raw), recs
}

func TestLockedVaultRefusesEncryptedServer(t *testing.T) {
	fe := &fakeExec{res: sshx.ExecResult{Stdout: "s3cr3t-pw"}}
	h, _, _ := newEncHub(t, fe)
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
	if strings.Contains(res.Stdout, "s3cr3t-pw") {
		t.Fatalf("secret not redacted: %q", res.Stdout)
	}
	h.Lock()
	if !h.Locked() {
		t.Fatal("Lock did not lock")
	}
}

func TestAuditRedactsSecretsBeforeApproval(t *testing.T) {
	h, path, _ := newEncHub(t, &fakeExec{})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	go decideFirst(h.Broker(), broker.Decision{Outcome: broker.Denied})
	_, err := h.Exec(context.Background(), ExecRequest{Server: "enc", Command: "echo s3cr3t-pw", Description: "prints s3cr3t-pw"})
	var de *DeniedError
	if !errors.As(err, &de) {
		t.Fatalf("want denied, got %v", err)
	}
	raw, recs := readAudit(t, path)
	if strings.Contains(raw, "s3cr3t-pw") || !strings.Contains(raw, "***") {
		t.Fatalf("audit not redacted: %s", raw)
	}
	if len(recs) != 1 || recs[0]["outcome"] != "denied" {
		t.Fatalf("recs = %v", recs)
	}
}

func TestAuditOutcomes(t *testing.T) {
	cancelled := fmt.Errorf("%w: %w", sshx.ErrCancelled, context.Canceled)
	out := sshx.ExecResult{Stdout: "OUTPUT-MARKER\n", Stderr: "ERR-MARKER", ExitCode: 3}
	cases := []struct {
		name     string
		fe       *fakeExec
		decision *broker.Decision // nil: nobody decides, request expires
		sudo     bool
		check    func(t *testing.T, rec map[string]any, res ExecResponse, err error)
	}{
		{"allowed", &fakeExec{res: out}, &broker.Decision{Outcome: broker.Allowed}, true,
			func(t *testing.T, rec map[string]any, res ExecResponse, err error) {
				if err != nil || res.ExitCode != 3 {
					t.Fatalf("res=%+v err=%v", res, err)
				}
				if rec["outcome"] != "allowed" || rec["exitCode"] != 3.0 || rec["stdoutBytes"] != 14.0 || rec["stderrBytes"] != 10.0 || rec["sudo"] != true {
					t.Fatalf("rec = %v", rec)
				}
			}},
		{"denied", &fakeExec{res: out}, &broker.Decision{Outcome: broker.Denied, Reason: "nope"}, false,
			func(t *testing.T, rec map[string]any, res ExecResponse, err error) {
				if rec["outcome"] != "denied" || rec["reason"] != "nope" {
					t.Fatalf("rec = %v", rec)
				}
			}},
		{"expired", &fakeExec{res: out}, nil, false,
			func(t *testing.T, rec map[string]any, res ExecResponse, err error) {
				if !errors.Is(err, ErrExpired) || rec["outcome"] != "expired" {
					t.Fatalf("err=%v rec=%v", err, rec)
				}
			}},
		{"sent_to_tab", &fakeExec{res: out}, &broker.Decision{Outcome: broker.SentToTab}, false,
			func(t *testing.T, rec map[string]any, res ExecResponse, err error) {
				if err != nil || res.ExitCode != 0 || res.Stdout != "User chose to run this in their terminal; no output captured" {
					t.Fatalf("res=%+v err=%v", res, err)
				}
				if rec["outcome"] != "sent_to_tab" {
					t.Fatalf("rec = %v", rec)
				}
			}},
		{"cancelled_running", &fakeExec{res: out, err: cancelled}, &broker.Decision{Outcome: broker.Allowed}, false,
			func(t *testing.T, rec map[string]any, res ExecResponse, err error) {
				if err == nil || err.Error() != "Cancelled; the remote process may still be running" {
					t.Fatalf("err = %v", err)
				}
				if rec["outcome"] != "cancelled_running" || rec["reason"] != cancelled.Error() {
					t.Fatalf("rec = %v", rec)
				}
			}},
		{"error", &fakeExec{res: out, err: errors.New("boom")}, &broker.Decision{Outcome: broker.Allowed}, false,
			func(t *testing.T, rec map[string]any, res ExecResponse, err error) {
				if err == nil || err.Error() != "boom" || rec["outcome"] != "error" || rec["reason"] != "boom" {
					t.Fatalf("err=%v rec=%v", err, rec)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, path := newHub(t, c.fe)
			if c.decision != nil {
				go decideFirst(h.Broker(), *c.decision)
			}
			res, err := h.Exec(context.Background(), ExecRequest{Client: "t", Server: "vis", Command: "ls", Sudo: c.sudo})
			raw, recs := readAudit(t, path)
			if len(recs) != 1 {
				t.Fatalf("want 1 record, got %d: %s", len(recs), raw)
			}
			if strings.Contains(raw, "OUTPUT-MARKER") || strings.Contains(raw, "ERR-MARKER") {
				t.Fatalf("output content in audit: %s", raw)
			}
			if c.sudo && (len(c.fe.calls) != 1 || !strings.HasPrefix(c.fe.calls[0], "sudo:")) {
				t.Fatalf("calls = %v", c.fe.calls)
			}
			c.check(t, recs[0], res, err)
		})
	}
}

func TestSixthPendingRefused(t *testing.T) {
	h, _ := newHubExpiry(t, &fakeExec{}, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 5)
	for range 5 {
		go func() { h.Exec(ctx, ExecRequest{Server: "vis", Command: "ls"}); done <- struct{}{} }()
	}
	for len(h.Broker().Pending()) < 5 {
		time.Sleep(2 * time.Millisecond)
	}
	_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	if err == nil || err.Error() != "too many pending requests, try again later" {
		t.Fatalf("got %v", err)
	}
	cancel()
	for range 5 {
		<-done
	}
}

func TestOutputCappedPerStream(t *testing.T) {
	fe := &fakeExec{res: sshx.ExecResult{Stdout: "small", Stderr: strings.Repeat("e", 200*1024)}}
	h, _ := newHub(t, fe)
	go allowFirst(h.Broker())
	res, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "small" || !strings.Contains(res.Stderr, "[truncated") || len(res.Stderr) > config.DefaultOutputCap+100 {
		t.Fatalf("stdout=%q stderr len=%d", res.Stdout, len(res.Stderr))
	}
}

func TestReloadRejectsTamperedStoreWhenUnlocked(t *testing.T) {
	h, path, _ := newEncHub(t, &fakeExec{})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Servers[0].Host = "evil" // no key: MAC left stale
	f.Revision++
	b, _ := json.Marshal(f)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err == nil {
		t.Fatal("tampered store accepted")
	}
	if s, _ := h.Deps().File.FindServer("enc"); s.Host != "h" {
		t.Fatalf("tampered file took effect: %+v", s)
	}
}

func TestResolveLearnHostKeyNeedsKeyForEncryptedVault(t *testing.T) {
	h, path, mk := newEncHub(t, &fakeExec{})
	dc, err := h.Resolve("agent") // locked, but "agent" has no encrypted fields
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	dc.OnLearnHostKey("SHA256:new")
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("locked encrypted vault was modified")
	}

	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	dc, err = h.Resolve("agent")
	if err != nil {
		t.Fatal(err)
	}
	h.Lock() // the closure owns its own key copy
	dc.OnLearnHostKey("SHA256:new")
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := f.FindServer("agent"); s.HostKey != "SHA256:new" {
		t.Fatalf("host key not recorded: %+v", s)
	}
	if f.MAC == "" || f.VerifyMAC(mk) != nil {
		t.Fatal("recording must keep a valid MAC")
	}
}
