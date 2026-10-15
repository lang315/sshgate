package view

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// savedBut starts the hub's error for a save that stood while its grant
// was not armed (Electron's SAVED_BUT).
const savedBut = "saved, but auto-allow was not turned on: "

// editorState is the open host editor (Electron's HostEditor.tsx): name is
// the server edited, "" for New host. A typed secret lives only in draft,
// which goes when the editor closes.
type editorState struct {
	seq             int // keys the sheet: each editor is a new mount, so AutoFocus runs again
	name            string
	draft           model.HostDraft
	showAuth        bool // the Auth radios instead of "+ Key or agent"
	escOpen, aiOpen bool // the two folds
	focusForget     bool // opened from the mismatch dialog
	busy            bool
	err             string
}

// server is a copy of the stored server called name, or nil.
func (a *App) server(name string) *hubapi.Server {
	for i := range a.servers {
		if name != "" && a.servers[i].Name == name {
			s := a.servers[i]
			return &s
		}
	}
	return nil
}

// openEditor shows the editor for name ("" for New host) from the list the
// app holds, in place of the import sheet.
func (a *App) openEditor(name string, focusForget bool) {
	s := a.server(name)
	a.sheetSeq++
	a.imp = nil
	a.editor = &editorState{seq: a.sheetSeq, name: name, draft: model.DraftFrom(s), showAuth: s != nil && s.Auth != "password", focusForget: focusForget}
}

// dropDialogs closes the host dialogs when the ready screen goes (lock, hub
// stop): a typed secret must not outlive it, and an unlock opens none again.
func (a *App) dropDialogs() { a.editor, a.imp, a.del = nil, nil, nil }

// closeSheet drops the open sheet, and any typed secret with it. mygo
// restores no focus: the active terminal gets it, else the host search.
func (a *App) closeSheet() {
	a.dropDialogs()
	if a.tabs.Active != "" {
		a.focusActive()
		return
	}
	a.searchH.Focus()
}

// keptMode is the autoAllow a save sends while the editor has no Auto-allow
// select: a live forever grant is armed again in the same write (Electron
// saves that without asking); a paused or timed one, or none, is off. 3b
// replaces it with saveConfirm and the Auto-allow dialog.
func keptMode(s *hubapi.Server, d model.HostDraft) string {
	if d.AutoAllow == "forever" && s != nil && s.AutoAllow != nil && s.AutoAllow.Forever && !s.AutoAllow.Paused {
		return "forever"
	}
	return "off"
}

// saveHost sends servers.save. A bad port is never sent: typed text reaches
// the draft only in the next build, after the check buildEditor drew. The
// reply is dropped if its hub stopped or the ready screen went (a lock); it
// touches this editor only, never one opened since.
func (a *App) saveHost() {
	ed := a.editor
	if ed == nil || ed.busy || model.PortError(ed.draft.Port) != "" {
		return
	}
	in, mode := model.ToInput(ed.draft), keptMode(a.server(ed.name), ed.draft)
	ed.busy, ed.err = true, ""
	gen := a.hubGen
	go func() {
		err := a.hub().SaveServer(context.Background(), ed.name, in, mode)
		a.Update(func() {
			if gen != a.hubGen || a.screen != model.ScreenReady {
				return
			}
			ed.busy = false
			if err != nil && !strings.HasPrefix(err.Error(), savedBut) {
				ed.err = err.Error()
				return
			}
			if err != nil {
				a.alert = "Saved, but auto-allow was not turned on: " + strings.TrimPrefix(err.Error(), savedBut)
			}
			a.reloadServers()
			if a.editor == ed {
				a.closeSheet()
			}
		})
	}()
}

// buildEditor is the host editor: a column beside the content, not a modal,
// so the AI requests column stays usable. Esc closes it only while the
// focus is inside it, and not while a save runs.
func (a *App) buildEditor(c *ui.Context) {
	t := c.Theme()
	ed := a.editor
	s := a.server(ed.name)
	portErr := model.PortError(ed.draft.Port)
	title := "New host"
	if ed.name != "" {
		title = "Edit " + model.DisplayText(ed.name)
	}
	submit, closed := false, false
	sheet := ui.Column(c.Key("editor-"+strconv.Itoa(ed.seq))).Fill().Width(420).Background(t.Surface).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).
		Role(ui.RoleDialog).Label("Host editor")
	sheet.Children(func() {
		ui.Row(c).Gap(8).Padding(12, 16).Children(func() {
			ui.Text(c, title).Bold().FontSize(16).Grow(1)
			if ui.Button(c, "×").Label("Close host editor").Tooltip("Close").Clicked() {
				closed = true
			}
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(14).Padding(8, 16).Children(func() {
				submit = a.buildEditorBody(c, s, portErr)
			})
		})
		ui.Row(c).Gap(8).Padding(12, 16).Justify(ui.End).Children(func() {
			if ed.err != "" {
				ui.Text(c, model.DisplayText(ed.err)).TextColor(t.Danger).Grow(1)
			}
			if ui.Button(c, "Close").Clicked() {
				closed = true
			}
			if ui.PrimaryButton(c, "Save").Disabled(ed.busy || portErr != "").Clicked() {
				submit = true
			}
		})
	})
	if sheet.Shortcut(0, ui.KeyEscape) && !ed.busy {
		closed = true
	}
	switch {
	case closed:
		a.closeSheet()
	case submit:
		a.saveHost()
	}
}

