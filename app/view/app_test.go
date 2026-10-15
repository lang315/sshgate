package view

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/model"
)

// fakeDoor answers UI-door calls from fields and records each method.
type fakeDoor struct {
	mu       sync.Mutex
	calls    []string
	status   hubapi.Status
	statusFn func() hubapi.Status // overrides status when set; called unlocked, so it may block
	pw       string
	servers  []hubapi.Server
	pending  []hubapi.Request
	pendGate chan struct{}            // when set, pending waits on it
	gates    map[string]chan struct{} // these methods answer once their channel closes
	errs     map[string]error
	params   map[string]string
	hang     map[string]bool // these methods never answer: they wait for ctx
	scan     hubapi.ImportScan
	imported hubapi.ImportResult
}

func (d *fakeDoor) Call(ctx context.Context, method string, params, result any) error {
	d.mu.Lock()
	d.calls = append(d.calls, method)
	if d.params == nil {
		d.params = map[string]string{}
	}
	b, _ := json.Marshal(params)
	d.params[method] = string(b)
	wait := d.gates[method]
	err, hang := d.errs[method], d.hang[method]
	st, stFn := d.status, d.statusFn
	gate := d.pendGate
	srv, pend := d.servers, d.pending
	scan, imported := d.scan, d.imported
	d.mu.Unlock()
	if wait != nil {
		<-wait
	}
	if err != nil {
		return err
	}
	if hang {
		<-ctx.Done()
		return ctx.Err()
	}
	var out any
	switch method {
	case "status":
		if stFn != nil {
			st = stFn()
		}
		out = st
	case "unlock":
		var p struct{ Password string }
		json.Unmarshal(b, &p)
		if p.Password != d.pw {
			return errors.New("wrong master password")
		}
		d.mu.Lock()
		d.status.Locked = false
		d.mu.Unlock()
		out = map[string]any{}
	case "servers":
		out = srv
	case "pending":
		if gate != nil {
			<-gate
		}
		out = pend
	case "import.scan":
		out = scan
	case "import.apply":
		out = imported
	default:
		out = map[string]any{}
	}
	if result != nil {
		b, _ := json.Marshal(out)
		return json.Unmarshal(b, result)
	}
	return nil
}

func (d *fakeDoor) callList() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func (d *fakeDoor) resetCalls() { d.mu.Lock(); d.calls = nil; d.mu.Unlock() }

// set changes the door's fields under its lock (calls read them from goroutines).
func (d *fakeDoor) set(fn func()) { d.mu.Lock(); fn(); d.mu.Unlock() }

type fakeShell struct {
	mu      sync.Mutex
	pending []int
	notes   []string
	themes  []string
}

func (s *fakeShell) SetTheme(pref string) {
	s.mu.Lock()
	s.themes = append(s.themes, pref)
	s.mu.Unlock()
}

