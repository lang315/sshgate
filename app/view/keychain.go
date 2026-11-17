package view

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// keysState is the Keychain's key list. keys.list is called when the view
// is entered, when the editor first needs it, and after a create, a delete
// or a host save: never from a timer or a redraw (the idle lock).
type keysState struct {
	list            []hubapi.Key
	loaded, loading bool
	gen             int // bumped by every read and reset: replies to older ones are dropped
	err             string
}

// keysReset drops the list and every read in flight (the ready screen was
// entered or left).
func (a *App) keysReset() { a.keys = keysState{gen: a.keys.gen + 1} }

func (a *App) keysLoad() {
	a.keys.gen++
	a.keys.loading, a.keys.err = true, ""
	gen, hubGen := a.keys.gen, a.hubGen
	go func() {
		l, err := a.hub().KeysList(context.Background())
		a.Update(func() {
			if gen != a.keys.gen || !a.ready(hubGen) {
				return
			}
			a.keys.loading, a.keys.loaded = false, true
			if err != nil {
				a.keys.err = err.Error()
				return
			}
			a.keys.list = l
		})
	}()
}

// keysEnsure reads the list unless it is loaded or loading. Call it from an
// event handler only (a click), never from a build function.
func (a *App) keysEnsure() {
	if !a.keys.loaded && !a.keys.loading {
		a.keysLoad()
	}
}

// keysChanged is a write that may change the list or who uses a key.
func (a *App) keysChanged() {
	if a.keys.loaded {
		a.keysLoad()
	}
}

func (a *App) key(name string) *hubapi.Key {
	for i := range a.keys.list {
		if a.keys.list[i].Name == name {
			return &a.keys.list[i]
		}
	}
	return nil
}

// newKeyState is the New key dialog; made is the reply, shown until Done.
type newKeyState struct {
	name string
	busy bool
	err  string
	made *hubapi.Key
}

// keyDelState is the delete confirm for a key, shown at at.
type keyDelState struct {
	name string
	at   time.Time
}

func keyDate(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Local().Format("2 Jan 2006")
	}
	return s
}

func (a *App) buildKeychain(c *ui.Context) {
	t, p := c.Theme(), tok(c)
	ui.Scroll(c).Grow(1).Padding(16).Gap(12).Label("Keychain").Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Keychain").Bold().FontSize(16).Grow(1)
			if ui.PrimaryButton(c, "New key").Clicked() {
				a.sheetSeq++
				a.newKey = &newKeyState{}
			}
		})
		if a.keys.err != "" {
			ui.Text(c, model.DisplayText(a.keys.err)).TextColor(t.Danger)
		}
		if len(a.keys.list) == 0 && a.keys.loaded && a.keys.err == "" {
			ui.Text(c, "No keys yet. Create one with New key.").TextColor(t.TextMuted)
		}
		for _, k := range a.keys.list {
			a.buildKeyCard(c, p, k)
		}
	})
}

func (a *App) buildKeyCard(c *ui.Context, p tokens, k hubapi.Key) {
	name := model.DisplayText(k.Name)
	card := ui.Column(c.Key("key-"+k.Name)).Gap(10).Padding(12).Radius(radiusCard).Border(1, p.Line).Background(p.Surface)
	cardShadow(c).On(card)
	card.Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(36, 36).Radius(8).Background(p.AccentTint).Center().Children(func() {
				ui.Icon(c, keyIcon).TextColor(p.Accent)
			})
			ui.Column(c).Grow(1).Children(func() {
				ui.Text(c, name).Bold().SingleLine()
				ui.Text(c, "ECDSA P-256 · Secure Enclave").TextColor(p.Muted).SingleLine()
				ui.Text(c, "Created "+keyDate(k.Created)).TextColor(p.Muted).SingleLine()
			})
			switch n := len(k.UsedBy); n {
			case 0:
				chip(c, "Not used", p.Muted, p.Raised, "")
			default:
				word := "hosts"
				if n == 1 {
					word = "host"
				}
				chip(c, fmt.Sprintf("Used by %d %s", n, word), p.Accent, p.AccentTint, model.DisplayText(strings.Join(k.UsedBy, ", ")))
			}
		})
		termBlock(c, 6).Children(func() {
			ui.Text(c, model.DisplayText(k.PublicKey)).Font("monospace").Selectable().TextColor(p.TermFg)
		})
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, "Copy public key").Clicked() {
				c.WriteClipboard(k.PublicKey)
			}
			del := ui.Button(c, "Delete").Label("Delete " + name).Disabled(len(k.UsedBy) > 0)
			if len(k.UsedBy) > 0 {
				del.Tooltip("In use by " + model.DisplayText(strings.Join(k.UsedBy, ", ")) + ". Remove the key from those hosts first.")
			}
			if del.Clicked() {
				a.keyDel = &keyDelState{name: k.Name, at: a.Now()}
			}
		})
	})
}

// buildKeyDialogs draws the New key dialog and the delete confirm.
func (a *App) buildKeyDialogs(c *ui.Context) {
	a.buildNewKey(c)
	a.buildKeyDelete(c)
}

