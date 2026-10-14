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

// Port of desktop/e2e/idle.spec.ts with a terminal tab open: its output,
// acks and query replies are not activity, so the idle lock still fires,
// and the tab is the same shell after unlock.
func TestFlowIdleLockWithATerminal(t *testing.T) {
	needLib(t)
	f := flow(t, "--idleLock=3s")
	f.unlock()
	id := f.openBox()
	f.tt.Type("echo before")
	f.shows(id, "echo before")
	if n := f.writes.userBytes(); n < len("echo before") {
		t.Fatalf("%d bytes sent as the user's, want at least %d", n, len("echo before"))
	}
	f.idleLocked("")
	f.unlock()
	if tabState(f.a, id) != "open" || f.a.tabs.Active != id {
		t.Fatalf("tabs after the idle lock: %+v", f.a.tabs.Tabs)
	}
	f.until(func() bool { return f.tt.Focused("Terminal box") }, "the terminal focused after unlock")
	f.tt.Type(" after")
	f.shows(id, "echo before after")
}

// Ports of desktop/e2e/keyboard.spec.ts tests 2 and 3.

func TestFlowShortcutAndEscMoveBetweenTerminalAndReason(t *testing.T) {
	needLib(t)
	f := flow(t)
	f.unlock()
	id := f.openBox()
	f.tt.Type("abc")
	f.shows(id, "abc")
	k2 := f.exec("k2", "echo k2")
	f.until(func() bool { return f.tt.HasText("echo k2") }, "k2 in the column")
	f.tt.Type("def") // an arriving request never takes the focus
	f.shows(id, "abcdef")
	if it := f.a.Items(); len(it) != 1 || *f.a.col.reasons[it[0].Request.ID] != "" {
		t.Fatal("typing reached the reason field")
	}
	f.tt.Key(chord, ui.KeyA)
	f.tt.flush()
	if !f.tt.Focused("Reason (optional)") {
		t.Fatal("the shortcut did not focus Reason")
	}
	f.tt.Key(0, ui.KeyEscape)
	f.tt.flush()
	if !f.tt.Focused("Terminal box") {
		t.Fatal("Esc did not return to the terminal")
	}
	f.tt.Type("g")
	f.shows(id, "abcdefg")
	f.tt.Key(chord, ui.KeyA)
	f.tt.flush()
	f.tt.Key(0, ui.KeyEnter)
	f.denied(k2, "Denied by user")
	f.wantAudit("echo k2", "denied", "")
}

func TestFlowShortcutOpensACollapsedColumn(t *testing.T) {
	needLib(t)
	f := flow(t)
	f.unlock()
	f.openBox()
	k3 := f.exec("k3", "echo k3")
	f.until(func() bool { return f.tt.HasText("echo k3") }, "k3 in the column")
	f.click("Close AI requests")
	f.tt.flush()
	if f.a.AIOpen() || f.tt.HasText("echo k3") {
		t.Fatal("column still open")
	}
	f.click("Terminal box")
	f.tt.Key(chord, ui.KeyA)
	f.tt.flush()
	if !f.a.AIOpen() || !f.tt.Focused("Reason (optional)") {
		t.Fatal("the shortcut must reopen the column and focus Reason")
	}
	f.tt.Key(0, ui.KeyEnter)
	f.denied(k3, "Denied by user")
	f.wantAudit("echo k3", "denied", "")
}