func (s *fakeShell) SetPending(n int) { s.mu.Lock(); s.pending = append(s.pending, n); s.mu.Unlock() }
func (s *fakeShell) Notify(id, title, _ string) {
	s.mu.Lock()
	s.notes = append(s.notes, id+" "+title)
	s.mu.Unlock()
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

// tester runs App headless; updates from goroutines queue until flush.
type tester struct {
	*ui.Tester
	t       *testing.T
	mu      sync.Mutex
	pending []func()
}

func newTester(t *testing.T, a *App) *tester {
	tt := &tester{t: t}
	a.Update = func(fn func()) { tt.mu.Lock(); tt.pending = append(tt.pending, fn); tt.mu.Unlock() }
	tt.Tester = ui.NewTester(a.Build, 800, 500)
	return tt
}

// flush runs queued updates (unlock runs off the UI thread) until none
// has come for 100 ms, or 2 s have passed.
func (tt *tester) flush() {
	deadline := time.Now().Add(2 * time.Second)
	for idle := 0; idle < 10 && time.Now().Before(deadline); {
		tt.mu.Lock()
		fns := tt.pending
		tt.pending = nil
		tt.mu.Unlock()
		for _, fn := range fns {
			fn()
		}
		tt.Frame()
		if len(fns) == 0 {
			idle++
		} else {
			idle = 0
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// flushOnce runs the updates queued so far, once, then draws a frame.
func (tt *tester) flushOnce() {
	tt.mu.Lock()
	fns := tt.pending
	tt.pending = nil
	tt.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
	tt.Frame()
}

func note(t *testing.T, a *App, tt *tester, method string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	a.Update(func() { a.Note(method, b) })
	tt.flush()
}

func running(a *App, tt *tester) {
	a.Update(func() { a.HubState(hubproc.State{Kind: "running"}) })
	tt.flush()
}

// newApp builds an App on a fake door, its clock and shell, with the hub running.
func newApp(t *testing.T, d *fakeDoor) (*App, *tester, *clock, *fakeShell) {
	c := &clock{t: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	sh := &fakeShell{}
	a := &App{Door: d, Now: c.now, Shell: sh}
	tt := newTester(t, a)
	running(a, tt)
	return a, tt, c, sh
}

func reqN(id, server, cmd string) hubapi.Request {
	return hubapi.Request{ID: id, Server: server, Command: cmd, Target: "test@127.0.0.1:22", Client: "e2e", TimeoutSec: 60}
}

func TestScreensFollowStatus(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: false}}
	a, tt, _, _ := newApp(t, d)
	if a.Screen() != model.ScreenCreateVault {
		t.Fatal(a.Screen())
	}
	d.set(func() { d.status = hubapi.Status{HasVault: true, Locked: true} })
	note(t, a, tt, "locked", map[string]string{"reason": "manual"})
	if a.Screen() != model.ScreenLocked {
		t.Fatal(a.Screen())
	}
}

func TestReadySeedsOnceAndOpensColumn(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box"}}, pending: []hubapi.Request{reqN("r1", "box", "ls")}}
	a, _, _, _ := newApp(t, d)
	if a.Screen() != model.ScreenReady || len(a.Items()) != 1 || !a.AIOpen() || len(a.Servers()) != 1 {
		t.Fatalf("screen %s items %d open %v servers %d", a.Screen(), len(a.Items()), a.AIOpen(), len(a.Servers()))
	}
	n := 0
	for _, m := range d.callList() {
		if m == "pending" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("pending called %d times", n)
	}
}

func TestNotificationsCallOnlyStatus(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}}
	a, tt, _, _ := newApp(t, d)
	d.resetCalls()
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "ls")})
	note(t, a, tt, "decided", map[string]any{"request": reqN("r1", "box", "ls"), "decision": map[string]string{"outcome": "denied"}})
	note(t, a, tt, "autoAllow.ran", map[string]any{"server": "box", "command": "ls", "description": "", "exitCode": 0, "time": "2026-10-15T12:00:00Z"})
	note(t, a, tt, "autoAllow.off", map[string]string{"server": "box", "reason": "expired"})
	note(t, a, tt, "audit.appended", map[string]any{"seq": 1, "record": map[string]any{}})
	for _, m := range d.callList() {
		if m != "status" {
			t.Fatalf("a notification called %q; only status is free (calls: %v)", m, d.callList())
		}
	}
	if len(a.Feed()) != 1 {
		t.Fatal("autoAllow.ran not in the feed")
	}
}

func TestPendingCountsAndNotifies(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}}
	a, tt, _, sh := newApp(t, d)
	r := reqN("r1", "box", "ls")
	r.Sudo = true
	note(t, a, tt, "pending", map[string]any{"request": r})
	note(t, a, tt, "pending", map[string]any{"request": r})
	note(t, a, tt, "decided", map[string]any{"request": r, "decision": map[string]string{"outcome": "allowed"}})
	if !reflect.DeepEqual(sh.pending, []int{1, 1, 0}) {
		t.Fatalf("tray counts %v", sh.pending)
	}
	if len(sh.notes) != 2 || sh.notes[0] != "r1 AI wants to run a command with sudo" {
		t.Fatalf("notifications %v", sh.notes)
	}
	if len(a.Items()) != 0 {
		t.Fatal("decided request still shown")
	}
}

func TestSeedRacingDecidedDoesNotResurrect(t *testing.T) {
	gate := make(chan struct{})
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, pending: []hubapi.Request{reqN("r1", "box", "ls")}, pendGate: gate}
	a, tt, _, _ := newApp(t, d)
	note(t, a, tt, "decided", map[string]any{"request": reqN("r1", "box", "ls"), "decision": map[string]string{"outcome": "denied"}})
	close(gate)
	tt.flush()
	if len(a.Items()) != 0 {
		t.Fatal("a request decided during the seed came back")
	}
}