// buildEditorBody draws the form, top to bottom as Electron's; it reports
// Enter in a text field, which saves.
func (a *App) buildEditorBody(c *ui.Context, s *hubapi.Server, portErr string) (submit bool) {
	t := c.Theme()
	ed := a.editor
	d := &ed.draft
	in := model.ToInput(*d)
	moved := model.EndpointChanged(s, *d)
	text := func(key, label string, v *string, opts func(ui.Element)) ui.Element {
		var input ui.Element
		f := ui.Field(c, label, func() {
			input = ui.TextInput(c.Key(key), v)
			opts(input)
		})
		if input.Submitted() {
			submit = true
		}
		return f
	}
	secret := func(f model.Secret) {
		e := &d.Secrets[f]
		saved := f.Saved(s)
		hint := model.SecretHint(saved, *e, moved)
		ui.Row(c.Key(fmt.Sprintf("secret-%d", f))).Gap(8).Children(func() {
			text(fmt.Sprintf("secret-in-%d", f), f.Label(), &e.Value, func(input ui.Element) {
				input.Password().Placeholder(hint)
				if input.Changed() {
					e.Cleared = false // typing after Clear sends the new value
				}
			}).Description(hint).Grow(1)
			if saved && !e.Cleared && ui.Button(c, "Clear").Label("Clear "+f.Label()).Clicked() {
				e.Value, e.Cleared = "", true
			}
		})
	}

	addr := text("host", "Address", &d.Host, func(input ui.Element) {
		input.Placeholder("IP or hostname").Font("monospace")
		if !ed.focusForget {
			input.AutoFocus()
		}
	})
	if s != nil && in.Host != s.Host {
		addr.Description("· changed")
	}
	ph := in.Host
	if ph == "" {
		ph = "Defaults to the address"
	}
	label := text("name", "Label", &d.Name, func(input ui.Element) { input.Placeholder(ph) })
	if h := model.LabelHint(*d); h != "" {
		label.Description(h)
	}
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "SSH on")
		if ui.TextInput(c.Key("port"), &d.Port).Label("Port").Font("monospace").Width(90).Submitted() {
			submit = true
		}
		ui.Text(c, "port")
		if s != nil && in.Port != s.Port {
			ui.Text(c, "· changed").TextColor(t.Warning)
		}
	})
	if portErr != "" {
		ui.Text(c, portErr).TextColor(t.Danger)
	}

	ui.Text(c, "Credentials").Bold()
	for f := model.SecretPassword; f <= model.SecretKeyPassphrase; f++ {
		if f.Saved(s) {
			ui.Text(c, "Saved secrets are never shown. Leave a field empty to keep it.").TextColor(t.TextMuted)
			break
		}
	}
	text("user", "User", &d.User, func(input ui.Element) { input.Placeholder("Username").Font("monospace") })
	if d.Auth == "password" || model.SecretPassword.Saved(s) {
		secret(model.SecretPassword)
	}
	if !ed.showAuth {
		if ui.Button(c, "+ Key or agent").Clicked() {
			ed.showAuth = true
		}
	} else {
		ui.Text(c, "Auth")
		// Keyed: the Password field above comes and goes with the choice, and
		// an unkeyed radio would lose the focus the arrows just gave it.
		ui.RadioGroup(c.Key("auth"), func() { // one Tab stop; the arrows choose, and wrap
			for _, v := range []string{"password", "key", "agent"} {
				ui.Radio(c.Key("auth-"+v), &d.Auth, v, v)
			}
		}).Row().Label("Auth")
		if d.Auth == "key" {
			text("keypath", "Key path", &d.KeyPath, func(input ui.Element) { input.Placeholder("~/.ssh/id_ed25519").Font("monospace") })
			secret(model.SecretKeyPassphrase)
		}
	}

	esc := "Privilege escalation (su / sudo)"
	if model.SecretSu.Saved(s) || model.SecretSudo.Saved(s) {
		esc += " · saved"
	}
	ui.Collapsible(c.Key("fold-esc"), esc, &ed.escOpen, func() {
		secret(model.SecretSu)
		secret(model.SecretSudo)
	})
	ai := "AI access · Off"
	if d.AIVisible {
		ai = "AI access · On"
	}
	if d.AutoAllow != "off" {
		ai += " · auto"
	}
	ui.Collapsible(c.Key("fold-ai"), ai, &ed.aiOpen, func() { a.buildAIAccess(c, s) })

	if s != nil {
		ui.Text(c, "Host key").Bold()
		if s.HostKey != "" {
			ui.Text(c, model.DisplayText(strings.TrimSpace(s.HostKeyAlgo+" "+s.HostKey))).Font("monospace").Selectable()
			ui.Row(c).Gap(8).Children(func() {
				forget := ui.Button(c, "Forget host key").Disabled(ed.busy)
				if ed.focusForget {
					forget.AutoFocus()
				}
				if forget.Clicked() {
					a.forgetKey()
				}
				ui.Text(c, "Takes effect immediately.").TextColor(t.TextMuted)
			})
		} else {
			ui.Text(c, "Not pinned. You will be asked to confirm it on the next connect.").TextColor(t.TextMuted)
		}
	}
	if w := model.EditorWarnings(s, *d, a.tabs.OpenCount(ed.name), 0); len(w) > 0 {
		ui.Column(c).Gap(4).Padding(8, 12).Radius(6).Background(t.Warning.Alpha(0.15)).Role(ui.RoleStatus).Children(func() {
			for _, line := range w {
				ui.Text(c, line)
			}
		})
	}
	return submit
}

