package view

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/model"
)

// guardedView shows one guarded mouse-only button, as the approval column will.
type guardedView struct {
	lc      *model.ListChanges
	now     time.Time
	key     string
	clicked int
}

func (v *guardedView) build(c *ui.Context) {
	if v.lc == nil {
		v.lc = model.NewListChanges(v.key, v.now)
	}
	v.lc.SetKey(v.key, v.now)
	wait := model.DelayRemaining(v.lc.At, v.lc.At, v.now)
	ready := wait == 0
	if !ready {
		c.After(wait)
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