func TestStatusReplyDoesNotReaddDroppedHost(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true, AutoHosts: []string{"box", "db"}}}
	a, tt, _, _ := newApp(t, d)
	if !reflect.DeepEqual(a.LockHosts(), []string{"box", "db"}) {
		t.Fatal(a.LockHosts())
	}
	// an autoAllow.off that lands while the next status call is in flight
	release := make(chan struct{})
	d.set(func() {
		d.statusFn = func() hubapi.Status {
			<-release
			return hubapi.Status{HasVault: true, Locked: true, AutoHosts: []string{"box", "db"}}
		}
	})
	a.Update(func() { a.Note("locked", json.RawMessage(`{"reason":"idle"}`)) })
	tt.flushOnce() // runs the locked note, which starts refresh
	a.Update(func() { a.Note("autoAllow.off", json.RawMessage(`{"server":"box","reason":"expired"}`)) })
	tt.flushOnce()
	close(release)
	tt.flush()
	if !reflect.DeepEqual(a.LockHosts(), []string{"db"}) {
		t.Fatalf("lock hosts %v: box came back from a stale status", a.LockHosts())
	}
}

func TestUnlockAndLockRules(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true}, pw: "right"}
	a, tt, _, _ := newApp(t, d)
	a.Update(func() { a.unlock("wrong") })
	tt.flush()
	if a.UnlockErr() != "wrong master password" || a.Screen() != model.ScreenLocked {
		t.Fatal(a.UnlockErr(), a.Screen())
	}
	a.Update(func() { a.unlock("right") })
	tt.flush()
	if a.UnlockErr() != "" || a.Screen() != model.ScreenReady {
		t.Fatal(a.UnlockErr(), a.Screen())
	}
	d.resetCalls()
	a.Update(func() { a.lock() })
	tt.flush()
	if got := d.callList(); !reflect.DeepEqual(got, []string{"lock"}) || d.params["lock"] != `{}` {
		t.Fatalf("lock sent %v %s; it must not refresh by itself", got, d.params["lock"])
	}
	d.set(func() {
		d.errs = map[string]error{"lock": errors.New("boom")}
		d.status.Locked = true
	})
	a.Update(func() { a.stopAndLock() })
	tt.flush()
	if a.UnlockErr() != "Could not stop auto-allow: boom" || d.params["lock"] != `{"stopAuto":true}` {
		t.Fatal(a.UnlockErr(), d.params["lock"])
	}
}

func TestLockedNoteSetsReasonAndPausesForever(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "a", AutoAllow: &hubapi.AutoAllow{Forever: true}}, {Name: "b", AutoAllow: &hubapi.AutoAllow{Until: "2099-01-01T00:00:00Z"}}}}
	a, tt, _, _ := newApp(t, d)
	d.set(func() { d.status.Locked = true })
	// Run only the note: the status reply it asks for moves to the locked
	// screen, which clears the host list.
	a.Update(func() { a.Note("locked", json.RawMessage(`{"reason":"idle"}`)) })
	tt.flushOnce()
	s := a.Servers()
	if len(s) != 2 || !reflect.DeepEqual(s[0].AutoAllow, &hubapi.AutoAllow{Forever: true, Paused: true}) || s[1].AutoAllow != nil {
		t.Fatalf("servers after the locked note: %+v %+v", s[0].AutoAllow, s[1].AutoAllow)
	}
	tt.flush()
	if a.LockReason() != "idle" {
		t.Fatal(a.LockReason())
	}
	note(t, a, tt, "locked", map[string]string{"reason": "grantsEnded"})
	if a.LockReason() != "idle" {
		t.Fatal("grantsEnded replaced the stored reason:", a.LockReason())
	}
}

func TestHubRestartResetsState(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, pending: []hubapi.Request{reqN("r1", "box", "ls")}}
	a, tt, _, sh := newApp(t, d)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r2", "box", "id")})
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", Attempt: 1, In: time.Second}) })
	tt.flush()
	if len(a.Items()) != 0 || len(a.Feed()) != 0 || a.Screen() != model.ScreenHub || sh.pending[len(sh.pending)-1] != 0 {
		t.Fatalf("items %d feed %d screen %s tray %v", len(a.Items()), len(a.Feed()), a.Screen(), sh.pending)
	}
}

