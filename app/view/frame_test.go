package view

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

func TestSidebarChoosesHostsAndAudit(t *testing.T) {
	a, tt, _ := readyApp(t, []hubapi.Server{boxServer})
	clickT(t, tt, "Audit")
	if a.tabs.Active != model.AuditTab {
		t.Fatalf("active %q after Audit", a.tabs.Active)
	}
	clickT(t, tt, "Hosts")
	if a.tabs.Active != "" {
		t.Fatalf("active %q after Hosts", a.tabs.Active)
	}
}

func TestSidebarHiddenWithTab(t *testing.T) {
	needLib(t)
	a, tt, _, _, _ := tabApp(t)
	clickT(t, tt, "Open box")
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" }, "t1 open")
	if _, ok := tt.Find("Audit"); ok {
		t.Fatal("the sidebar shows beside a terminal")
	}
	clickT(t, tt, "Home")
	if !tt.HasText("Search hosts") {
		t.Fatal("Home did not show the host list")
	}
	if _, ok := tt.Find("Audit"); !ok {
		t.Fatal("the sidebar is missing on Home")
	}
}

func TestTabStripDoesNotMoveWhenSidebarHides(t *testing.T) {
	needLib(t)
	a, tt, _, _, _ := tabApp(t)
	clickT(t, tt, "Open box")
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" }, "t1 open")
	clickT(t, tt, "Home")
	r1, ok := tt.Find("box")
	if !ok {
		t.Fatal("no tab button")
	}
	clickT(t, tt, "box")
	r2, _ := tt.Find("box")
	if r1.X != r2.X {
		t.Fatalf("tab moved from %v to %v", r1.X, r2.X)
	}
}

func TestToggleDoesNotResizeTerminalPerFrame(t *testing.T) {
	needLib(t)
	a, tt, _, hd, _ := tabApp(t)
	clickT(t, tt, "Open box")
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" }, "t1 open")
	resizes := func() int {
		hd.mu.Lock()
		defer hd.mu.Unlock()
		n := 0
		for _, c := range hd.calls {
			if c == "term.resize" {
				n++
			}
		}
		return n
	}
	tt.Advance(time.Second)
	base := resizes()
	for i := 0; i < 2; i++ { // open then close the AI column
		a.aiOpen = !a.aiOpen
		tt.Frame()
		tt.Advance(time.Second)
	}
	clickT(t, tt, "Home")
	clickT(t, tt, "box")
	tt.Advance(time.Second)
	if got := resizes() - base; got > 3 {
		t.Fatalf("%d term.resize for 3 layout changes", got)
	}
}

func TestLockReachableWithSidebarHidden(t *testing.T) {
	needLib(t)
	a, tt, _, _, _ := tabApp(t)
	clickT(t, tt, "Open box")
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" }, "t1 open")
	r, ok := tt.Find("Lock")
	if !ok || r.W <= 0 || r.X+r.W > 800 {
		t.Fatalf("Lock at %+v (%v) in an 800-wide window", r, ok)
	}
}

// Under Reduce Motion the column shows at once: its first frame is its last.
func TestAIColumnReduceMotionShowsAtOnce(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box", User: "u", Host: "h", Port: 22}},
		pending: []hubapi.Request{reqN("r1", "box", "ls")}}
	a, tt, _, _ := newApp(t, d)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.flush()
	tt.Advance(time.Second)
	a.Update(func() { a.aiOpen = true })
	tt.flushOnce()
	r, ok := tt.Find("Deny all")
	if !ok {
		t.Fatalf("no column: %q", tt.Texts())
	}
	col := ui.Rect{X: r.X - 100, Y: 0, W: 380, H: 500}
	first := shot(tt)
	tt.Advance(time.Second)
	tt.Frame()
	if !sameRegion(first, shot(tt), col) {
		t.Fatal("the column changed after its first frame")
	}
}

// Hiding the sidebar does not remount the AI column. A regression guard: it
// passed without c.Key("ai") too.
func TestAIColumnKeepsItsNodeWhenSidebarHides(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box", User: "u", Host: "h", Port: 22}},
		pending: []hubapi.Request{reqN("r1", "box", "ls")}}
	a, tt, _, _ := newApp(t, d)
	tt.flush()
	a.Update(func() { a.aiOpen = true })
	tt.flush()
	tt.Advance(time.Second)
	tt.Frame()
	closeID := func() uint64 {
		for _, n := range tt.Nodes() {
			if n.Label == "Close AI requests" {
				return n.ID
			}
		}
		return 0
	}
	before := closeID()
	if before == 0 {
		t.Fatal("no close button")
	}
	clickT(t, tt, "New host")
	if _, ok := tt.Find("Audit"); ok {
		t.Fatal("the sidebar did not hide")
	}
	if after := closeID(); after != before {
		t.Fatalf("node id %d became %d", before, after)
	}
}

// The top-bar buttons wear the rail's pill, not the stock face (light: dark
// grey on the teal rail).
func TestTopBarButtonsStyledForRail(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	for _, dark := range []bool{false, true} {
		p := palette(dark)
		_, tt, _ := readyApp(t, []hubapi.Server{boxServer})
		tt.SetDark(dark)
		tt.Advance(time.Second)
		tt.Frame()
		r, ok := tt.Find("Lock")
		if !ok {
			t.Fatalf("no Lock in %q", tt.Texts())
		}
		strip := ui.Rect{X: r.X - 4, Y: r.Y, W: 2, H: r.H}
		if n := countNear(shot(tt), strip, railButtonFill(p), 3); n < int(r.H)*2-2 {
			t.Fatalf("dark=%v: Lock not on the rail-button fill (%d of %d)", dark, n, int(r.H)*2)
		}
	}
}

// Settings and the theme control are drawn icons in RailText, not glyphs.
func TestRailIconsDrawn(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	for _, dark := range []bool{false, true} {
		p := palette(dark)
		d := &fakeDoor{status: hubapi.Status{HasVault: true, SoftLockLimit: "24h"}, servers: []hubapi.Server{boxServer}}
		_, tt, _, _ := newApp(t, d)
		tt.flush()
		tt.SetDark(dark)
		tt.Advance(time.Second)
		tt.Frame()
		for _, name := range []string{"Settings", "Dark", "Light"} {
			r, ok := tt.Find(name)
			if !ok {
				t.Fatalf("dark=%v: no %q in %q", dark, name, tt.Texts())
			}
			if n := countNear(shot(tt), grow(r, 14), p.RailText, 30); n < 20 {
				t.Fatalf("dark=%v: %s has %d RailText pixels, want >= 20", dark, name, n)
			}
		}
	}
}
