package view

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// confirmState is the Auto-allow dialog over the editor (Electron's
// AutoAllowDialog.tsx): what SaveConfirm asked, the refusal shown instead
// of the warnings, the root check's answer, the typed name, and Enable's
// last error. lc restarts Enable's delay when what it shows moves.
type confirmState struct {
	ask       model.Confirm
	refused   string
	check     *hubapi.AutoCheck
	checkFail bool
	typed     string
	err       string
	lc        *model.ListChanges
}

// openConfirm shows the dialog and, unless the host is refused, runs the
// hub's root check once; its reply touches only this dialog, while its hub
// runs and the ready screen shows.
func (a *App) openConfirm(s *hubapi.Server) {
	ed := a.editor
	cf := &confirmState{ask: model.SaveConfirm(s, ed.draft), refused: model.DraftRefusal(s, ed.draft)}
	ed.confirm = cf
	if cf.refused != "" {
		return
	}
	gen := a.hubGen
	go func() {
		r, err := a.hub().AutoAllowCheck(context.Background(), ed.name)
		a.Update(func() {
			if !a.ready(gen) || ed.confirm != cf {
				return
			}
			if err != nil {
				cf.checkFail = true
			} else {
				cf.check = &r
			}
		})
	}()
}

// buildConfirm is the dialog: Cancel has the focus, so Enter and Esc
// cancel (the typed-name field's Enter too); Enable is mouse-only and waits
// AllowDelay after the dialog opens or anything it shows changes.
func (a *App) buildConfirm(c *ui.Context) {
	t := c.Theme()
	ed, now := a.editor, a.Now()
	cf, d := ed.confirm, ed.draft
	key := model.DialogChangeKey(d.AutoAllow, cf.ask.RootNew, cf.ask.SudoNew, d.AutoAllowSudo, cf.check, cf.checkFail, cf.err)
	if cf.lc == nil {
		cf.lc = model.NewListChanges(key, now)
	}
	cf.lc.SetKey(key, now)
	enabled := func(now time.Time) bool {
		return !ed.busy && model.EnableAllowed(cf.ask.TypeName, cf.typed, ed.name, cf.refused, cf.lc.At, now)
	}
	if wait := model.DelayRemaining(cf.lc.At, cf.lc.At, now); wait > 0 {
		c.After(wait)
	}
	name := model.DisplayText(ed.name)
	title := "Auto-allow AI commands on " + name
	answer := 0 // 1 enable, 2 cancel
	open := true
	m := ui.Modal(c.Key(fmt.Sprintf("auto-%p", cf)), &open, func() { // keyed: Cancel takes the focus on each open
		ui.Column(c).Width(460).Gap(10).Padding(20).Children(func() {
			ui.Text(c, title).Bold().FontSize(16)
			if cf.refused != "" {
				ui.Text(c, "Auto-allow is not available here: "+model.DisplayText(cf.refused)).TextColor(t.Danger)
			} else {
				a.buildConfirmBody(c, cf, d, name, now, &answer)
			}
			if cf.err != "" {
				ui.Text(c, model.DisplayText(cf.err)).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if MouseButton(c, "Enable", enabled(now)) && enabled(a.Now()) {
					answer = 1
				}
				if ui.PrimaryButton(c, "Cancel").AutoFocus().Clicked() {
					answer = 2
				}
			})
		})
	}).Label(title)
	if m.Dismissed() {
		answer = 2
	}
	switch answer {
	case 1:
		a.saveHost()
	case 2:
		ed.confirm = nil // back to the editor, the draft kept
	}
}

// buildConfirmBody is the dialog's consent text, top to bottom as Electron's.
func (a *App) buildConfirmBody(c *ui.Context, cf *confirmState, d model.HostDraft, name string, now time.Time, answer *int) {
	t := c.Theme()
	ui.Text(c, "The AI runs any command this account can run on "+name+" without asking, including commands planted by what it reads (prompt injection), and can leave things that run later (cron jobs, SSH keys, shell startup files).")
	if !d.AutoAllowSudo {
		ui.Text(c, "sudo-exec still asks.")
	}
	if cf.ask.RootNew {
		ui.Text(c, "Allowing root: the AI runs as root, and a command it plants can capture sudo or su passwords you type or store").TextColor(t.Danger)
	}
	if cf.ask.SudoNew {
		ui.Text(c, "sudo-exec will run without asking: the AI has full root on this host").TextColor(t.Danger)
	}
	if d.AutoAllowSudo && !cf.ask.SudoNew {
		ui.Text(c, "sudo-exec also runs without asking on this host, the same as plain exec.")
	}
	if cf.check != nil && (cf.check.UID == 0 || cf.check.PasswordlessSudo) {
		ui.Text(c, "This is root access: the AI can do anything on this host.").TextColor(t.Danger)
	}
	if cf.checkFail {
		ui.Text(c, "Could not check this host's sudo access.").TextColor(t.TextMuted)
	}
	m, _ := model.AutoModeOf(d.AutoAllow)
	if m.Mode == "forever" {
		limit := ""
		if a.status != nil {
			limit = a.status.SoftLockLimit
		}
		ui.Text(c, "Until turned off").Bold()
		ui.Text(c, model.LimitText(limit)).TextColor(t.TextMuted)
	} else {
		ends := now.Add(m.Dur).Local().Format("15:04")
		c.After(now.Truncate(time.Minute).Add(time.Minute).Sub(now)) // the end time moves with the clock
		ui.Text(c, fmt.Sprintf("For %s (ends at %s)", m.Label, ends)).Bold()
		ui.Text(c, "Ends at "+ends+" or when you stop it. Locking the app, by hand or from inactivity, does not stop it: the AI keeps running on this host until then. To stop it, use Stop auto-allow and lock on the unlock screen, Stop on the host card, or Stop all auto-allow.").TextColor(t.TextMuted)
	}
	if cf.ask.TypeName {
		var in ui.Element
		ui.Field(c, "Type "+name+" to confirm", func() { in = ui.TextInput(c.Key("typed"), &cf.typed) })
		if in.Submitted() {
			*answer = 2 // Enter cancels, as everywhere in the dialog
		}
	}
}
