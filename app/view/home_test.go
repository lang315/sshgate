package view

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
)

const keyBackspace = ui.KeyBackspace

func readyApp(t *testing.T, servers []hubapi.Server) (*App, *tester, *fakeDoor) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, StorePath: "/v/servers.json"}, servers: servers}
	a, tt, _, _ := newApp(t, d)
	tt.flush()
	return a, tt, d
}

func TestHostListSearchAndEmptyStates(t *testing.T) {
	_, tt, _ := readyApp(t, []hubapi.Server{{Name: "box", User: "test", Host: "127.0.0.1", Port: 2222, AIVisible: true}, {Name: "db", User: "pg", Host: "10.0.0.5", Port: 22}})
	for _, s := range []string{"Hosts · 2", "box", "test@127.0.0.1:2222", "AI", "New key", "Vault file: /v/servers.json — copy it to back up."} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if err := tt.Click("Search hosts"); err != nil {
		t.Fatal(err)
	}
	tt.Type("nomatch")
	tt.Frame()
	if !tt.HasText(`No hosts match "nomatch"`) || !tt.HasText("Hosts · 0") {
		t.Fatal(tt.Texts())
	}
	for range "nomatch" {
		tt.Key(0, keyBackspace)
	}
	tt.Type("BO")
	tt.Frame()
	if !tt.HasText("Hosts · 1") || !tt.HasText("box") {
		t.Fatal(tt.Texts())
	}
}

func TestNoHostsYet(t *testing.T) {
	_, tt, _ := readyApp(t, []hubapi.Server{})
	if !tt.HasText("No hosts yet. Add one with New host.") {
		t.Fatal(tt.Texts())
	}
}

func TestStoreErrorBanner(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, StoreError: "the vault file failed its integrity check or could not be read; the app keeps the last good copy"}}
	_, tt, _, _ := newApp(t, d)
	tt.flush()
	if !tt.HasText("The vault file failed its integrity check") {
		t.Fatal(tt.Texts())
	}
}

func TestLockButtonAndAIButton(t *testing.T) {
	_, tt, d := readyApp(t, []hubapi.Server{{Name: "box", AutoAllow: &hubapi.AutoAllow{Forever: true}}})
	if !tt.HasText("AI requests") {
		t.Fatal("AI button hidden while a host is on auto-allow:", tt.Texts())
	}
	if !tt.HasText("auto 1") {
		t.Fatal(tt.Texts())
	}
	d.resetCalls()
	if err := tt.Click("Lock"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if got := d.callList(); len(got) != 1 || got[0] != "lock" {
		t.Fatal(got)
	}
}

// The host list scrolls when it is taller than the window.
func TestHostListScrolls(t *testing.T) {
	var servers []hubapi.Server
	for i := 0; i < 40; i++ {
		servers = append(servers, hubapi.Server{Name: fmt.Sprintf("host%02d", i), User: "u", Host: "h", Port: 22})
	}
	_, tt, _ := readyApp(t, servers)
	tt.SetSize(800, 400)
	tt.Frame()
	r, ok := tt.Find("host39")
	if !ok || r.Y+r.H <= 400 {
		t.Fatalf("last host should start below the window: %+v %v", r, ok)
	}
	tt.Move(400, 200)
	tt.Scroll(400, 200, 0, 20000)
	tt.Frame()
	r, ok = tt.Find("host39")
	if !ok || r.Y < 0 || r.Y+r.H > 400 {
		t.Fatalf("last host not in view after scrolling: %+v %v", r, ok)
	}
}

func TestHostNameIsDisplayText(t *testing.T) {
	_, tt, _ := readyApp(t, []hubapi.Server{{Name: "\u202eevil", User: "u", Host: "h", Port: 22}})
	for _, s := range tt.Texts() {
		if strings.ContainsRune(s, '\u202e') {
			t.Fatalf("raw U+202E in %q", s)
		}
	}
}

// The card's Files, Tunnels, Edit and Delete are icon buttons: a word would
// push the row past the 240 px card on wider fonts. Only their names remain.
func TestCardActionsAreIcons(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}}
	_, tt, _, _ := newApp(t, d)
	tt.flush()
	for _, w := range []string{"Files", "Tunnels", "Edit", "Delete"} {
		if _, ok := tt.Find(w + " box"); !ok {
			t.Errorf("no button named %q in %q", w+" box", tt.Texts())
		}
		if count(tt.Texts(), w) != 0 {
			t.Errorf("the card shows the word %q: %q", w, tt.Texts())
		}
	}
}

// The entering view fades in from 0, and nothing animates once it is in.
func TestViewChangeFadesInThenRests(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box"}}}
	d.audit = auditDoor(&[]hubapi.AuditEntry{execEntry(1, "echo one", "allowed")}, 200)
	a, tt, _, _ := newApp(t, d)
	tt.flush()
	if a.viewFading {
		t.Fatal("the first view fades")
	}
	clickT(t, tt, "Audit")
	if !a.viewFading {
		t.Fatal("entering Audit does not fade")
	}
	tt.flushOnce()
	tt.Advance(40 * time.Millisecond)
	tt.Frame()
	row, ok := tt.Find("echo one")
	if !ok {
		t.Fatalf("no row: %q", tt.Texts())
	}
	row = grow(row, 4)
	mid := shot(tt)
	tt.flush()
	tt.Advance(time.Second)
	tt.Frame()
	tt.Frame()
	if a.viewFading || !tt.HasText("echo one") {
		t.Fatalf("fading %v texts %q", a.viewFading, tt.Texts())
	}
	rest := shot(tt)
	if sameRegion(mid, rest, row) {
		t.Fatal("the row looks the same mid-fade and at rest")
	}
	tt.Frame()
	if !sameRegion(rest, shot(tt), row) {
		t.Fatal("the row changes at rest")
	}
}

