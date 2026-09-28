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
	"sync"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshx"
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

// testKDF and testMK are one master password ("pw") derived once for the
// package: Argon2 per test would be slow under -race.
var (
	testKDFOnce sync.Once
	testKDF     config.KDF
	testMK      []byte
)

// testVault returns a copy of the shared KDF and its key.
func testVault(t *testing.T) (*config.KDF, []byte) {
	t.Helper()
	testKDFOnce.Do(func() {
		var err error
		if testKDF, testMK, err = config.NewKDF("pw"); err != nil {
			panic(err)
		}
	})
	k := testKDF
	return &k, testMK
}

// unlockForTest installs testMK without a second Argon2 run.
func unlockForTest(h *Hub) {
	h.mu.Lock()
	h.deps.MasterKey = bytes.Clone(testMK)
	h.mu.Unlock()
}

// newHub writes an unlocked vault (master password "pw", key testMK) with
// agent-auth servers: "vis" (AIVisible, pinned host key), "nokey"
// (AIVisible, no pin) and "hid" (hidden).
func newHub(t *testing.T, fe Executor) (*Hub, string) {
	t.Helper()
	return newHubExpiry(t, fe, 200*time.Millisecond)
}

func newHubExpiry(t *testing.T, fe Executor, expiry time.Duration) (*Hub, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	f := &config.File{Version: 1, Servers: []config.Server{
		{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true},
		{Name: "nokey", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true},
		{Name: "hid", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc"},
	}}
	var mk []byte
	f.KDF, mk = testVault(t)
	if err := config.Save(path, f, mk); err != nil {
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
	unlockForTest(h)
	return h, path
}

const testWait = 10 * time.Second

// waitFor polls cond until it holds. After testWait it reports a failure
// with t.Errorf and returns false, so it is safe on any goroutine.
func waitFor(t *testing.T, what string, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Errorf("timed out after %v waiting for %s", testWait, what)
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return true
}

// waitPending waits for at least n pending requests; test goroutine only.
func waitPending(t *testing.T, b *broker.Broker, n int) {
	t.Helper()
	if !waitFor(t, fmt.Sprintf("%d pending", n), func() bool { return len(b.Pending()) >= n }) {
		t.FailNow()
	}
}

// decideFirst runs on its own goroutine, so it only reports via waitFor.
func decideFirst(t *testing.T, b *broker.Broker, d broker.Decision) {
	if waitFor(t, "a pending request", func() bool { return len(b.Pending()) > 0 }) {
		b.Decide(b.Pending()[0].ID, d)
	}
}

func allowFirst(t *testing.T, b *broker.Broker) {
	decideFirst(t, b, broker.Decision{Outcome: broker.Allowed})
}

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
	go allowFirst(t, h.Broker())
	res, err := h.Exec(context.Background(), ExecRequest{Client: "t", Server: "vis", Command: "ls", Description: "list"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 2 || res.Stdout != "out\n" || res.Stderr != "err\n" {
		t.Fatalf("got %+v", res)
	}
	if len(fe.calls) != 1 || fe.calls[0] != "ls" {
		t.Fatalf("calls = %v", fe.calls)
	}
}

// I1 / R39: the description is metadata. Appended as a shell comment, a
// trailing backslash or an open quote would turn it into code.
func TestDescriptionIsNeverExecuted(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHub(t, fe)
	go func() {
		if !waitFor(t, "a pending request", func() bool { return len(h.Broker().Pending()) > 0 }) {
			return
		}
		r := h.Broker().Pending()[0]
		if r.Command != "ls" || r.Description != `x \` {
			t.Errorf("approver saw %q / %q", r.Command, r.Description)
		}
		h.Broker().Decide(r.ID, broker.Decision{Outcome: broker.Allowed})
	}()
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls", Description: `x \`}); err != nil {
		t.Fatal(err)
	}
	if len(fe.calls) != 1 || fe.calls[0] != "ls" {
		t.Fatalf("calls = %q", fe.calls)
	}
}

func TestDescriptionStillValidated(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHub(t, fe)
	for _, d := range []string{"bad\x1bdesc", strings.Repeat("x", 501)} {
		if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls", Description: d}); err == nil || !strings.Contains(err.Error(), "description") {
			t.Fatalf("%q: got %v", d, err)
		}
	}
	if len(h.Broker().Pending()) != 0 || len(fe.calls) != 0 {
		t.Fatal("invalid description reached the broker")
	}
}

// Exec records how long the human took to decide, for both outcomes; a
// plain (non-auto) decision leaves Approval empty.
func TestExecRecordsWaitMs(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	go func() {
		waitPending(t, h.Broker(), 1)
		time.Sleep(20 * time.Millisecond)
		allowFirst(t, h.Broker())
	}()
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); err != nil {
		t.Fatal(err)
	}
	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if _, ok := last["approval"]; ok {
		t.Fatalf("approval must be absent on a human decision: %+v", last)
	}
	if wm, ok := last["waitMs"].(float64); !ok || wm < 20 {
		t.Fatalf("waitMs = %v, want >= 20", last["waitMs"])
	}

	go func() {
		waitPending(t, h.Broker(), 1)
		time.Sleep(20 * time.Millisecond)
		decideFirst(t, h.Broker(), broker.Decision{Outcome: broker.Denied, Reason: "no"})
	}()
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); err == nil {
		t.Fatal("want denied error")
	}
	_, recs = readAudit(t, path)
	last = recs[len(recs)-1]
	if wm, ok := last["waitMs"].(float64); !ok || wm < 20 {
		t.Fatalf("waitMs on deny = %v, want >= 20", last["waitMs"])
	}
}

func TestDeniedAndExpiredAreDistinct(t *testing.T) {
	fe := &fakeExec{}
	h, _ := newHub(t, fe)
	go decideFirst(t, h.Broker(), broker.Decision{Outcome: broker.Denied, Reason: "nope"})
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
	waitPending(t, b, 1)
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
	if err := config.Save(path, f, testMK); err != nil {
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
	go allowFirst(t, h.Broker())
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
// "s3cr3t-pw"), "agent" (no encrypted fields, no pin) and "keyonly" (no
// encrypted fields, AIVisible, pinned). The hub starts
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
	f.Servers = []config.Server{s, {Name: "agent", Host: "h", Port: 22, User: "u", Auth: "agent"},
		{Name: "keyonly", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true}}
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
	go allowFirst(t, h.Broker())
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

// I2: while an encrypted vault is locked its MAC is unchecked, so no server
// in it may run, even one with no encrypted fields.
func TestLockedVaultRefusesKeyOnlyServer(t *testing.T) {
	fe := &fakeExec{}
	h, _, _ := newEncHub(t, fe)
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "keyonly", Command: "ls"}); !errors.Is(err, ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
	if len(h.Broker().Pending()) != 0 || len(fe.calls) != 0 {
		t.Fatal("locked vault reached the broker or ran")
	}
	for _, s := range h.ServersForMCP() {
		if !s.Locked {
			t.Fatalf("%s listed as unlocked in a locked vault", s.Name)
		}
	}
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	for _, s := range h.ServersForMCP() {
		if s.Locked {
			t.Fatalf("%s still locked after unlock", s.Name)
		}
	}
}

func TestAuditRedactsSecretsBeforeApproval(t *testing.T) {
	h, path, _ := newEncHub(t, &fakeExec{})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	go decideFirst(t, h.Broker(), broker.Decision{Outcome: broker.Denied})
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
		// I3: SSH detail (host:port, fingerprints) goes to the audit reason,
		// never to the AI.
		{"error", &fakeExec{res: out, err: errors.New("dial tcp 10.9.8.7:22: connection refused")}, &broker.Decision{Outcome: broker.Allowed}, false,
			func(t *testing.T, rec map[string]any, res ExecResponse, err error) {
				if !errors.Is(err, ErrConnFailed) || err.Error() != "connection to server failed; see the app for details" {
					t.Fatalf("err = %v", err)
				}
				if rec["outcome"] != "error" || rec["reason"] != "dial tcp 10.9.8.7:22: connection refused" {
					t.Fatalf("rec = %v", rec)
				}
			}},
		{"host_key_mismatch", &fakeExec{res: out, err: fmt.Errorf("ssh: handshake failed: %w for 10.9.8.7:22: got SHA256:evil, pinned SHA256:abc", sshx.ErrHostKeyMismatch)}, &broker.Decision{Outcome: broker.Allowed}, false,
			func(t *testing.T, rec map[string]any, res ExecResponse, err error) {
				if err == nil || err.Error() != "host key verification failed; check the server in the app" {
					t.Fatalf("err = %v", err)
				}
				if rec["outcome"] != "error" || !strings.Contains(rec["reason"].(string), "SHA256:evil") {
					t.Fatalf("rec = %v", rec)
				}
			}},
		{"timeout", &fakeExec{res: out, err: fmt.Errorf("%w: %w", sshx.ErrTimeout, context.DeadlineExceeded)}, &broker.Decision{Outcome: broker.Allowed}, false,
			func(t *testing.T, rec map[string]any, res ExecResponse, err error) {
				if err == nil || err.Error() != "command timed out: context deadline exceeded" || rec["outcome"] != "error" {
					t.Fatalf("err=%v rec=%v", err, rec)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, path := newHub(t, c.fe)
			if c.decision != nil {
				go decideFirst(t, h.Broker(), *c.decision)
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

// I3: a resolve failure before approval (here, an unreadable key file)
// must not show the AI a local path.
func TestResolveFailureIsGenericToAI(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Servers[0].KeyPath = "/nonexistent/secret-dir/id_ed25519"
	if err := config.Save(path, f, testMK); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	_, err = h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	if !errors.Is(err, ErrConnFailed) || strings.Contains(err.Error(), "secret-dir") {
		t.Fatalf("got %v", err)
	}
	if len(h.Broker().Pending()) != 0 || len(fe.calls) != 0 {
		t.Fatal("unresolvable server reached the broker")
	}
}

func TestSixthPendingRefused(t *testing.T) {
	h, _ := newHubExpiry(t, &fakeExec{}, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 5)
	for range 5 {
		go func() { h.Exec(ctx, ExecRequest{Server: "vis", Command: "ls"}); done <- struct{}{} }()
	}
	waitPending(t, h.Broker(), 5)
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
	go allowFirst(t, h.Broker())
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

// No hub path can learn a host key: every resolved config is strict with no
// learner, and an AI exec on an unpinned server leaves the store untouched.
func TestHubNeverLearnsHostKeys(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	for _, name := range []string{"vis", "nokey", "hid"} {
		dc, err := h.Resolve(name)
		if err != nil {
			t.Fatal(err)
		}
		if !dc.StrictHostKey || dc.OnLearnHostKey != nil {
			t.Fatalf("%s: not strict: %+v", name, dc)
		}
	}
	before, _ := os.ReadFile(path)
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "nokey", Command: "ls"}); !errors.Is(err, ErrNoHostKey) {
		t.Fatalf("want ErrNoHostKey, got %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) || len(fe.calls) != 0 {
		t.Fatal("an AI exec on an unpinned server wrote the store or dialled")
	}
}

// An approval is for user@host:port and pin as shown; an edit during the
// wait voids it.
func TestApprovalBoundToEndpoint(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHubExpiry(t, fe, time.Minute)
	done := make(chan error, 1)
	go func() { _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); done <- err }()
	waitPending(t, h.Broker(), 1)
	if got := h.Broker().Pending()[0].Target; got != "u@h:22" {
		t.Fatalf("target = %q", got)
	}
	if err := config.Update(path, testMK, func(f *config.File) error { f.Servers[0].Port = 2222; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	allowFirst(t, h.Broker())
	if err := <-done; !errors.Is(err, ErrServerChanged) {
		t.Fatalf("got %v", err)
	}
	if len(fe.calls) != 0 {
		t.Fatalf("ran on the new endpoint: %v", fe.calls)
	}
	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if last["outcome"] != "error" || !strings.Contains(last["reason"].(string), "server changed") {
		t.Fatalf("audit: %v", last)
	}
}

// A store that lost its KDF lost its MAC check with it: the hub keeps the old
// state, locked or unlocked, instead of trusting the stripped file.
func TestReloadRejectsStoreThatLostItsKDF(t *testing.T) {
	for _, unlock := range []bool{true, false} {
		h, path, _ := newEncHub(t, &fakeExec{})
		if unlock {
			if err := h.Unlock("pw"); err != nil {
				t.Fatal(err)
			}
		}
		f, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		f.KDF, f.MAC = nil, ""
		f.Servers[0].Host = "evil"
		f.Revision++
		b, _ := json.Marshal(f)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := h.Reload(); err == nil {
			t.Fatalf("unlocked=%v: KDF-stripped store accepted", unlock)
		}
		if s, _ := h.Deps().File.FindServer("enc"); s.Host != "h" || h.Deps().File.KDF == nil {
			t.Fatalf("unlocked=%v: stripped file took effect: %+v", unlock, s)
		}
	}
}

// The pin leg of the binding: same endpoint, but the key was forgotten and a
// different one trusted during the wait.
func TestApprovalBoundToPin(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHubExpiry(t, fe, time.Minute)
	done := make(chan error, 1)
	go func() { _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); done <- err }()
	waitPending(t, h.Broker(), 1)
	if err := config.Update(path, testMK, func(f *config.File) error { f.Servers[0].HostKey = ""; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := config.RecordHostKey(path, "vis", "h", 22, "SHA256:other", "", testMK); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	allowFirst(t, h.Broker())
	if err := <-done; !errors.Is(err, ErrServerChanged) {
		t.Fatalf("got %v", err)
	}
	if len(fe.calls) != 0 {
		t.Fatalf("ran under the new pin: %v", fe.calls)
	}
	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if last["outcome"] != "error" || !strings.Contains(last["reason"].(string), "server changed") {
		t.Fatalf("audit: %v", last)
	}
}

// A store with no KDF was never MAC'd, so its aiVisible flags and pins are
// unauthenticated: the hub treats it as no vault at all. The AI sees no
// servers and every exec is refused (audited, never dialled); the UI door
// refuses terminals.
func TestNoVaultGivesAINothing(t *testing.T) {
	for _, missing := range []bool{false, true} {
		fe := &fakeExec{}
		dir := t.TempDir()
		path := filepath.Join(dir, "servers.json")
		if !missing {
			f := &config.File{Version: 1, Servers: []config.Server{
				{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true},
			}}
			if err := config.Save(path, f, nil); err != nil {
				t.Fatal(err)
			}
		}
		audit, err := broker.OpenAudit(filepath.Join(dir, "audit.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { audit.Close() })
		h, err := New(Options{StorePath: path, Audit: audit, Dialer: func(sshx.DialConfig) Executor { return fe }})
		if err != nil {
			t.Fatal(err)
		}
		if got := h.ServersForMCP(); len(got) != 0 {
			t.Fatalf("missing=%v: listServers = %+v", missing, got)
		}
		for _, sudo := range []bool{false, true} {
			if _, err := h.Exec(context.Background(), ExecRequest{Client: "c", Server: "vis", Command: "ls", Sudo: sudo}); !errors.Is(err, ErrNoVault) {
				t.Fatalf("missing=%v sudo=%v: want ErrNoVault, got %v", missing, sudo, err)
			}
		}
		if len(fe.calls) != 0 || len(h.Broker().Pending()) != 0 {
			t.Fatalf("missing=%v: no-vault exec reached the broker or dialled", missing)
		}
		_, recs := readAudit(t, path)
		if len(recs) != 2 || recs[0]["outcome"] != "error" || recs[0]["reason"] != ErrNoVault.Error() || recs[0]["server"] != "vis" || recs[1]["sudo"] != true {
			t.Fatalf("missing=%v: audit = %v", missing, recs)
		}
		c, _, _ := startTermDoor(t, h)
		err = c.Call(context.Background(), "term.open", map[string]any{"id": "t1", "server": "vis", "rows": 24, "cols": 80}, nil)
		if err == nil || err.Error() != errNoVault.Error() {
			t.Fatalf("missing=%v: term.open: want %q, got %v", missing, errNoVault, err)
		}
	}
}

// The no-vault refusal is audited before any validation, so it must not let
// the AI write unbounded bytes to the audit log.
func TestNoVaultAuditIsBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	audit, err := broker.OpenAudit(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { audit.Close() })
	h, err := New(Options{StorePath: path, Audit: audit})
	if err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("x", 1<<20)
	if _, err := h.Exec(context.Background(), ExecRequest{Server: huge, Command: huge, Description: huge, Client: huge}); !errors.Is(err, ErrNoVault) {
		t.Fatalf("got %v", err)
	}
	raw, recs := readAudit(t, path)
	if len(recs) != 1 || recs[0]["reason"] != ErrNoVault.Error() || len(raw) > 8<<10 {
		t.Fatalf("audit record is %d bytes: %v", len(raw), len(recs))
	}
}
