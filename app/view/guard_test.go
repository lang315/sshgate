package view

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestGuardRestartsOnChange(t *testing.T) {
	var g Guard
	t0 := time.Unix(0, 0)
	g.Note("a", t0)
	if g.Ready(t0.Add(499 * time.Millisecond)) {
		t.Fatal("ready before 500 ms")
	}
	g.Note("a", t0.Add(400*time.Millisecond)) // same key: no restart
	if !g.Ready(t0.Add(500 * time.Millisecond)) {
		t.Fatal("not ready at 500 ms")
	}
	g.Note("b", t0.Add(600*time.Millisecond)) // changed: restart
	if g.Ready(t0.Add(1000 * time.Millisecond)) {
		t.Fatal("ready 400 ms after a change")
	}
	if r := g.Remaining(t0.Add(1000 * time.Millisecond)); r != 100*time.Millisecond {
		t.Fatalf("remaining %s", r)
	}
}

func TestGuardNeverNotedIsNotReady(t *testing.T) {
	var g Guard
	if g.Ready(time.Unix(0, 0)) || g.Ready(time.Unix(1<<40, 0)) {
		t.Fatal("a Guard that never saw Note is ready")
	}
}

// guardedView shows one guarded mouse-only button, as the approval column will.
type guardedView struct {
	g       Guard
	now     time.Time
	key     string
	clicked int
}

func (v *guardedView) build(c *ui.Context) {
	v.g.Note(v.key, v.now)
	ready := v.g.Ready(v.now)
	if !ready {
		c.After(v.g.Remaining(v.now))
	}
	if MouseButton(c, "Allow", ready) {
		v.clicked++
	}
	ui.TextInput(c, new(string)).Label("Reason").AutoFocus()
}

func TestMouseButtonWaitsAndNeverTakesKeys(t *testing.T) {
	v := &guardedView{now: time.Unix(0, 0), key: "r1"}
	tt := ui.NewTester(v.build, 400, 200)
	_ = tt.Click("Allow")
	if v.clicked != 0 {
		t.Fatal("clicked before 500 ms")
	}
	v.now = v.now.Add(500 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Allow"); err != nil {
		t.Fatal(err)
	}
	if v.clicked != 1 {
		t.Fatalf("clicked %d", v.clicked)
	}
	// The keyboard never reaches it: Tab cycles to the reason field only,
	// and Enter or Space there do not press Allow.
	for range 4 {
		tt.Key(0, ui.KeyTab)
		if tt.Focused("Allow") {
			t.Fatal("Allow took the focus")
		}
	}
	tt.Key(0, ui.KeyEnter)
	tt.Key(0, ui.KeySpace)
	if v.clicked != 1 {
		t.Fatalf("a key pressed Allow: clicked %d", v.clicked)
	}
	// A change of what is shown restarts the delay.
	v.key = "r1,r2"
	tt.Frame()
	_ = tt.Click("Allow")
	if v.clicked != 1 {
		t.Fatal("clicked right after the list changed")
	}
}
