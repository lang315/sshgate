package view

import (
	"image"
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

// toneView shows one decision button of one tone, enabled or not.
type toneView struct {
	t       tone
	enabled bool
}

func (v *toneView) build(c *ui.Context) {
	ui.Column(c).Padding(20).Children(func() { decisionButton(c, "Go", "Go", v.enabled, v.t) })
}

func toneShot(t *testing.T, v *toneView, dark bool) (*image.RGBA, ui.Rect) {
	tt := ui.NewTester(v.build, 300, 120)
	tt.SetDark(dark)
	tt.Frame()
	for _, n := range tt.Nodes() {
		if n.Label == "Go" {
			return shot(&tester{Tester: tt}), n.Bounds
		}
	}
	t.Fatal("no Go button")
	return nil, ui.Rect{}
}

func TestDecisionButtonDisabledLooksNeutral(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	for _, dark := range []bool{false, true} {
		ref, r := toneShot(t, &toneView{t: toneNeutral}, dark)
		for _, tn := range []tone{toneOk, toneAccent, toneDanger, toneDangerFill} {
			img, rr := toneShot(t, &toneView{t: tn}, dark)
			if rr != r || !sameRegion(img, ref, grow(r, 2)) {
				t.Errorf("tone %d disabled differs from neutral (dark=%v)", tn, dark)
			}
		}
	}
}

func TestDecisionButtonTones(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	for _, dark := range []bool{false, true} {
		p := palette(dark)
		for tn, col := range map[tone]ui.Color{toneOk: p.Ok, toneAccent: p.Accent, toneDanger: p.Danger} {
			img, r := toneShot(t, &toneView{t: tn, enabled: true}, dark)
			if n := countNear(img, grow(r, 1), col, 20); n < 10 {
				t.Errorf("tone %d dark=%v: %d border pixels", tn, dark, n)
			}
		}
		img, r := toneShot(t, &toneView{t: toneDangerFill, enabled: true}, dark)
		patch := ui.Rect{X: r.X + 2, Y: r.Y + 2, W: 6, H: 4}
		if n := countNear(img, patch, p.DangerFill, 3); n != 24 {
			t.Errorf("dangerFill dark=%v: %d of 24 pixels", dark, n)
		}
	}
}