// TestRepliesFromStoppedHubDropped: a pending seed and a status reply asked
// of a hub that then stopped are dropped when they land after a restart.
func TestRepliesFromStoppedHubDropped(t *testing.T) {
	gate := make(chan struct{})
	release := make(chan struct{})
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, pending: []hubapi.Request{reqN("old", "box", "ls")}, pendGate: gate}
	a, tt, _, _ := newApp(t, d) // the seed's pending call waits on gate
	d.set(func() {
		d.statusFn = func() hubapi.Status {
			<-release
			return hubapi.Status{HasVault: true, Locked: true, AutoHosts: []string{"box"}}
		}
	})
	a.Update(func() { a.Note("locked", json.RawMessage(`{"reason":"idle"}`)) })
	tt.flushOnce() // a status call is now in flight to the old hub
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", Attempt: 1, In: time.Second}) })
	tt.flushOnce()
	close(gate)
	close(release)
	tt.flush()
	if len(a.Items()) != 0 || len(a.LockHosts()) != 0 || a.Screen() != model.ScreenHub {
		t.Fatalf("items %v lock hosts %v screen %s: a stopped hub's reply landed", a.Items(), a.LockHosts(), a.Screen())
	}
	// The new hub's own replies still land.
	d.set(func() {
		d.statusFn, d.pendGate = nil, nil
		d.status = hubapi.Status{HasVault: true}
		d.pending = []hubapi.Request{reqN("new", "box", "id")}
	})
	running(a, tt)
	if it := a.Items(); len(it) != 1 || it[0].Request.ID != "new" {
		t.Fatalf("items after restart %+v", it)
	}
}

// TestStaleStatusReplyIgnored: the hub serves calls concurrently, so a
// status reply can land after a newer one; it must not undo the newer one.
func TestStaleStatusReplyIgnored(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true, AutoHosts: []string{"box", "db"}}}
	a, tt, _, _ := newApp(t, d)
	gate, arrived := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	calls := 0
	d.set(func() {
		d.statusFn = func() hubapi.Status {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n == 1 { // the older call: answered last, from before box's grant ended and the unlock
				close(arrived)
				<-gate
				return hubapi.Status{HasVault: true, Locked: true, AutoHosts: []string{"box", "db"}}
			}
			return hubapi.Status{HasVault: true}
		}
	})
	a.Update(func() { a.refresh() })
	tt.flushOnce()
	<-arrived // calls are numbered on arrival: the newer refresh must not reach the door first
	a.Update(func() { a.Note("autoAllow.off", json.RawMessage(`{"server":"box","reason":"expired"}`)) })
	a.Update(func() { a.refresh() })
	tt.flush()
	if a.Screen() != model.ScreenReady {
		t.Fatal("the newer reply did not apply:", a.Screen())
	}
	close(gate)
	tt.flush()
	if a.Screen() != model.ScreenReady || len(a.LockHosts()) != 0 {
		t.Fatalf("screen %s lock hosts %v: the older reply landed last and applied", a.Screen(), a.LockHosts())
	}
}

// TestHungHubCallTimesOut: a running hub that never answers must not leave
// Unlock disabled; the call fails like any other failure.
func TestHungHubCallTimesOut(t *testing.T) {
	old := callTimeout
	callTimeout = 50 * time.Millisecond
	t.Cleanup(func() { callTimeout = old })
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true}, pw: "right", hang: map[string]bool{"unlock": true}}
	a, tt, _, _ := newApp(t, d)
	a.Update(func() { a.unlock("right") })
	tt.flush()
	if a.busy || a.UnlockErr() != "hub did not answer in 0 s" {
		t.Fatalf("busy %v err %q after the timeout", a.busy, a.UnlockErr())
	}
	d.set(func() { d.hang = nil })
	a.Update(func() { a.unlock("right") })
	tt.flush()
	if a.Screen() != model.ScreenReady {
		t.Fatal("Unlock not usable after a timeout:", a.Screen(), a.UnlockErr())
	}
}

// TestDenyAllErrorClearsWhenListEmpties is M5.
func TestDenyAllErrorClearsWhenListEmpties(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, errs: map[string]error{"denyAll": errors.New("deny all failed")}}
	a, tt, _, _ := newApp(t, d)
	for _, id := range []string{"r1", "r2"} {
		note(t, a, tt, "pending", map[string]any{"request": reqN(id, "box", "ls")})
	}
	a.Update(func() { a.denyAll() })
	tt.flush()
	if !tt.HasText("deny all failed") {
		t.Fatal(tt.Texts())
	}
	for _, id := range []string{"r1", "r2"} {
		note(t, a, tt, "decided", map[string]any{"request": reqN(id, "box", "ls"), "decision": map[string]string{"outcome": "denied"}})
	}
	if tt.HasText("deny all failed") {
		t.Fatal("the Deny all error outlived the list")
	}
}
