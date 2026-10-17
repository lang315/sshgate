package view

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/model"
)

func card(c *ui.Context, body func()) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Children(func() {
		ui.Column(c).Width(420).Gap(12).Padding(24).Radius(12).Border(1, t.Border).Background(t.Surface).Children(body)
	})
}

func (a *App) buildHub(c *ui.Context) {
	t := c.Theme()
	card(c, func() {
		switch a.hubState.Kind {
		case "restarting":
			left := int(math.Max(0, math.Round(float64(a.hubSince.Add(a.hubState.In).Sub(a.Now()))/float64(time.Second))))
			ui.Text(c, fmt.Sprintf("Hub crashed. Restarting in %d s (attempt %d).", left, a.hubState.Attempt)).Bold()
			ui.Text(c, "The vault will be locked again after the restart. Open terminals will end.").TextColor(t.TextMuted)
			c.After(time.Second)
		case "failed":
			ui.Text(c, "The hub stopped").Bold()
			ui.Text(c, model.DisplayText(a.hubState.Message))
			for _, line := range strings.Split(a.hubState.Stderr, "\n") {
				if line != "" {
					ui.Text(c, model.DisplayText(line)).Font("monospace").TextColor(t.TextMuted)
				}
			}
		default:
			ui.Text(c, "Starting the hub…")
		}
	})
}

func check(c *ui.Context, ok bool, label string) {
	t := c.Theme()
	if ok {
		ui.Text(c, label).TextColor(t.Success)
		return
	}
	ui.Text(c, label+" (not yet)").TextColor(t.TextMuted)
}

func (a *App) buildCreateVault(c *ui.Context) {
	t := c.Theme()
	length, match := model.PasswordChecks(a.newPw, a.again)
	ok := length && match
	card(c, func() {
		ui.Text(c, "Create your vault").Bold().FontSize(18)
		ui.Text(c, "The master password encrypts saved passwords and protects the host list.").TextColor(t.TextMuted)
		ui.TextInput(c.Key("cv-pw"), &a.newPw).Password().Label("New master password").Placeholder("New master password").AutoFocus()
		again := ui.TextInput(c.Key("cv-again"), &a.again).Password().Label("Confirm master password").Placeholder("Confirm master password")
		check(c, length, "At least 8 characters")
		check(c, match, "Both passwords match")
		ui.Text(c, "The master password cannot be recovered. If you forget it, this vault cannot be unlocked and its saved passwords are lost.").TextColor(t.Warning)
		if len(a.servers) > 0 {
			ui.Text(c, "These servers from your existing file are kept, with Visible to AI turned off and their host keys unpinned; you'll confirm each key on the next connect.").TextColor(t.TextMuted)
			for _, s := range a.servers {
				ui.Text(c, model.DisplayText(s.Name)+"  "+model.DisplayText(model.Addr(s))).Font("monospace")
			}
		}
		if a.createErr != "" {
			ui.Text(c, model.DisplayText(a.createErr)).TextColor(t.Danger)
		}
		if (ui.PrimaryButton(c, "Create vault").Disabled(a.busy || !ok).Clicked() || again.Submitted()) && ok {
			a.createVault(a.newPw)
		}
	})
}

func (a *App) buildUnlock(c *ui.Context) {
	t := c.Theme()
	card(c, func() {
		ui.Text(c, "Unlock vault").Bold().FontSize(18)
		if a.lockReason == "idle" {
			ui.Text(c, "Locked after inactivity.").TextColor(t.Warning)
		}
		if a.lockReason != "" && len(a.lockHosts) == 0 {
			ui.Text(c, "Open terminals stay connected while locked. AI requests are refused until you unlock.").TextColor(t.TextMuted)
		}
		if len(a.lockHosts) > 0 {
			names := make([]string, len(a.lockHosts))
			for i, h := range a.lockHosts {
				names[i] = model.DisplayText(h)
			}
			ui.Row(c).Gap(8).Padding(8).Radius(8).Background(t.Warning.Alpha(0.15)).Role(ui.RoleStatus).Children(func() {
				ui.Text(c, "AI auto-allow is still running on: "+strings.Join(names, ", ")).Grow(1)
				if ui.Button(c, "Stop auto-allow and lock").Clicked() {
					a.stopAndLock()
				}
			})
		}
		if ts := a.touch; ts != nil && ts.Enabled && ts.Available {
			if ui.Button(c, "Unlock with Touch ID").Disabled(a.busy || a.touchBusy).Clicked() {
				a.touchUnlock()
			}
		}
		in := ui.TextInput(c.Key("unlock-pw"), &a.password).Password().Label("Master password").Placeholder("Master password").AutoFocus()
		if a.unlockErr != "" {
			ui.Text(c, model.DisplayText(a.unlockErr)).TextColor(t.Danger)
		}
		if (ui.PrimaryButton(c, "Unlock").Disabled(a.busy || a.password == "").Clicked() || in.Submitted()) && a.password != "" {
			a.unlock(a.password)
		}
		// Inside the card: the filling, centred column leaves no room below it.
		if a.status != nil && a.status.StorePath != "" {
			ui.Text(c, "Vault file "+model.DisplayText(a.status.StorePath)).TextColor(t.TextMuted)
		}
	})
}
