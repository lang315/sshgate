package view

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/termconn"
)

// tabApp is a ready app with host box whose tabs open on a fake hub
// (hubDoor), through a real termconn, as main wires the real door.
func tabApp(t *testing.T) (*App, *tester, *fakeDoor, *hubDoor, *termconn.Mux) {
	a, tt, d := readyApp(t, []hubapi.Server{{Name: "box", User: "test", Host: "127.0.0.1", Port: 2222}})
	hd, m := &hubDoor{}, termconn.NewMux()
	a.Dial = func(ctx context.Context, id, server string, trust *hubapi.Trust) (*termconn.Conn, error) {
		return termconn.Open(ctx, hd, m, id, server, 24, 80, trust)
	}
	return a, tt, d, hd, m
}

// hubNote sends a term.* notification as the hub would.
func hubNote(m *termconn.Mux, method string, v any) {
	b, _ := json.Marshal(v)
	m.Note(method, b)
}

// tabsUntil flushes until cond holds, failing after 10 s.
func tabsUntil(t *testing.T, tt *tester, cond func() bool, what string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); tt.flush() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; screen shows %q", what, tt.Texts())
		}
	}
}

func tabState(a *App, id string) string { t, _ := a.tabs.Get(id); return t.State }

func termText(a *App, id string) string {
	if t := a.terms[id]; t != nil && t.term != nil {
		return t.term.Text()
	}
	return ""
}

func (d *hubDoor) closeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, c := range d.calls {
		if c == "term.close" {
			n++
		}
	}
	return n
}

// dialRec answers Dial with err and records each call's id and server.
type dialRec struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (r *dialRec) dial(_ context.Context, id, server string, _ *hubapi.Trust) (*termconn.Conn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, id+" "+server)
	return nil, r.err
}

