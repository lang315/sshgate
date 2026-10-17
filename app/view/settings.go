package view

import (
	"context"
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/model"
)

// settingsState is the open Settings dialog (Electron's SettingsDialog.tsx):
// the soft-lock limit in force when it opened, the choice, Touch ID as it
// stood when it opened (touch) and the switch (touchOn, with the master
// password asked when it is being turned on), and Save's state. lc restarts
// Save's delay when the notice moves it.
type settingsState struct {
	limit, chosen string
	touch         model.TouchStatus
	touchOn       bool
	password      string
	pwH           ui.Handle // the password field: it takes the focus when it appears
	pwShown       bool
	busy          bool
	err           string
	lc            *model.ListChanges
}

// buildSettings is the dialog: Cancel has the focus, so Enter and Esc
// cancel (not while saving); Save is mouse-only, enabled once the choice
// differs and AllowDelay has passed since the dialog opened or its notice
// changed. Raising the limit lists the hosts on auto-allow, amber; No limit
// lists them red.
func (a *App) buildSettings(c *ui.Context) {
	st := a.settings
	if st == nil {
		return
	}
	t, now := c.Theme(), a.Now()
	hosts := model.AutoHostNames(a.servers, now)
	slices.Sort(hosts)
	change := model.LimitChange(st.limit, st.chosen)
	touch := model.TouchChange(st.touch.Enabled, st.touchOn)
	key := model.SettingsChangeKey(st.chosen, hosts, st.err, touch == "on")
	guard(c, &st.lc, key, now)
	answer := 0 // 1 save, 2 cancel
	labels, cur := make([]string, len(model.Limits)), ""
	for i, l := range model.Limits {
		labels[i] = l.Label
		if l.Value == st.chosen {
			cur = l.Label
		}
	}
	open := true
	m := ui.Modal(c.Key(fmt.Sprintf("settings-%p", st)), &open, func() { // keyed: Cancel takes the focus on each open
		ui.Column(c).Width(460).Gap(10).Padding(20).Children(func() {
			ui.Text(c, "Settings").Bold().FontSize(16)
			if st.touch.Supported {
				ui.Text(c, "Unlock").Bold()
				var sw ui.Element
				ui.Field(c, "Unlock with Touch ID", func() {
					sw = ui.Switch(c.Key("touch"), &st.touchOn).Disabled(st.busy || (!st.touch.Available && !st.touchOn))
				})
				if sw.Changed() {
					st.password = ""
				}
				if !st.touch.Available {
					ui.Text(c, "Touch ID is not available on this Mac right now.").TextColor(t.TextMuted)
				}
				if touch == "on" {
					pw := ui.TextInput(c.Key("touch-pw"), &st.password).Password().Label("Master password").Placeholder("Master password").Disabled(st.busy).Bind(&st.pwH)
					if !st.pwShown { // AutoFocus only works as a dialog opens
						st.pwShown = true
						st.pwH.Focus()
					}
					if pw.Submitted() {
						answer = 2 // Enter means Cancel here too: Save is mouse-only
					}
				} else {
					st.pwShown = false
				}
			}
			ui.Text(c, "Auto-allow").Bold()
			var sel ui.Element
			ui.Field(c, "Keep running while locked", func() { sel = ui.Select(c.Key("limit"), &cur, labels) })
			if sel.Changed() {
				for _, l := range model.Limits {
					if l.Label == cur {
						st.chosen = l.Value
					}
				}
			}
			ui.Text(c, model.LimitHelp(st.chosen)).TextColor(t.TextMuted)
			if change == "higher" || change == "none" {
				bg := t.Warning
				if change == "none" {
					bg = t.Danger
				}
				ui.Column(c).Gap(4).Padding(8, 12).Radius(6).Background(bg.Alpha(0.15)).Role(ui.RoleStatus).Children(func() {
					if len(hosts) == 0 {
						ui.Text(c, model.LimitNotice(st.chosen)+" No host is on auto-allow now. The limit applies from the next grant.")
						return
					}
					ui.Text(c, model.LimitNotice(st.chosen)+" On auto-allow now:")
					for _, h := range hosts {
						ui.Text(c, model.DisplayText(h)).Font("monospace")
					}
				})
			}
			if st.err != "" {
				ui.Text(c, model.DisplayText(st.err)).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if MouseButton(c, "Save", model.SaveAllowed(change, touch, st.password, st.busy, st.lc.At, now)) && model.SaveAllowed(change, touch, st.password, st.busy, st.lc.At, a.Now()) {
					answer = 1
				}
				if ui.PrimaryButton(c, "Cancel").AutoFocus().Disabled(st.busy).Clicked() {
					answer = 2
				}
			})
		})
	}).Label("Settings")
	if m.Dismissed() && !st.busy {
		answer = 2
	}
	switch answer {
	case 1:
		a.saveSettings(st)
	case 2:
		a.settings = nil
		a.focusActive()
	}
}

// saveSettings applies Touch ID first, then the limit if it changed, reads
// status, and closes; an error stays in the dialog, and a Touch ID error
// leaves the limit unsaved. The reply is dropped if its hub stopped or the
// ready screen went (a lock closes the dialog), and touches only this one.
func (a *App) saveSettings(st *settingsState) {
	st.busy, st.err = true, ""
	gen := a.hubGen
	limit, limitChanged := st.chosen, model.LimitChange(st.limit, st.chosen) != "same"
	touch, pw := model.TouchChange(st.touch.Enabled, st.touchOn), st.password
	go func() {
		var err error
		switch touch {
		case "on":
			err = a.Touch.Enable(context.Background(), pw)
		case "off":
			err = a.Touch.Disable()
		}
		touched := err == nil && touch != "same"
		if err == nil && limitChanged {
			err = a.hub().SetSoftLockLimit(context.Background(), limit)
		}
		a.Update(func() {
			if !a.ready(gen) || a.settings != st {
				return
			}
			st.busy = false
			if touched { // stands even if the limit failed: a retry then changes only the limit
				st.touch.Enabled, st.password = touch == "on", ""
				a.touch.Enabled = st.touch.Enabled
			}
			if err != nil {
				st.err = err.Error()
				return
			}
			a.settings = nil
			a.focusActive()
			a.refresh()
		})
	}()
}
