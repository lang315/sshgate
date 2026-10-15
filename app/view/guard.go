// Package view is the app's state and its native views.
package view

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/model"
)

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

// guard keeps *lc on key, so a change restarts its AllowDelay, and asks for
// a redraw when the delay ends; it reports whether the delay has passed.
func guard(c *ui.Context, lc **model.ListChanges, key string, now time.Time) bool {
	if *lc == nil {
		*lc = model.NewListChanges(key, now)
	}
	(*lc).SetKey(key, now)
	ok := model.TrustEnabled((*lc).At, now)
	if !ok {
		c.After((*lc).At.Add(model.AllowDelay).Sub(now))
	}
	return ok
}
