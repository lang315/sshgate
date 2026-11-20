package view

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

const inboundNote = "These devices can log in only when Remote Login is on. The list protects this Mac only when password login is off."

// partialPrefix starts the error of a write that changed the list but not authorized_keys.
const partialPrefix = "saved, but authorized_keys was not updated: "

// fileErrText is what a failed or doubtful authorized_keys render means for the
// person: usually the file is stale, so a blocked or deleted device may still
// log in; a key also on a line outside the block is written but cannot be reached.
func fileErrText(err string) string {
	err = strings.TrimPrefix(err, partialPrefix)
	if strings.Contains(err, "outside sshgate's block") {
		return "authorized_keys holds the new list, but a device key is also on a line outside sshgate's block, which blocking and deleting do not reach. " + err
	}
	return "authorized_keys still holds the old list: a blocked or deleted device may still log in. " + err
}

// inboundState is the Inbound view's device list. inbound.list is called when
// the view is entered, after a write, and by Try again: never from a timer
// or a redraw (the idle lock).
type inboundState struct {
	list            []hubapi.InboundDevice
	authKeys        string
	fileErr         string
	loaded, loading bool
	gen             int // bumped by every read and reset: replies to older ones are dropped
	err             string
	writeErr        string // the last failed write
	busy            bool   // a write is in flight
	ips             map[string]*ipDraft
}

// ipDraft is a device card's IP address field and its client-side error.
type ipDraft struct{ text, err string }

func (a *App) inboundReset() { a.inbound = inboundState{gen: a.inbound.gen + 1} }

func (a *App) inboundLoad() {
	a.inbound.gen++
	a.inbound.loading, a.inbound.err = true, ""
	gen, hubGen := a.inbound.gen, a.hubGen
	go func() {
		l, err := a.hub().InboundList(context.Background())
		a.Update(func() {
			if gen != a.inbound.gen || !a.ready(hubGen) {
				return
			}
			a.inbound.loading, a.inbound.loaded = false, true
			if err != nil {
				a.inbound.err = err.Error()
				return
			}
			a.inbound.list, a.inbound.authKeys, a.inbound.fileErr = l.Devices, l.AuthorizedKeys, l.FileError
		})
	}()
}

// inboundWrite runs one write. A success, or the partial-failure error (the
// list changed, the file did not), reloads the list; any other error leaves
// it. done runs on success only.
func (a *App) inboundWrite(call func(h *hubapi.Client) error, done func()) {
	a.inbound.busy, a.inbound.writeErr = true, ""
	gen := a.hubGen
	go func() {
		err := call(a.hub())
		a.Update(func() {
			if !a.ready(gen) {
				return
			}
			a.inbound.busy = false
			if err == nil {
				if done != nil {
					done()
				}
				a.inboundLoad()
				return
			}
			a.inbound.writeErr = err.Error()
			if strings.HasPrefix(err.Error(), partialPrefix) {
				a.inbound.writeErr = fileErrText(err.Error())
				a.inboundLoad()
			}
		})
	}()
}

func (a *App) ipField(name string) *ipDraft {
	if a.inbound.ips == nil {
		a.inbound.ips = map[string]*ipDraft{}
	}
	d := a.inbound.ips[name]
	if d == nil {
		d = &ipDraft{}
		a.inbound.ips[name] = d
	}
	return d
}

// setIP puts ip in state for a device, after the client-side check.
func (a *App) setIP(name, ip, state string, d *ipDraft) {
	if a.inbound.busy {
		return
	}
	ip = strings.TrimSpace(ip)
	if msg := model.InboundIPError(ip); msg != "" {
		d.err = msg
		return
	}
	d.err = ""
	a.inboundWrite(func(h *hubapi.Client) error { return h.InboundSetIP(context.Background(), name, ip, state) }, func() {
		if state != hubapi.InboundRemoved {
			d.text = ""
		}
	})
}

// newDeviceState is the New device dialog.
type newDeviceState struct {
	name, key, ip string
	busy          bool
	err           string
}

func (n *newDeviceState) ready() bool {
	return !n.busy && strings.TrimSpace(n.name) != "" && strings.TrimSpace(n.key) != "" && strings.TrimSpace(n.ip) != ""
}

// deviceDelState is the delete confirm for a device, shown at at.
type deviceDelState struct {
	name string
	at   time.Time
}

