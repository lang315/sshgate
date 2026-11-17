package view

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// modal is ui.Modal with the app's dialog look (Surface, radiusDialog,
// raisedShadow) and a 160 ms fade and 0.98 to 1 scale entrance. Callers key c
// as they would for ui.Modal.
func modal(c *ui.Context, open *bool, fn func()) ui.Element {
	panel := ui.Modal(c, open, fn)
	if !*open {
		return panel
	}
	panel.Background(tok(c).Surface).Radius(radiusDialog)
	raisedShadow(c).On(panel)
	if c.Preferences().ReduceMotion {
		return panel
	}
	// Two builds at the start pose, not one: a pose that follows a frame the
	// panel did not pose in is taken at once, and the first build after the
	// dialog opens is followed by such a frame (no animation otherwise).
	entered := ui.Local(panel, "entered", func() int { return 0 })
	p, a := ui.Pose{}, ui.Animation{Duration: 160 * time.Millisecond}
	if *entered < 2 {
		p = ui.Pose{Scale: 0.98, Fade: 1}
		*entered++
		c.Invalidate()
	}
	panel.AnimatePose(p, a)
	return panel
}
