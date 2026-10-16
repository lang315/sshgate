package view

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// hkState restarts the Trust delay when a prompt shows (again): a new
// prompt, or the same one after the lock screen hid it.
type hkState struct {
	lc      *model.ListChanges
	mounted bool
}

// keyAddr is the user@host:port a host-key result names.
func keyAddr(k hubapi.HostKey) string { return fmt.Sprintf("%s@%s:%d", k.User, k.Host, k.Port) }

// buildHostKeyDialogs shows a host-key mismatch, else the oldest unknown
// key (Electron's HostKeyDialog.tsx). Only the ready screen calls it.
func (a *App) buildHostKeyDialogs(c *ui.Context) {
	if a.mismatch != nil {
		a.hk.mounted = false // a prompt it hides shows again with a fresh Trust delay
		a.buildMismatch(c, *a.mismatch)
		return
	}
	p, ok := a.prompts.Current()
	if !ok {
		a.hk.mounted = false
		return
	}
	t := c.Theme()
	now := a.Now()
	key := model.PromptKey(p, ok)
	if !a.hk.mounted || a.hk.lc == nil {
		a.hk.lc, a.hk.mounted = model.NewListChanges(key, now), true
	}
	a.hk.lc.SetKey(key, now)
	trust := model.TrustEnabled(a.hk.lc.At, now)
	if !trust {
		c.After(a.hk.lc.At.Add(model.AllowDelay).Sub(now))
	}
	k := p.Key
	answer := 0 // 1 trust, 2 cancel
	open := true
	m := ui.Modal(c.Key("hk-"+key), &open, func() { // keyed: Cancel takes the focus again for the next prompt
		ui.Column(c).Width(460).Gap(10).Padding(20).Children(func() {
			ui.Text(c, "Trust this host key?").Bold().FontSize(16)
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, model.DisplayText(k.Server)).Bold()
				ui.Text(c, model.DisplayText(keyAddr(k))).Font("monospace").TextColor(t.TextMuted)
			})
			ui.Text(c, "No host key is pinned for this server yet. Check the fingerprint against one you got another way, from the server's console or its admin.")
			ui.Text(c, model.DisplayText(k.KeyType)+" fingerprint").TextColor(t.TextMuted)
			ui.Text(c, model.DisplayText(k.Fingerprint)).Font("monospace").Selectable()
			switch k.KnownHosts {
			case "match":
				ui.Text(c, "Your ~/.ssh/known_hosts lists this same key for this host.").TextColor(t.TextMuted)
			case "absent":
				ui.Text(c, "This host is not in your ~/.ssh/known_hosts.").TextColor(t.TextMuted)
			case "different":
				ui.Text(c, "Warning: your ~/.ssh/known_hosts lists a different key for this host. The connection may be intercepted.").TextColor(t.Danger)
			}
			ui.Text(c, "Trust only if this matches the key you expect. Once trusted it is pinned, and a different key later is refused.").TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if MouseButton(c, "Trust and connect", trust) && model.TrustEnabled(a.hk.lc.At, a.Now()) {
					answer = 1
				}
				if ui.Button(c, "Cancel").AutoFocus().Clicked() {
					answer = 2
				}
			})
		})
	}).Label("Unknown host key")
	if m.Dismissed() {
		answer = 2 // Esc or a click outside
	}
	if answer != 0 {
		a.answerHostKey(answer == 1)
	}
}

// answerHostKey takes the shown prompt: Trust retries its tab's open (or
// its Files tab's listing) with the same id, pinned to exactly that key;
// Cancel ends the tab (or fails the listing).
func (a *App) answerHostKey(trust bool) {
	p, ok := a.prompts.Answer()
	if !ok {
		return
	}
	if v := a.files[p.ID]; v != nil {
		if trust {
			a.loadFiles(p.ID, v.requested, &hubapi.Trust{Fingerprint: p.Key.Fingerprint, KeyType: p.Key.KeyType}, false)
		} else {
			v.loading, v.listing, v.err = false, nil, "host key not trusted"
		}
	} else if t, ok := a.tabs.Get(p.ID); ok && t.State == "opening" {
		if trust {
			a.dial(p.ID, t.Server, &hubapi.Trust{Fingerprint: p.Key.Fingerprint, KeyType: p.Key.KeyType})
		} else {
			a.fail(p.ID, "host key not trusted")
		}
	}
	a.focusActive()
}

// buildMismatch shows a pinned key the server no longer presents. There is
// no way to pin the new key here: the host editor's Forget is, which Open
// host editor opens with the focus on it.
func (a *App) buildMismatch(c *ui.Context, k hubapi.HostKey) {
	t := c.Theme()
	closed, edit := false, false
	open := true
	m := ui.Modal(c, &open, func() {
		ui.Column(c).Width(460).Gap(10).Padding(20).Children(func() {
			ui.Text(c, "Host key changed").Bold().FontSize(16).TextColor(t.Danger)
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, model.DisplayText(k.Server)).Bold()
				ui.Text(c, model.DisplayText(keyAddr(k))).Font("monospace").TextColor(t.TextMuted)
			})
			ui.Row(c).Padding(8, 12).Radius(6).Background(t.Danger.Alpha(0.15)).Children(func() {
				ui.Text(c, "This server presented a different host key than the one pinned. The connection may be intercepted. Nothing was sent.")
			})
			ui.Text(c, "Pinned").TextColor(t.TextMuted)
			ui.Text(c, model.DisplayText(k.Pinned)).Font("monospace").Selectable()
			ui.Text(c, "Presented now").TextColor(t.Danger)
			ui.Text(c, model.DisplayText(k.Presented)).Font("monospace").Selectable().TextColor(t.Danger)
			ui.RichText(c,
				ui.Span{Text: "Did the key change on purpose?", Weight: 700},
				ui.Span{Text: " If the server was rebuilt or its keys rotated, confirm the new fingerprint with its admin, then forget the old key in the host editor and connect again."})
			// Close first: when a focus request from outside lands in the
			// modal (a tab activated behind it), mygo gives the focus to the
			// first button, and that must be the safe one.
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Close").AutoFocus().Clicked() {
					closed = true
				}
				if ui.Button(c, "Open host editor").Clicked() {
					edit = true
				}
			})
		})
	}).Label("Host key mismatch")
	if edit {
		a.mismatch = nil
		a.openEditor(k.Server, true)
		return
	}
	if closed || m.Dismissed() {
		a.mismatch = nil
		a.focusActive()
	}
}