func (a *App) buildInbound(c *ui.Context) {
	t, p := c.Theme(), tok(c)
	ui.Scroll(c).Grow(1).Padding(16).Gap(12).Label("Inbound").Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Inbound").Bold().FontSize(16).Grow(1)
			if ui.PrimaryButton(c, "New device").Clicked() {
				a.sheetSeq++
				a.newDev = &newDeviceState{}
			}
		})
		ui.Text(c, inboundNote).TextColor(t.TextMuted)
		if a.inbound.fileErr != "" {
			a.inboundBanner(c, p, fileErrText(a.inbound.fileErr))
		}
		if a.inbound.writeErr != "" {
			a.inboundBanner(c, p, a.inbound.writeErr)
		}
		if a.inbound.err != "" {
			ui.Text(c, model.DisplayText(a.inbound.err)).TextColor(t.Danger)
			if ui.Button(c, "Try again").Clicked() {
				a.inboundLoad() // a click only: never from a build, a timer or a redraw
			}
		}
		if len(a.inbound.list) == 0 && a.inbound.loaded && a.inbound.err == "" {
			ui.Text(c, "No devices yet. Add one with New device.").TextColor(t.TextMuted)
		}
		for _, d := range a.inbound.list {
			a.buildDeviceCard(c, p, d)
		}
		if a.inbound.authKeys != "" {
			ui.Text(c, "Rendered into "+model.DisplayText(a.inbound.authKeys)).TextColor(t.TextMuted).Selectable()
		}
	})
}

func (a *App) inboundBanner(c *ui.Context, p tokens, msg string) {
	ui.Row(c).Padding(8, 12).Radius(radiusControl).Background(p.DangerTint).Children(func() {
		ui.Text(c, model.DisplayText(msg)).TextColor(p.Danger).Grow(1)
	})
}

func (a *App) buildDeviceCard(c *ui.Context, p tokens, d hubapi.InboundDevice) {
	name := model.DisplayText(d.Name)
	card := ui.Column(c.Key("dev-"+d.Name)).Gap(10).Padding(12).Radius(radiusCard).Border(1, p.Line).Background(p.Surface)
	cardShadow(c).On(card)
	draft := a.ipField(d.Name)
	card.Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(36, 36).Radius(8).Background(p.AccentTint).Center().Children(func() {
				ui.Icon(c, inboundIcon).TextColor(p.Accent)
			})
			ui.Column(c).Grow(1).Children(func() {
				ui.Text(c, name).Bold().SingleLine()
				typ, _, _ := strings.Cut(d.PublicKey, " ")
				ui.Text(c, model.DisplayText(typ+" · "+d.Fingerprint)).Font("monospace").TextColor(p.Muted).SingleLine().Selectable()
			})
			if len(d.Allowed) == 0 {
				chip(c, "Cannot log in", p.Muted, p.Raised, "No allowed IP: this device gets no line in authorized_keys.")
			}
		})
		if len(d.Allowed)+len(d.Blocked) > 0 {
			ui.Column(c).Gap(4).Children(func() {
				for _, ip := range d.Allowed {
					a.buildIPRow(c, p, d.Name, ip, true)
				}
				for _, ip := range d.Blocked {
					a.buildIPRow(c, p, d.Name, ip, false)
				}
			})
		}
		ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
			var in ui.Element
			ui.Field(c, "IP address", func() {
				in = ui.TextInput(c.Key("ip-"+d.Name), &draft.text).Placeholder("100.64.0.5 or 192.168.1.0/24")
			}).Grow(1)
			can := !a.inbound.busy && strings.TrimSpace(draft.text) != ""
			// Enter does what Allow does, and only when Allow is enabled.
			if in.Submitted() && can {
				a.setIP(d.Name, draft.text, hubapi.InboundAllowed, draft)
			}
			if ui.Button(c, "Allow").Label("Allow IP for " + name).Disabled(!can).Clicked() {
				a.setIP(d.Name, draft.text, hubapi.InboundAllowed, draft)
			}
			if ui.Button(c, "Block").Label("Block IP for " + name).Disabled(!can).Clicked() {
				a.setIP(d.Name, draft.text, hubapi.InboundBlocked, draft)
			}
		})
		if draft.err != "" {
			ui.Text(c, model.DisplayText(draft.err)).TextColor(p.Danger)
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.Button(c, "Delete device").Label("Delete device " + name).Clicked() {
				a.devDel = &deviceDelState{name: d.Name, at: a.Now()}
			}
		})
	})
}