func (a *App) buildNewKey(c *ui.Context) {
	nk := a.newKey
	if nk == nil {
		return
	}
	t, p := c.Theme(), tok(c)
	open, done, create := true, false, false
	m := modal(c.Key(fmt.Sprintf("newkey-%d", a.sheetSeq)), &open, func() {
		ui.Column(c).Width(440).Gap(12).Children(func() {
			ui.Text(c, "New key").Bold().FontSize(16)
			if nk.made == nil {
				ui.Text(c, "The key is made in this Mac's Secure Enclave and never leaves it.").TextColor(t.TextMuted)
				in := ui.TextInput(c.Key("keyname"), &nk.name).Label("Key name").Placeholder("laptop").AutoFocus()
				if in.Submitted() {
					create = true
				}
				if nk.err != "" {
					ui.Text(c, model.DisplayText(nk.err)).TextColor(t.Danger)
				}
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					if ui.Button(c, "Cancel").Clicked() {
						done = true
					}
					if ui.PrimaryButton(c, "Create").Disabled(nk.busy).Clicked() {
						create = true
					}
				})
				return
			}
			ui.Text(c, "Add this public key to the server's authorized_keys.").TextColor(t.TextMuted)
			termBlock(c, 6).Children(func() {
				ui.Text(c, model.DisplayText(nk.made.PublicKey)).Font("monospace").Selectable().TextColor(p.TermFg)
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Copy public key").Clicked() {
					c.WriteClipboard(nk.made.PublicKey)
				}
				if ui.PrimaryButton(c, "Done").Clicked() {
					done = true
				}
			})
		})
	}).Label("New key")
	if m.Dismissed() || done {
		if !nk.busy {
			a.newKey = nil
		}
		return
	}
	if create && !nk.busy {
		a.createKey(nk)
	}
}

func (a *App) createKey(nk *newKeyState) {
	name := strings.TrimSpace(nk.name)
	if msg := model.KeyNameError(name); msg != "" {
		nk.err = msg
		return
	}
	nk.busy, nk.err = true, ""
	gen := a.hubGen
	go func() {
		k, err := a.hub().KeysCreate(context.Background(), name)
		a.Update(func() {
			if !a.ready(gen) {
				return
			}
			nk.busy = false
			if err != nil {
				nk.err = err.Error()
				return
			}
			nk.made = &k
			a.keysLoad()
		})
	}()
}

func (a *App) buildKeyDelete(c *ui.Context) {
	del := a.keyDel
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
	m := modal(c.Key("keydel-"+del.name), &open, func() {
		ui.Column(c).Width(420).Gap(12).Children(func() {
			ui.Text(c, "Delete key "+model.DisplayText(del.name)+"? It cannot be recovered: servers that trust its public key will no longer accept it.")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.PrimaryButton(c, "Cancel").AutoFocus().Clicked() {
					answer = 2
				}
				if decisionButton(c, "Delete key", "Delete key", ok, toneDangerFill) && model.TrustEnabled(del.at, a.Now()) {
					answer = 1
				}
			})
		})
	}).Label("Confirm delete key")
	if m.Dismissed() {
		answer = 2
	}
	if answer == 0 {
		return
	}
	a.keyDel = nil
	if answer == 1 {
		a.deleteKey(del.name)
	}
}

func (a *App) deleteKey(name string) {
	gen := a.hubGen
	go func() {
		err := a.hub().KeysDelete(context.Background(), name)
		a.Update(func() {
			if !a.ready(gen) {
				return
			}
			if err != nil {
				a.alert = err.Error()
			}
			a.keysLoad()
		})
	}()
}

// buildKeychainAuth is the editor's Keychain auth: a select of key names and
// the chosen key's public key. No passphrase: the key never leaves the Secure Enclave.
func (a *App) buildKeychainAuth(c *ui.Context, d *model.HostDraft) {
	t, p := c.Theme(), tok(c)
	if len(a.keys.list) == 0 {
		if a.keys.err != "" {
			ui.Text(c, model.DisplayText(a.keys.err)).TextColor(t.Danger)
			if ui.Button(c, "Try again").Clicked() {
				a.keysLoad()
			}
		} else if a.keys.loaded {
			ui.Text(c, "No keys yet. Create one in Keychain.").TextColor(t.TextMuted)
		}
		return
	}
	names := make([]string, len(a.keys.list))
	for i, k := range a.keys.list {
		names[i] = k.Name
	}
	// A name not in the list (or none) shows "Choose a key"; Save stays off until one is picked.
	const none = "Choose a key"
	sel := d.KeyName
	if !slices.Contains(names, sel) {
		sel = none
	}
	ui.Field(c, "Keychain key", func() { ui.Select(c.Key("keyname"), &sel, append([]string{none}, names...)) })
	d.KeyName = sel
	if sel == none {
		d.KeyName = ""
	}
	if k := a.key(d.KeyName); k != nil {
		termBlock(c, 6).Children(func() {
			ui.Text(c, model.DisplayText(k.PublicKey)).Font("monospace").Selectable().TextColor(p.TermFg)
		})
	}
}
