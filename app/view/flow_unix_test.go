//go:build unix

package view

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/internal/hubtest"
	"github.com/lang315/sshgate/app/model"
	"github.com/lang315/sshgate/app/termconn"
)

// flowT drives the App against a real hub and sshtestd: AI requests go in
// through the MCP door, every decision is a click or key in the UI.
type flowT struct {
	t      *testing.T
	a      *App
	tt     *tester
	c      *clock
	sock   string
	h      *hubapi.Client // setup only; a test never decides through it
	audit  string
	writes *countDoor
}

// countDoor is the door terminals use, counting the bytes of term.write
// sent as the user's (the idle lock counts only those).
type countDoor struct {
	termconn.Door
	mu   sync.Mutex
	user int
}

func (d *countDoor) Notify(method string, params any) error {
	if p, ok := params.(map[string]any); ok && method == "term.write" && p["user"] == true {
		d.mu.Lock()
		d.user += len(p["data"].([]byte))
		d.mu.Unlock()
	}
	return d.Door.Notify(method, params)
}

func (d *countDoor) userBytes() int { d.mu.Lock(); defer d.mu.Unlock(); return d.user }

func flow(t *testing.T, extraArgs ...string) *flowT {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	store := hubtest.SSHD(t)
	bin, _ := hubtest.Binaries(t)
	env, dir := hubtest.Runtime(t)
	c := &clock{t: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	a := &App{Now: c.now}
	tt := newTester(t, a) // sets a.Update before the hub can send a note
	mux := termconn.NewMux()
	p, err := hubproc.Start(context.Background(), hubproc.Launch{Command: bin, Args: append([]string{"hub", "--store=" + store}, extraArgs...), Env: env},
		func(m string, params json.RawMessage) {
			if mux.Note(m, params) { // as main routes them
				return
			}
			a.Update(func() { a.Note(m, params) })
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop() })
	a.Door = p.Client()
	writes := &countDoor{Door: p.Client()}
	a.Dial = func(ctx context.Context, id, server string, trust *hubapi.Trust) (*termconn.Conn, error) {
		return termconn.Open(ctx, writes, mux, id, server, 24, 80, trust)
	}
	running(a, tt)
	return &flowT{t: t, a: a, tt: tt, c: c, sock: hubtest.MCPSocket(dir), h: hubapi.New(p.Client()), audit: filepath.Join(filepath.Dir(store), "audit.jsonl"), writes: writes}
}

// until flushes the UI until cond holds, failing after 10 s.
func (f *flowT) until(cond func() bool, what string) {
	f.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		f.tt.flush()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %s; screen shows %q", what, f.tt.Texts())
		}
	}
}

func (f *flowT) exec(id, cmd string) <-chan hubtest.ExecResult {
	return hubtest.Exec(f.sock, map[string]any{"requestId": id, "client": "e2e", "server": "box", "command": cmd})
}

func (f *flowT) click(label string) {
	f.t.Helper()
	if err := f.tt.Click(label); err != nil {
		f.t.Fatal(err)
	}
}

// pass moves the fake clock past the 500 ms Allow delay.
func (f *flowT) pass() { f.c.add(600 * time.Millisecond); f.tt.Frame() }

func (f *flowT) unlock() {
	f.t.Helper()
	f.until(func() bool { return f.tt.HasText("Master password") }, "the unlock screen")
	f.click("Master password")
	f.tt.Type(hubtest.Password)
	f.tt.Key(0, ui.KeyEnter)
	// Not "Hosts · 1": a terminal tab may cover the host list.
	f.until(func() bool { return f.a.Screen() == model.ScreenReady && len(f.a.Servers()) == 1 }, "the ready screen")
}

// openBox opens a box tab from its card, as the e2e's waitOpen does, and
// waits until it is open and its terminal focused.
func (f *flowT) openBox() string {
	f.t.Helper()
	if f.a.tabs.Active != "" {
		f.click("Hosts")
	}
	n := len(f.a.tabs.Tabs)
	f.click("Open box")
	f.until(func() bool { return len(f.a.tabs.Tabs) == n+1 }, "a new tab")
	id := f.a.tabs.Active
	f.until(func() bool { return tabState(f.a, id) == "open" && f.tt.Focused("Terminal box") }, "the box tab open and focused")
	return id
}

// shows waits for s in tab id's terminal (sshtestd echoes what is typed).
func (f *flowT) shows(id, s string) {
	f.t.Helper()
	f.until(func() bool { return strings.Contains(termText(f.a, id), s) }, fmt.Sprintf("%q in the terminal", s))
}

func (f *flowT) result(ch <-chan hubtest.ExecResult) hubtest.ExecResult {
	f.t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(10 * time.Second):
		f.t.Fatal("no exec result after 10 s")
		return hubtest.ExecResult{}
	}
}

