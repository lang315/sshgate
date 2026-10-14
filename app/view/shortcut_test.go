package view

import (
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestApprovalsChord(t *testing.T) {
	for goos, want := range map[string]ui.Modifiers{"darwin": ui.Super | ui.Shift, "linux": ui.Ctrl | ui.Shift, "windows": ui.Ctrl | ui.Shift} {
		if got := approvalsChord(goos); got != want {
			t.Errorf("%s: %v, want %v", goos, got, want)
		}
	}
}

// chordApp is sendApp with box's tab t1 open and focused.
func chordApp(t *testing.T) (*App, *tester, *fakeDoor, *hubDoor) {
	needLib(t)
	a, tt, d, hd, _ := sendApp(t)
	a.Update(func() { a.openTab("box") })
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" && tt.Focused("Terminal box") }, "t1 open and focused")
	return a, tt, d, hd
}

var chord = approvalsChord(runtime.GOOS)

func TestShortcutFocusesTheOldestReasonAndEscReturns(t *testing.T) {
	a, tt, _, hd := chordApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r2", "box", "echo r2")})
	if !tt.Focused("Terminal box") {
		t.Fatal("an arriving request took the focus")
	}
	tt.Key(chord, ui.KeyA)
	tt.flush()
	tt.Type("why")
	tt.flush()
	if *a.col.reasons["r1"] != "why" || *a.col.reasons["r2"] != "" {
		t.Fatal("the shortcut must focus the oldest request's reason")
	}
	tt.Key(0, ui.KeyEscape)
	tt.flush()
	if !tt.Focused("Terminal box") {
		t.Fatal("Esc did not return to the terminal")
	}
	tt.Type("g")
	tt.flush()
	hd.waitData(func(s string) bool { return s == "g" })
	if got := hd.dataFor("t1"); got != "g" {
		t.Fatalf("the remote got %q: the chord and Esc must send nothing", got)
	}
	tt.KeyAt(chord, ui.KeyA) // a layout that does not type A: matched by position
	tt.flush()
	if tt.Focused("Terminal box") {
		t.Fatal("the chord by key position did not leave the terminal")
	}
	tt.Key(0, ui.KeyEscape)
	tt.flush()
	other := ui.Ctrl | ui.Shift
	if chord == other {
		other = ui.Super | ui.Shift
	}
	for _, m := range []ui.Modifiers{chord | ui.Alt, chord &^ ui.Shift, other} {
		tt.Key(m, ui.KeyA)
		tt.flush()
		if !tt.Focused("Terminal box") {
			t.Fatalf("mods %v left the terminal", m)
		}
	}
}

func TestShortcutOpensACollapsedColumn(t *testing.T) {
	a, tt, d, _ := chordApp(t)
	click(t, tt, "Close AI requests")
	if a.AIOpen() || !tt.Focused("Terminal box") {
		t.Fatal("closing the column must leave the focus in the terminal")
	}
	tt.Key(chord, ui.KeyA)
	tt.flush()
	if !a.AIOpen() || !tt.Focused("Reason (optional)") {
		t.Fatal("the shortcut must open the column and focus Reason")
	}
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if decided(d) != `{"id":"r1","outcome":"denied","reason":""}` {
		t.Fatal(decided(d))
	}
}

func TestTabKeyReachesTheShell(t *testing.T) {
	_, tt, _, hd := chordApp(t)
	tt.Key(0, ui.KeyTab)
	tt.flush()
	hd.waitData(func(s string) bool { return s != "" })
	if got := hd.dataFor("t1"); got != "\t" {
		t.Fatalf("the remote got %q", got)
	}
}

func TestShortcutOnlyFromATerminal(t *testing.T) {
	a, tt, _, _ := colApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "ls")})
	click(t, tt, "Search hosts")
	tt.Key(chord, ui.KeyA)
	tt.flush()
	if !tt.Focused("Search hosts") {
		t.Fatal("the chord moved the focus outside a terminal")
	}
	click(t, tt, "Reason (optional)")
	tt.Key(0, ui.KeyEscape) // no terminal tab: the focus stays
	tt.flush()
	if !tt.Focused("Reason (optional)") {
		t.Fatal("Esc with no terminal lost the focus")
	}
}