// The AI column never narrows or clips: Deny and Allow of a request stay
// inside the window beside the audit table, at the minimum width and above.
func TestAIColumnStaysWholeBesideAuditAndHosts(t *testing.T) {
	for _, sz := range [][2]int{{900, 500}, {1000, 640}} {
		d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box", User: "u", Host: "h", Port: 22}},
			pending: []hubapi.Request{reqN("r1", "box", "ls")}}
		d.audit = auditDoor(&[]hubapi.AuditEntry{execEntry(1, "echo one", "allowed")}, 200)
		_, tt, _, _ := newApp(t, d)
		tt.SetSize(sz[0], sz[1])
		tt.flush()
		check := func(view string) {
			t.Helper()
			tt.Advance(time.Second)
			tt.Frame()
			for _, l := range []string{"Deny", "Allow"} {
				r, ok := tt.Find(l)
				if !ok || r.X < 0 || r.X+r.W > float32(sz[0]) {
					t.Fatalf("%dx%d %s: %q at %+v (found %v)", sz[0], sz[1], view, l, r, ok)
				}
			}
		}
		check("hosts")
		clickT(t, tt, "Audit")
		tt.flush()
		check("audit")
	}
}

// Under Reduce Motion no view or column fade starts, so no frame is transparent.
func TestReduceMotionSkipsFades(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box"}},
		pending: []hubapi.Request{reqN("r1", "box", "ls")}}
	d.audit = auditDoor(&[]hubapi.AuditEntry{execEntry(1, "echo one", "allowed")}, 200)
	a := &App{Door: d, Now: (&clock{t: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}).now, Shell: &fakeShell{}}
	tt := &tester{t: t}
	a.Update = func(fn func()) { tt.mu.Lock(); tt.pending = append(tt.pending, fn); tt.mu.Unlock() }
	tt.Tester = ui.NewTesterWith(a.Build, 800, 500, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	running(a, tt)
	clickT(t, tt, "Audit")
	if a.viewFading {
		t.Fatal("the view fades under Reduce Motion")
	}
}

// With a sheet open beside the AI column, every decision element stays whole
// inside the window and Allow does not move when the sheet opens or closes
// (the sidebar steps aside instead).
func TestAIColumnStaysPutWithSheetOpen(t *testing.T) {
	for _, sz := range [][2]int{{900, 500}, {999, 500}} {
		d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box", User: "u", Host: "h", Port: 22}},
			pending: []hubapi.Request{reqN("r1", "box", "ls"), reqN("r2", "box", "pwd")}}
		_, tt, _, _ := newApp(t, d)
		tt.SetSize(sz[0], sz[1])
		tt.flush()
		tt.Advance(time.Second)
		tt.Frame()
		r, ok := tt.Find("Allow")
		if !ok {
			t.Fatalf("%v: no Allow", sz)
		}
		allowX := r.X
		check := func(step string) {
			t.Helper()
			tt.Advance(time.Second)
			tt.Frame()
			for _, l := range []string{"Close AI requests", "Deny all", "Deny", "Allow"} {
				r, ok := tt.Find(l)
				if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > float32(sz[0]) || r.Y+r.H > float32(sz[1]) {
					t.Fatalf("%v %s: %q at %+v (found %v)", sz, step, l, r, ok)
				}
			}
			if r, _ := tt.Find("Allow"); r.X != allowX {
				t.Fatalf("%v %s: Allow moved from x %v to %v", sz, step, allowX, r.X)
			}
		}
		check("no sheet")
		for _, s := range []struct{ open, closeL string }{{"New host", "Close host editor"}, {"Import from SSH config", "Close import"}} {
			clickT(t, tt, s.open)
			check(s.open)
			clickT(t, tt, s.closeL)
			check("closed after " + s.open)
		}
	}
}

// The card's open button starts at the left, so the address has the card's
// width and is not cut; New host is the screen's filled primary.
func TestHostCardAddressWholeAndNewHostPrimary(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	for _, dark := range []bool{false, true} {
		_, tt, _ := readyApp(t, []hubapi.Server{{Name: "staging", User: "deploy", Host: "10.0.0.6", Port: 22, HostKey: "k", AIVisible: true}})
		tt.SetDark(dark)
		tt.SetSize(1000, 640)
		tt.Advance(time.Second)
		tt.Frame()
		if !tt.HasText("deploy@10.0.0.6:22") {
			t.Fatalf("dark=%v: the address is cut: %q", dark, tt.Texts())
		}
		r, ok := tt.Find("New host")
		if !ok {
			t.Fatal(tt.Texts())
		}
		if n := countNear(shot(tt), ui.Rect{X: r.X - 4, Y: r.Y, W: 2, H: r.H}, palette(dark).Accent, 3); n < int(r.H)*2-2 {
			t.Fatalf("dark=%v: New host is not the filled primary (%d of %d)", dark, n, int(r.H)*2)
		}
	}
}