func (f *flowT) denied(ch <-chan hubtest.ExecResult, want string) {
	f.t.Helper()
	if r := f.result(ch); r.Err == nil || !strings.Contains(r.Err.Error(), want) {
		f.t.Fatalf("want an error containing %q, got %+v", want, r)
	}
}

// stillPending fails if the request was decided: its result came back.
func (f *flowT) stillPending(ch <-chan hubtest.ExecResult, d time.Duration, what string) {
	f.t.Helper()
	f.tt.flush()
	select {
	case r := <-ch:
		f.t.Fatalf("%s decided the request: %+v", what, r)
	case <-time.After(d):
	}
}

// audited waits for the exec record of cmd in the hub's audit log.
func (f *flowT) audited(cmd string) auditRec {
	f.t.Helper()
	var rec auditRec
	f.until(func() bool {
		file, err := os.Open(f.audit)
		if err != nil {
			return false
		}
		defer file.Close()
		for sc := bufio.NewScanner(file); sc.Scan(); {
			var r auditRec
			if json.Unmarshal(sc.Bytes(), &r) == nil && r.Kind == "" && r.Command == cmd {
				rec = r
				return true
			}
		}
		return false
	}, "the audit record of "+cmd)
	return rec
}

// auditRec is the part of an exec audit record the tests check.
type auditRec struct {
	Kind, Command, Outcome, Reason, Approval string
}

func (f *flowT) wantAudit(cmd, outcome, reason string) {
	f.t.Helper()
	if r := f.audited(cmd); r.Outcome != outcome || r.Reason != reason || r.Approval != "" {
		f.t.Fatalf("audit of %q: %+v, want outcome %q reason %q and no approval", cmd, r, outcome, reason)
	}
}

func TestFlowSmokeApprovals(t *testing.T) {
	f := flow(t)
	f.unlock()

	f.click("Lock")
	f.until(func() bool { return f.tt.HasText("Master password") }, "the unlock screen after Lock")
	f.unlock()

	r1 := f.exec("r1", "echo approved")
	f.until(func() bool { return f.a.AIOpen() && f.tt.HasText("echo approved") }, "r1 in the column")
	f.pass()
	f.click("Reason (optional)")
	f.tt.Key(0, ui.KeySpace)
	f.stillPending(r1, 300*time.Millisecond, "Space in Reason")
	for i := 0; i < 30; i++ {
		f.tt.Key(0, ui.KeyTab)
		if f.tt.Focused("Allow") {
			t.Fatal("Tab reached Allow")
		}
	}
	f.pass() // focus moves may have scrolled the list, which restarts the delay
	f.click("Allow")
	if r := f.result(r1); r.Err != nil || r.ExitCode != 0 || !strings.Contains(r.Stdout, "echo approved") {
		t.Fatalf("r1: %+v", r)
	}
	f.until(func() bool { return f.tt.HasText("Nothing waiting.") }, "an empty column")
	f.wantAudit("echo approved", "allowed", "")

	r2 := f.exec("r2", "rm -rf /")
	f.until(func() bool { return f.tt.HasText("rm -rf /") }, "r2 in the column")
	f.click("Reason (optional)")
	f.tt.Type("not today")
	f.tt.Key(0, ui.KeyEnter)
	f.denied(r2, "Denied by user: not today")
	f.wantAudit("rm -rf /", "denied", "not today")

	// r5 is sent once r4 shows, so r4 is the first card.
	r4 := f.exec("r4", "echo first")
	f.until(func() bool { return f.tt.HasText("echo first") }, "r4 in the column")
	r5 := f.exec("r5", "echo second")
	f.until(func() bool { return f.tt.HasText("echo second") }, "r5 in the column")
	f.pass()
	f.click("Allow")
	if r := f.result(r4); r.Err != nil || !strings.Contains(r.Stdout, "echo first") {
		t.Fatalf("r4: %+v", r)
	}
	f.until(func() bool { return len(f.a.Items()) == 1 }, "one request left")
	// The clock stands still, so the shrink's fresh delay is still running.
	f.click("Allow")
	f.click("Deny all")
	f.stillPending(r5, 300*time.Millisecond, "Allow right after the list shrank, or Deny all with one request,")
	f.click("Deny")
	f.denied(r5, "Denied by user")
	f.wantAudit("echo first", "allowed", "")
	f.wantAudit("echo second", "denied", "") // denyAll would have left "denied all by user"
}

func TestFlowColumnCollapseKeepsRequest(t *testing.T) {
	f := flow(t)
	f.unlock()
	c1 := f.exec("c1", "echo c1")
	f.until(func() bool { return f.a.AIOpen() && f.tt.HasText("echo c1") }, "the column opening itself")
	f.pass() // Allow is enabled now, so the reopen below must be what disables it

	f.click("Close AI requests")
	f.tt.flush()
	if f.a.AIOpen() || f.tt.HasText("echo c1") {
		t.Fatal("column still open")
	}
	f.stillPending(c1, 500*time.Millisecond, "collapsing the column")

	f.click("AI requests")
	f.tt.Frame()
	f.click("Allow")
	f.click("Send to tab")
	f.stillPending(c1, 300*time.Millisecond, "Allow or Send to tab right after the column reopened")
	if len(f.a.tabs.Tabs) != 0 {
		t.Fatal("Send to tab worked right after the column reopened")
	}

	f.pass()
	f.click("Deny")
	f.denied(c1, "Denied by user")
	f.wantAudit("echo c1", "denied", "")
}

