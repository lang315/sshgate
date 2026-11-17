package view

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// modal is ui.Modal with the app's dialog look (Surface, radiusDialog,
// raisedShadow) and a 160 ms fade and 0.98 to 1 scale entrance. Callers key c
// as they would for ui.Modal.
func modal(c *ui.Context, open *bool, fn func()) ui.Element {
	// Esc or a backdrop click clears *open during Modal; that frame still
	// draws the panel, so it keeps the app's look.
	wasOpen := *open
	panel := ui.Modal(c, open, fn)
	if !wasOpen {
		return panel
	}
	panel.Background(tok(c).Surface).Radius(radiusDialog)
	raisedShadow(c).On(panel)
	enter(c, panel)
	return panel
}

// enter is the dialogs' and sheets' entrance: a 160 ms fade and 0.98 to 1
// scale from the centre, nothing under Reduce Motion. Call it once per
// opening on an element that is a new mount each time.
func enter(c *ui.Context, e ui.Element) {
	if c.Preferences().ReduceMotion {
		return
	}
	// Two builds at the start pose, not one: a pose that follows a frame the
	// element did not pose in is taken at once, and the first build after it
	// opens is followed by such a frame (no animation otherwise).
	entered := ui.Local(e, "entered", func() int { return 0 })
	p, a := ui.Pose{}, ui.Animation{Duration: 160 * time.Millisecond}
	if *entered < 2 {
		p = ui.Pose{Scale: 0.98, Fade: 1}
		*entered++
		c.Invalidate()
	}
	e.TransformOrigin(0.5, 0.5).AnimatePose(p, a)
}
