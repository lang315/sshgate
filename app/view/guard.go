// Package view is the app's state and its native views.
package view

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// AllowDelay is how long a decisive button stays disabled after what it
// decides on changed, so nothing that moves under the pointer is pressed
// by a click meant for something else.
const AllowDelay = 500 * time.Millisecond

// Guard tracks when what a decision shows last changed. Its key is
// everything that can move a decisive button: the ids shown, the list's
// height, its scroll offset, and whether it just appeared.
type Guard struct {
	key   string
	since time.Time
}

// Note records the current key; a different key restarts the delay.
func (g *Guard) Note(key string, now time.Time) {
	if key != g.key || g.since.IsZero() {
		g.key, g.since = key, now
	}
}

// Ready is false until Note has run once: a button not yet guarded is not enabled.
func (g *Guard) Ready(now time.Time) bool {
	return !g.since.IsZero() && now.Sub(g.since) >= AllowDelay
}

func (g *Guard) Remaining(now time.Time) time.Duration { return max(0, AllowDelay-now.Sub(g.since)) }

// MouseButton is a button only the pointer presses: a ui.Button takes the
// focus and Enter or Space press it, so a decisive button is a row that
// never takes the focus. It reports a click only while enabled; disabled
// is set before the click is read.
func MouseButton(c *ui.Context, label string, enabled bool) bool {
	t := c.Theme()
	b := ui.Row(c).Role(ui.RoleButton).Label(label).Padding(6, 12).Radius(6).Border(1, t.Border).Disabled(!enabled)
	b.Children(func() { ui.Text(c, label) })
	return enabled && b.Clicked()
}