func (r *dialRec) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func TestHostCardOpensATabAndReconnectOpensAnother(t *testing.T) {
	a, tt, _ := readyApp(t, []hubapi.Server{{Name: "box", User: "u", Host: "h", Port: 22}})
	r := &dialRec{err: errors.New("Vault is locked; unlock it in the app")}
	a.Dial = r.dial
	if err := tt.Click("Open box"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if got := r.list(); len(got) != 1 || got[0] != "t1 box" {
		t.Fatal(got)
	}
	for _, s := range []string{"box · exited", "[exited: open failed: Vault is locked; unlock it in the app]", "Reconnect"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if err := tt.Click("Reconnect"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if got := r.list(); len(got) != 2 || got[1] != "t2 box" {
		t.Fatal(got)
	}
	if len(a.tabs.Tabs) != 1 || a.tabs.Tabs[0].ID != "t2" || a.tabs.Active != "t2" {
		t.Fatalf("tabs %+v active %q: Reconnect closes the old tab and opens one at the end", a.tabs.Tabs, a.tabs.Active)
	}
}

func TestTabsOpenSwitchAndClose(t *testing.T) {
	needLib(t)
	a, tt, _, hd, _ := tabApp(t)
	if err := tt.Click("Open box"); err != nil {
		t.Fatal(err)
	}
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" && tt.Focused("Terminal box") }, "t1 open and focused")
	if tt.HasText("Search hosts") {
		t.Fatal("the host list shows behind the terminal")
	}
	if err := tt.Click("Hosts"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if !tt.HasText("Search hosts") || a.tabs.Active != "" {
		t.Fatal("Hosts did not show the home tab")
	}
	if err := tt.Click("Open box"); err != nil {
		t.Fatal(err)
	}
	tabsUntil(t, tt, func() bool { return tabState(a, "t2") == "open" && a.tabs.Active == "t2" }, "a second box tab")
	if tabState(a, "t1") != "open" || hd.closeCount() != 0 {
		t.Fatal("opening a second tab must keep the first")
	}
	if err := tt.Click("box"); err != nil { // the first tab's button
		t.Fatal(err)
	}
	tabsUntil(t, tt, func() bool { return a.tabs.Active == "t1" && tt.Focused("Terminal box") }, "t1 active and focused")
	if err := tt.Click("Close tab"); err != nil {
		t.Fatal(err)
	}
	tabsUntil(t, tt, func() bool { return hd.closeCount() == 1 }, "term.close for t1")
	if _, ok := a.tabs.Get("t1"); ok || a.tabs.Active != "t2" {
		t.Fatalf("active %q: closing the active tab goes back to the previous one", a.tabs.Active)
	}
	if err := tt.Click("Close tab"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if a.tabs.Active != "" || !tt.HasText("Search hosts") {
		t.Fatal("closing the last tab must show the Hosts home")
	}
}

func TestExitAndDroppedInputShowInTheTerminal(t *testing.T) {
	needLib(t)
	a, tt, _, hd, m := tabApp(t)
	if err := tt.Click("Open box"); err != nil {
		t.Fatal(err)
	}
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" }, "t1 open")
	hubNote(m, "term.dropped", map[string]any{"id": "t1", "bytes": 7})
	hubNote(m, "term.exit", map[string]any{"id": "t1", "code": 0, "reason": ""})
	tabsUntil(t, tt, func() bool {
		s := termText(a, "t1")
		return strings.Contains(s, "[input dropped: 7 bytes]") && strings.Contains(s, "[exited: code 0]") && tt.HasText("box · exited")
	}, "the dropped and exit lines")
	if hd.closeCount() != 0 {
		t.Fatal("an exited terminal is gone from the hub: no term.close")
	}
	if err := tt.Click("Reconnect"); err != nil {
		t.Fatal(err)
	}
	tabsUntil(t, tt, func() bool { return tabState(a, "t2") == "open" && len(a.tabs.Tabs) == 1 }, "the reconnected tab")
}

// TestHubStopEndsEveryTab: a stopped hub's terminals end with "hub
// restarted" and stay ended; nothing reattaches after the restart.
func TestHubStopEndsEveryTab(t *testing.T) {
	needLib(t)
	a, tt, _, hd, m := tabApp(t)
	a.Update(func() { a.openTab("box"); a.openTab("box") })
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" && tabState(a, "t2") == "open" }, "two open tabs")
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", Attempt: 1, In: time.Second}) })
	tabsUntil(t, tt, func() bool {
		return strings.Contains(termText(a, "t1"), "[exited: hub restarted]") && strings.Contains(termText(a, "t2"), "[exited: hub restarted]")
	}, "both terminals to say why they ended")
	// End unregistered the terminal: data the dead hub sent late is dropped.
	hubNote(m, "term.data", map[string]any{"id": "t1", "data": []byte("late output")})
	running(a, tt)
	if tabState(a, "t1") != "exited" || tabState(a, "t2") != "exited" || !tt.HasText("box · exited") {
		t.Fatalf("tabs %+v after the restart", a.tabs.Tabs)
	}
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); tt.flush() {
		if strings.Contains(termText(a, "t1"), "late output") {
			t.Fatal("a term.data after End reached the tab")
		}
	}
	if hd.closeCount() != 0 {
		t.Fatal("term.close sent to a hub that is gone")
	}
}

// TestOpenReplyAfterHubStopIsClosed: a term.open answered after its hub
// stopped must not attach a terminal of the dead hub.
func TestOpenReplyAfterHubStopIsClosed(t *testing.T) {
	a, tt, _, hd, m := tabApp(t)
	gate := make(chan struct{})
	a.Dial = func(ctx context.Context, id, server string, trust *hubapi.Trust) (*termconn.Conn, error) {
		<-gate
		return termconn.Open(ctx, hd, m, id, server, 24, 80, trust)
	}
	if err := tt.Click("Open box"); err != nil {
		t.Fatal(err)
	}
	tt.flushOnce()
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", Attempt: 1, In: time.Second}) })
	tt.flushOnce()
	close(gate)
	running(a, tt)
	tabsUntil(t, tt, func() bool { return hd.closeCount() == 1 }, "the late terminal to close")
	if a.terms["t1"].term != nil || !tt.HasText("[exited: hub restarted]") {
		t.Fatalf("a dead hub's terminal attached; screen %q", tt.Texts())
	}
}

func TestClosingAnOpeningTabClosesItsLateTerminal(t *testing.T) {
	a, tt, _, hd, m := tabApp(t)
	gate := make(chan struct{})
	a.Dial = func(ctx context.Context, id, server string, trust *hubapi.Trust) (*termconn.Conn, error) {
		<-gate
		return termconn.Open(ctx, hd, m, id, server, 24, 80, trust)
	}
	if err := tt.Click("Open box"); err != nil {
		t.Fatal(err)
	}
	tt.flushOnce()
	if err := tt.Click("Close tab"); err != nil {
		t.Fatal(err)
	}
	close(gate)
	tabsUntil(t, tt, func() bool { return hd.closeCount() == 1 }, "the late terminal to close")
	if len(a.tabs.Tabs) != 0 || len(a.terms) != 0 {
		t.Fatalf("tabs %+v terms %v", a.tabs.Tabs, a.terms)
	}
}

// hangOpen is a hub that never answers term.open: the call waits for its ctx.
type hangOpen struct{ *hubDoor }

func (d hangOpen) Call(ctx context.Context, method string, params, result any) error {
	if method == "term.open" {
		<-ctx.Done()
		return ctx.Err() // no hub answer, not a *door.Error: termconn closes the hub terminal
	}
	return d.hubDoor.Call(ctx, method, params, result)
}

// TestHungOpenFailsTheTab (Decision 9): term.open is bounded by
// callTimeout; the tab fails with the slice-1 text, and termconn closes
// the hub terminal the hub may still open late.
func TestHungOpenFailsTheTab(t *testing.T) {
	old := callTimeout
	callTimeout = 50 * time.Millisecond
	t.Cleanup(func() { callTimeout = old })
	a, tt, _, hd, m := tabApp(t)
	a.Dial = func(ctx context.Context, id, server string, trust *hubapi.Trust) (*termconn.Conn, error) {
		return termconn.Open(ctx, hangOpen{hd}, m, id, server, 24, 80, trust)
	}
	if err := tt.Click("Open box"); err != nil {
		t.Fatal(err)
	}
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "exited" }, "the hung open to fail the tab")
	if !tt.HasText("[exited: open failed: hub did not answer in 0 s]") || !tt.HasText("box · exited") {
		t.Fatalf("screen %q", tt.Texts())
	}
	if hd.closeCount() != 1 || a.terms["t1"].term != nil {
		t.Fatalf("term.close sent %d times, terminal attached %v", hd.closeCount(), a.terms["t1"].term != nil)
	}
}

// TestManyTabsKeepLockReachable: the strip scrolls; it never pushes AI or
// Lock out of the window, and its far end can be scrolled to.
func TestManyTabsKeepLockReachable(t *testing.T) {
	var servers []hubapi.Server
	for i := range 30 {
		servers = append(servers, hubapi.Server{Name: fmt.Sprintf("host%02d", i), User: "u", Host: "h", Port: 22})
	}
	a, tt, d := readyApp(t, servers)
	a.Dial = (&dialRec{err: errors.New("no")}).dial
	a.Update(func() {
		for _, s := range servers {
			a.openTab(s.Name)
		}
		a.activate("t1")
	})
	tt.flush()
	r, ok := tt.Find("Lock")
	if !ok || r.W <= 0 || r.X+r.W > 800 {
		t.Fatalf("Lock at %+v (%v) in an 800-wide window", r, ok)
	}
	const last = "host29 · exited"
	if r, ok := tt.Find(last); !ok || r.X+r.W <= 800 {
		t.Fatalf("the last tab should start out of view: %+v %v", r, ok)
	}
	hosts, _ := tt.Find("Hosts")
	tt.Scroll(400, hosts.Y+hosts.H/2, 20000, 0)
	tt.Frame()
	if r, ok := tt.Find(last); !ok || r.X < 0 || r.X+r.W > 800 {
		t.Fatalf("the last tab not in view after scrolling: %+v %v", r, ok)
	}
	if err := tt.Click(last); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if a.tabs.Active != "t30" || !tt.HasText("[exited: open failed: no]") {
		t.Fatalf("active %q after clicking the last tab; screen %q", a.tabs.Active, tt.Texts())
	}
	if r, ok := tt.Find("Lock"); !ok || r.X+r.W > 800 {
		t.Fatalf("Lock at %+v (%v) after scrolling", r, ok)
	}
	d.resetCalls()
	if err := tt.Click("Lock"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if got := d.callList(); !slices.Contains(got, "lock") {
		t.Fatalf("Lock sent %v", got)
	}
}