func TestFlowKeyboardOnlyDenies(t *testing.T) {
	f := flow(t)
	f.unlock()
	k1 := f.exec("k1", "echo k1")
	f.until(func() bool { return f.tt.HasText("echo k1") }, "k1 in the column")
	f.c.add(time.Second)
	f.tt.Frame()

	f.click("Reason (optional)")
	f.tt.Key(0, ui.KeySpace)
	f.stillPending(k1, 300*time.Millisecond, "Space in Reason")
	for i := 0; i < 30; i++ {
		f.tt.Key(0, ui.KeyTab)
		if f.tt.Focused("Allow") || f.tt.Focused("Send to tab") {
			t.Fatal("Tab reached a mouse-only button")
		}
	}

	f.click("Reason (optional)")
	f.tt.Key(0, ui.KeyTab)
	if !f.tt.Focused("Deny") {
		t.Fatal("Tab from Reason did not land on Deny")
	}
	f.tt.Key(0, ui.KeySpace)
	f.denied(k1, "Denied by user")
	if r := f.audited("echo k1"); r.Outcome != "denied" {
		t.Fatalf("audit of k1: %+v", r)
	}
}

// ran fails unless the exec succeeded and printed out.
func (f *flowT) ran(ch <-chan hubtest.ExecResult, out string) {
	f.t.Helper()
	if r := f.result(ch); r.Err != nil || r.ExitCode != 0 || !strings.Contains(r.Stdout, out) {
		f.t.Fatalf("want a run printing %q, got %+v", out, r)
	}
}

// auto fails unless cmd's audit record says it ran without approval.
func (f *flowT) auto(cmd string) {
	f.t.Helper()
	if r := f.audited(cmd); r.Outcome != "allowed" || r.Approval != "auto" {
		f.t.Fatalf("audit of %q: %+v, want an auto-allowed run", cmd, r)
	}
}

// idleLocked waits for the idle lock. The wait makes no door call but
// status, so it cannot hold the vault open.
func (f *flowT) idleLocked(hosts string) {
	f.t.Helper()
	f.until(func() bool {
		return f.tt.HasText("Master password") && f.tt.HasText("Locked after inactivity.") &&
			(hosts == "" || f.tt.HasText("AI auto-allow is still running on: "+hosts))
	}, "the idle lock")
}

func TestFlowIdleLockWaitsForPendingRequests(t *testing.T) {
	f := flow(t, "--idleLock=1s")
	f.unlock()
	f.idleLocked("")

	f.unlock()
	i1 := f.exec("i1", "echo idle")
	f.until(func() bool { return f.tt.HasText("echo idle") }, "i1 in the column")
	time.Sleep(2500 * time.Millisecond) // negative check: real time past the 1 s idle lock
	f.tt.flush()
	if f.a.Screen() != model.ScreenReady || !f.tt.HasText("echo idle") {
		t.Fatalf("locked while a request was pending; screen shows %q", f.tt.Texts())
	}

	f.click("Deny")
	f.denied(i1, "Denied by user")
	f.until(func() bool { return f.tt.HasText("Master password") }, "the idle lock after the decision")
}

func TestFlowManualLockKeepsAutoAllow(t *testing.T) {
	f := flow(t)
	f.unlock()
	if err := f.h.SetAutoAllow(context.Background(), "box", "forever"); err != nil {
		t.Fatal(err)
	}

	f.click("Lock")
	f.until(func() bool {
		return f.tt.HasText("Master password") && f.tt.HasText("AI auto-allow is still running on: box")
	}, "the soft-lock screen")

	f.ran(f.exec("m1", "echo manual-1"), "manual-1")
	f.auto("echo manual-1")
}

func TestFlowSoftLockAndStop(t *testing.T) {
	f := flow(t, "--idleLock=2s")
	f.unlock()
	if err := f.h.SetAutoAllow(context.Background(), "box", "forever"); err != nil {
		t.Fatal(err)
	}
	f.idleLocked("box")
	f.ran(f.exec("s1", "echo soft-1"), "soft-1")
	f.auto("echo soft-1")

	f.unlock()
	f.ran(f.exec("s2", "echo soft-2"), "soft-2")
	f.auto("echo soft-2")

	f.idleLocked("box")
	f.click("Stop auto-allow and lock")
	f.until(func() bool {
		return f.tt.HasText("Master password") && !f.tt.HasText("AI auto-allow is still running on: box")
	}, "the auto-allow list to go")
	f.denied(f.exec("s3", "echo soft-3"), "Vault is locked")
}