func (a *App) buildIPRow(c *ui.Context, p tokens, dev, ip string, allowed bool) {
	shown := model.DisplayText(ip)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, shown).Font("monospace").Selectable().Grow(1).SingleLine()
		if allowed {
			chip(c, "Allowed", p.Ok, p.OkTint, "")
		} else {
			chip(c, "Blocked", p.Danger, p.DangerTint, "")
		}
		toggle, to := "Block", hubapi.InboundBlocked
		if !allowed {
			toggle, to = "Unblock", hubapi.InboundAllowed
		}
		if ui.Button(c, toggle).Label(toggle + " " + shown + " on " + model.DisplayText(dev)).Disabled(a.inbound.busy).Clicked() {
			a.setIP(dev, ip, to, a.ipField(dev))
		}
		if ui.Button(c, "Remove").Label("Remove " + shown + " from " + model.DisplayText(dev)).Disabled(a.inbound.busy).Clicked() {
			a.setIP(dev, ip, hubapi.InboundRemoved, a.ipField(dev))
		}
	})
}

// buildInboundDialogs draws the New device dialog and the delete confirm.
func (a *App) buildInboundDialogs(c *ui.Context) {
	a.buildNewDevice(c)
	a.buildDeviceDelete(c)
}

func (a *App) buildNewDevice(c *ui.Context) {
	nd := a.newDev
	if nd == nil {
		return
	}
	t := c.Theme()
	open, done, add := true, false, false
	m := modal(c.Key(fmt.Sprintf("newdev-%d", a.sheetSeq)), &open, func() {
		ui.Column(c).Width(480).Gap(12).Children(func() {
			ui.Text(c, "New device").Bold().FontSize(16)
			ui.Text(c, "Paste the device's public key, one line such as ssh-ed25519 AAAA...").TextColor(t.TextMuted)
			// Enter in a field adds only when Add is enabled.
			submitted := false
			ui.Field(c, "Device name", func() {
				submitted = ui.TextInput(c.Key("devname"), &nd.name).Placeholder("iphone").AutoFocus().Submitted() || submitted
			})
			ui.Field(c, "Public key", func() {
				ui.TextArea(c.Key("devkey"), &nd.key).Placeholder("ssh-ed25519 AAAA...").Lines(3, 6).Font("monospace")
			})
			ui.Field(c, "First IP address", func() {
				submitted = ui.TextInput(c.Key("devip"), &nd.ip).Placeholder("100.64.0.5 or 192.168.1.0/24").Submitted() || submitted
			})
			if submitted && nd.ready() {
				add = true
			}
			if nd.err != "" {
				ui.Text(c, model.DisplayText(nd.err)).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					done = true
				}
				if ui.PrimaryButton(c, "Add device").Disabled(!nd.ready()).Clicked() {
					add = true
				}
			})
		})
	}).Label("New device")
	if m.Dismissed() || done {
		if !nd.busy {
			a.newDev = nil
		}
		return
	}
	if add && nd.ready() {
		a.addDevice(nd)
	}
}

func (a *App) addDevice(nd *newDeviceState) {
	name, key, ip := strings.TrimSpace(nd.name), strings.TrimSpace(nd.key), strings.TrimSpace(nd.ip)
	if msg := model.InboundAddError(name, key, ip); msg != "" {
		nd.err = msg
		return
	}
	nd.busy, nd.err = true, ""
	gen := a.hubGen
	go func() {
		err := a.hub().InboundAdd(context.Background(), name, key, ip)
		a.Update(func() {
			if !a.ready(gen) {
				return
			}
			nd.busy = false
			if err == nil || strings.HasPrefix(err.Error(), partialPrefix) {
				a.inboundLoad()
			}
			if err != nil {
				nd.err = err.Error()
				if strings.HasPrefix(err.Error(), partialPrefix) { // the device is on the list: close, show the error there
					a.inbound.writeErr, a.newDev = fileErrText(err.Error()), nil
				}
				return
			}
			a.newDev = nil
		})
	}()
}

func (a *App) buildDeviceDelete(c *ui.Context) {
	del := a.devDel
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
	m := modal(c.Key("devdel-"+del.name), &open, func() {
		ui.Column(c).Width(420).Gap(12).Children(func() {
			ui.Text(c, "Delete device "+model.DisplayText(del.name)+"? Its key is removed from authorized_keys and it can no longer log in.")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.PrimaryButton(c, "Cancel").AutoFocus().Clicked() {
					answer = 2
				}
				if decisionButton(c, "Delete device", "Delete device", ok, toneDangerFill) && model.TrustEnabled(del.at, a.Now()) {
					answer = 1
				}
			})
		})
	}).Label("Confirm delete device")
	if m.Dismissed() {
		answer = 2
	}
	if answer == 0 {
		return
	}
	if answer == 1 && a.inbound.busy {
		return // a write is in flight; the dialog stays
	}
	a.devDel = nil
	if answer == 1 {
		name := del.name
		a.inboundWrite(func(h *hubapi.Client) error { return h.InboundDelete(context.Background(), name) }, func() { delete(a.inbound.ips, name) })
	}
}
