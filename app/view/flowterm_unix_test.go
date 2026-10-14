//go:build unix

package view

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Ports of the terminal parts of desktop/e2e/smoke.spec.ts and ui.spec.ts.

func TestFlowSmokeTerminal(t *testing.T) {
	needLib(t)
	f := flow(t)
	f.unlock()
	id := f.openBox()
	f.tt.Type("echo smoke-ok")
	f.shows(id, "echo smoke-ok")

	f.click("Lock")
	f.until(func() bool { return f.tt.HasText("Master password") }, "the unlock screen")
	f.unlock()
	if tabs := f.a.tabs.Tabs; len(tabs) != 1 || tabs[0].Server != "box" || tabs[0].State != "open" || f.tt.HasText("box · exited") {
		t.Fatalf("tabs after the lock: %+v", tabs)
	}
	f.until(func() bool { return f.tt.Focused("Terminal box") }, "the same terminal focused again")
	f.tt.Type(" after-lock")
	f.shows(id, "echo smoke-ok after-lock") // the same shell: no reconnect

	r3 := f.exec("r3", "echo sc4")
	f.until(func() bool { return len(f.a.Items()) == 1 }, "r3 in the column")
	f.click("Terminal box")
	f.tt.Type("x")
	f.tt.Key(0, ui.KeyEnter)
	// Electron's 1000 ms window: a negative check needs the time.
	f.stillPending(r3, 1000*time.Millisecond, "keys in a terminal")
	f.click("Deny")
	f.denied(r3, "Denied by user")
	f.until(func() bool { return len(f.a.Items()) == 0 }, "an empty column")
	f.wantAudit("echo sc4", "denied", "")
}

func TestFlowHostsHomeAndClosingTheLastTab(t *testing.T) {
	needLib(t)
	f := flow(t)
	f.unlock()
	f.openBox()
	if f.tt.HasText("Search hosts") {
		t.Fatal("the host list shows behind the terminal")
	}
	f.click("Close tab")
	f.until(func() bool { return f.tt.HasText("Search hosts") && f.a.tabs.Active == "" }, "the Hosts home")
}
