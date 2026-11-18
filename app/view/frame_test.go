package view

import (
	"testing"
	"time"

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
