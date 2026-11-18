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
	return mouseButtonNamed(c, label, label, enabled)
}

// mouseButtonNamed is MouseButton showing text, named name: the delete
// dialog's "Delete" is "Delete for good", apart from the toolbar's Delete.
func mouseButtonNamed(c *ui.Context, text, name string, enabled bool) bool {
	t := c.Theme()
	b := ui.Row(c).Role(ui.RoleButton).Label(name).Padding(6, 12).Radius(6).Border(1, t.Border).Disabled(!enabled)
	b.Children(func() { ui.Text(c, text) })
	return enabled && b.Clicked()
}

// tone is what a decision button shows once enabled; disabled, every tone
// looks like a plain MouseButton.
type tone int

const (
	toneNeutral tone = iota
	toneOk
	toneAccent
	toneDanger
	toneDangerFill
)

// decisionButton is mouseButtonNamed that takes its tone only once enabled:
// a border and text in the tone's colour, or for toneDangerFill a filled
// Deny-style button.
func decisionButton(c *ui.Context, text, name string, enabled bool, t tone) bool {
	if !enabled || t == toneNeutral {
		return mouseButtonNamed(c, text, name, enabled)
	}
	p := tok(c)
	b := ui.Row(c).Role(ui.RoleButton).Label(name).Padding(6, 12).Radius(radiusControl)
	if t == toneDangerFill {
		fill := p.DangerFill
		if b.Pressed() {
			fill = p.DangerFillPressed
		} else if b.Hovered() {
			fill = p.DangerFillHover
		}
		b.Border(1, fill).Background(fill).Children(func() { ui.Text(c, text).TextColor(p.DangerText) })
		return b.Clicked()
	}
	col := map[tone]ui.Color{toneOk: p.Ok, toneAccent: p.Accent, toneDanger: p.Danger}[t]
	b.Border(1, col).Children(func() { ui.Text(c, text).TextColor(col) })
	return b.Clicked()
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
