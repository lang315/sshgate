package view

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/model"
	"github.com/lang315/sshgate/app/touchid"
)

// revealHide is how long a shown secret stays on screen.
const revealHide = 15 * time.Second

// revealState is the Show dialog for one saved secret of the open host
// (spec 2026-11-22-reveal-secret-design.md), then the value it got, shown
// from at until revealHide passes. It lives in editorState, so closing the
// editor, a lock or a hub stop drops it with the editor.
type revealState struct {
	field    model.Secret
	pw       string // the typed master password; goes with the dialog
	busy     bool   // a typed reveal is at the hub
	touching bool   // a Touch ID sheet is up (or its reveal is at the hub)
	seq      int    // bumped per call; an answer for an older call is dropped
	err      string
	value    string
	at       time.Time
}

// endReveal drops the open editor's reveal, closing a Touch ID sheet still
// up: left open it would hold the manager's one helper slot, and a late touch
// would audit a showing nobody sees.
func (a *App) endReveal() {
	ed := a.editor
	if ed == nil || ed.reveal == nil {
		return
	}
	if ed.reveal.touching && a.Touch != nil {
		a.Touch.Cancel(nil)
	}
	ed.reveal = nil
}

// askReveal opens the Show dialog for f; with Touch ID on it asks Touch ID
// once by itself.
func (a *App) askReveal(f model.Secret) {
	a.editor.reveal = &revealState{field: f}
	if a.touchOn() {
		a.revealTouch()
	}
}

// touchOn is Touch ID turned on, as the lock screen last read it.
func (a *App) touchOn() bool { return a.Touch != nil && a.touch != nil && a.touch.Enabled }

// revealTouch is one Touch ID reveal: the manager sends the unsealed master
// password to the hub itself, so only the value comes back here.
func (a *App) revealTouch() {
	ed := a.editor
	r := ed.reveal
	if r == nil || r.busy || r.touching {
		return
	}
	r.touching, r.err = true, ""
	name, field := ed.name, r.field.Wire()
	a.revealDo(ed, r, true, func() (string, error) { return a.Touch.Reveal(context.Background(), name, field) })
}

// revealTyped sends the typed master password; the field is emptied at once.
// A Touch ID sheet still up is closed: the typed password won.
func (a *App) revealTyped() {
	ed := a.editor
	r := ed.reveal
	if r == nil || r.busy || r.pw == "" {
		return
	}
	if r.touching {
		a.Touch.Cancel(nil)
		r.touching = false
	}
	r.busy, r.err = true, ""
	name, field, pw := ed.name, r.field.Wire(), r.pw
	r.pw = ""
	a.revealDo(ed, r, false, func() (string, error) { return a.hub().ServersReveal(context.Background(), name, field, pw) })
}

// revealDo runs call off the UI thread; its answer is dropped when the hub
// stopped, the vault locked, the editor closed, the dialog was cancelled or a
// later call replaced it.
func (a *App) revealDo(ed *editorState, r *revealState, touch bool, call func() (string, error)) {
	gen := a.hubGen
	r.seq++
	seq := r.seq
	go func() {
		v, err := call()
		a.Update(func() {
			if !a.ready(gen) || a.editor != ed || ed.reveal != r || r.seq != seq {
				return
			}
			if touch {
				r.touching = false
			} else {
				r.busy = false
			}
			switch {
			case err == nil:
				r.value, r.at = v, a.Now()
			case errors.Is(err, touchid.ErrCancelled), errors.Is(err, touchid.ErrOff):
				// the password field stays the way in
			case errors.Is(err, touchid.ErrUnavailable):
				r.err = "Touch ID is not available right now. Type the master password."
			case errors.Is(err, touchid.ErrReset):
				r.err = "Touch ID was turned off because your fingerprints changed or your master password is different. Type the master password, and turn Touch ID on again in Settings."
				a.loadTouch()
			default:
				r.err = err.Error()
				if a.Touch != nil {
					a.loadTouch() // a reset deleted touchid.json
				}
			}
		})
	}()
}

// buildRevealValue draws f's shown value under its field, read-only and not
// selectable (no clipboard), until revealHide passes.
func (a *App) buildRevealValue(c *ui.Context, f model.Secret) {
	ed := a.editor
	r := ed.reveal
	if r == nil || r.field != f || r.value == "" {
		return
	}
	now := a.Now()
	left := r.at.Add(revealHide).Sub(now)
	if left <= 0 {
		ed.reveal = nil
		return
	}
	c.After(left)
	ui.Text(c.Key("reveal-value"), model.DisplayText(r.value)).Font("monospace") // hidden and bidi runes show as \uXXXX
}

// buildRevealDialog asks for the master password (or Touch ID) before a
// secret is shown. Esc, a backdrop click and Cancel drop it.
func (a *App) buildRevealDialog(c *ui.Context) {
	t := c.Theme()
	ed := a.editor
	r := ed.reveal
	if r == nil || r.value != "" {
		return
	}
	submit, cancel := false, false
	open := true
	m := modal(c.Key("reveal"), &open, func() {
		ui.Column(c).Width(420).Gap(12).Children(func() {
			ui.Text(c, "Show the saved "+strings.ToLower(r.field.Label())+" of "+model.DisplayText(ed.name)+"? Type the master password.")
			ui.Field(c, "Master password", func() {
				in := ui.TextInput(c.Key("reveal-pw"), &r.pw).Password().AutoFocus()
				if in.Submitted() {
					submit = true
				}
			})
			if r.err != "" {
				ui.Text(c, model.DisplayText(r.err)).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if a.touchOn() && ui.Button(c, "Use Touch ID").Disabled(r.busy || r.touching).Clicked() {
					a.revealTouch()
				}
				if ui.Button(c, "Cancel").Clicked() {
					cancel = true
				}
				if ui.PrimaryButton(c, "Show").Disabled(r.busy || r.pw == "").Clicked() {
					submit = true
				}
			})
		})
	}).Label("Show saved secret")
	switch {
	case cancel || m.Dismissed():
		a.endReveal()
	case submit:
		a.revealTyped()
	}
}