// buildAIAccess is the AI access fold. 3b adds the Auto-allow select and the
// two opt-in checkboxes here, below the switch.
func (a *App) buildAIAccess(c *ui.Context, s *hubapi.Server) {
	t := c.Theme()
	ui.Field(c, "Visible to AI", func() { ui.Switch(c, &a.editor.draft.AIVisible) })
	ui.Text(c, "AI clients can see this server and ask to run commands. Commands wait for your approval unless auto-allow is on.").TextColor(t.TextMuted)
	if s == nil || s.HostKey == "" {
		ui.Text(c, "Needs a pinned host key.").TextColor(t.TextMuted)
	}
}

// forgetKey unpins the edited server's host key at once, as Electron does,
// with no confirm; the editor stays open and shows it unpinned once the
// host list is back. The hub closes the server's connection. The reply is
// dropped if its hub stopped or the ready screen went (a lock).
func (a *App) forgetKey() {
	ed := a.editor
	if ed == nil || ed.busy {
		return
	}
	ed.busy, ed.err = true, ""
	gen := a.hubGen
	go func() {
		err := a.hub().ForgetHostKey(context.Background(), ed.name)
		a.Update(func() {
			if gen != a.hubGen || a.screen != model.ScreenReady {
				return
			}
			ed.busy = false
			if err != nil {
				ed.err = err.Error()
				return
			}
			a.reloadServers()
		})
	}()
}

// deleteState is the delete confirm for name, shown at at: its Delete
// waits AllowDelay, so a double click on the card cannot confirm it.
type deleteState struct {
	name string
	at   time.Time
}

func (a *App) askDelete(name string) { a.del = &deleteState{name: name, at: a.Now()} }

// buildDeleteConfirm is Electron's window.confirm before servers.delete:
// Cancel has the focus, so Enter and Esc keep the host, and Delete host is
// mouse-only.
func (a *App) buildDeleteConfirm(c *ui.Context) {
	del := a.del
	if del == nil {
		return
	}
	now := a.Now()
	ok := model.TrustEnabled(del.at, now)
	if !ok {
		c.After(del.at.Add(model.AllowDelay).Sub(now))
	}
	answer := 0 // 1 delete, 2 cancel
	open := true
	m := ui.Modal(c.Key("del-"+del.name), &open, func() {
		ui.Column(c).Width(420).Gap(12).Padding(20).Children(func() {
			ui.Text(c, "Delete "+model.DisplayText(del.name)+"? Its saved passwords and host key are removed and its open tabs close.")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").AutoFocus().Clicked() {
					answer = 2
				}
				if MouseButton(c, "Delete host", ok) && model.TrustEnabled(del.at, a.Now()) {
					answer = 1
				}
			})
		})
	}).Label("Confirm delete")
	if m.Dismissed() {
		answer = 2
	}
	if answer == 0 {
		return
	}
	a.del = nil
	if answer == 1 {
		a.deleteHost(del.name)
	}
	a.searchH.Focus()
}

// deleteHost sends servers.delete; a refusal shows in the alert row, and
// the host list is read again either way. The reply is dropped if its hub
// stopped or the ready screen went (a lock).
func (a *App) deleteHost(name string) {
	gen := a.hubGen
	go func() {
		err := a.hub().DeleteServer(context.Background(), name)
		a.Update(func() {
			if gen != a.hubGen || a.screen != model.ScreenReady {
				return
			}
			if err != nil {
				a.alert = err.Error()
			}
			a.reloadServers()
		})
	}()
}
