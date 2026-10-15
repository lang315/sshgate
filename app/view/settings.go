package view

import (
	"context"
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/model"
)

// settingsState is the open Settings dialog (Electron's SettingsDialog.tsx):
// the soft-lock limit in force when it opened, the choice, and Save's state.
// lc restarts Save's delay when the notice moves it.
type settingsState struct {
	limit, chosen string
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
	key := model.SettingsChangeKey(st.chosen, hosts, st.err)
	if st.lc == nil {
		st.lc = model.NewListChanges(key, now)
	}
	st.lc.SetKey(key, now)
	if wait := model.DelayRemaining(st.lc.At, st.lc.At, now); wait > 0 {
		c.After(wait)
	}
	labels, cur := make([]string, len(model.Limits)), ""
	for i, l := range model.Limits {
		labels[i] = l.Label
		if l.Value == st.chosen {
			cur = l.Label
		}
	}
	answer := 0 // 1 save, 2 cancel
	open := true
	m := ui.Modal(c.Key(fmt.Sprintf("settings-%p", st)), &open, func() { // keyed: Cancel takes the focus on each open
		ui.Column(c).Width(460).Gap(10).Padding(20).Children(func() {
			ui.Text(c, "Settings").Bold().FontSize(16)
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
				if MouseButton(c, "Save", model.SaveAllowed(change, st.busy, st.lc.At, now)) && model.SaveAllowed(change, st.busy, st.lc.At, a.Now()) {
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

// saveSettings stores the chosen limit, reads status for it, and closes; an
// error stays in the dialog. The reply is dropped if its hub stopped or the
// ready screen went (a lock closes the dialog), and touches only this one.
func (a *App) saveSettings(st *settingsState) {
	st.busy, st.err = true, ""
	gen := a.hubGen
	go func() {
		err := a.hub().SetSoftLockLimit(context.Background(), st.chosen)
		a.Update(func() {
			if !a.ready(gen) || a.settings != st {
				return
			}
			st.busy = false
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
