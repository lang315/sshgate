package view

import (
	"context"
	"fmt"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// tunnelsTab is one host's Tunnels tab (Electron's TunnelsView.tsx). Its
// rows are App.tunnels, the hub's list; err is the last action's error.
type tunnelsTab struct {
	server string
	err    string
	dlg    *tunnelDialog
}

// tunnelDialog is Add tunnel (edit nil) or Edit tunnel.
type tunnelDialog struct {
	edit *hubapi.Tunnel
	form model.TunnelForm
	err  string
	busy bool
}

// tunnelActionsW holds Start/Stop, Edit and Delete (about 193 pt) with room for wider fonts.
const tunnelActionsW = 230

// tunnelKinds are the dialog's radio buttons, with Electron's hints.
var tunnelKinds = []struct{ kind, label, hint string }{
	{"local", "Local", "A port here reaches a host:port as seen from the server"},
	{"remote", "Remote", "A port on the server reaches a host:port as seen from here"},
	{"dynamic", "Dynamic", "A SOCKS5 proxy here; connections leave from the server"},
}

// openTunnels shows server's Tunnels tab, adding it the first time.
func (a *App) openTunnels(server string) string {
	id := a.tabs.OpenTunnels(server)
	if a.tunnelTabs[id] == nil {
		a.tunnelTabs[id] = &tunnelsTab{server: server}
	}
	return id
}

// reloadTunnels reads tunnels.list. The tunnels.state notes that arrive
// while it is in flight are kept and applied to its reply (Electron's
// tunnelBuffers), so an older snapshot never hides a newer status. A
// failed read keeps the list shown, and a reply older than one already
// applied is dropped (the hub answers each request on its own goroutine).
func (a *App) reloadTunnels() {
	a.tunnelSeq++
	n, gen := a.tunnelSeq, a.hubGen
	a.tunnelBufs[n] = nil // only the key matters: tunnelState appends to every open buffer
	go func() {
		l, err := a.hub().TunnelsList(context.Background())
		a.Update(func() {
			buf := a.tunnelBufs[n]
			delete(a.tunnelBufs, n)
			if gen != a.hubGen || err != nil || n < a.tunnelApplied {
				return
			}
			a.tunnelApplied = n
			a.tunnels = model.ReplayTunnelStates(l, buf)
		})
	}()
}

// tunnelState applies a tunnels.state note; it calls nothing. The hub sends
// them behind the lock too, so the list stays true across a lock.
func (a *App) tunnelState(e hubapi.TunnelState) {
	a.tunnels = model.ApplyTunnelState(a.tunnels, e)
	for n, buf := range a.tunnelBufs {
		a.tunnelBufs[n] = append(buf, e)
	}
}

// tunnelsStop is a notification: it works behind the lock and answers nothing.
func (a *App) tunnelsStop(server, id string) {
	a.Door.Notify("tunnels.stop", map[string]string{"server": server, "id": id})
}

// tunnelAct runs a row's Start or Delete. Its error shows in the tab; a
// delete then reads the list again, while the ready screen shows.
func (a *App) tunnelAct(id, tid string, del bool) {
	v := a.tunnelTabs[id]
	v.err = ""
	gen, server := a.hubGen, v.server
	go func() {
		var err error
		if del {
			err = a.hub().TunnelsDelete(context.Background(), server, tid)
		} else {
			err = a.hub().TunnelsStart(context.Background(), server, tid)
		}
		a.Update(func() {
			if gen != a.hubGen || a.tunnelTabs[id] != v {
				return
			}
			if err != nil {
				v.err = err.Error()
				return
			}
			if del && a.ready(gen) {
				a.reloadTunnels()
			}
		})
	}()
}

// saveTunnel checks the dialog's fields, then sends tunnels.save. On success
// the dialog closes and, while the ready screen shows, the list is read
// again; on error it stays open with the message.
func (a *App) saveTunnel(id string) {
	v := a.tunnelTabs[id]
	dl := v.dlg
	if dl.busy {
		return
	}
	tid := ""
	if dl.edit != nil {
		tid = dl.edit.ID
	}
	t, e := model.ParseTunnelForm(dl.form, tid)
	if e != "" {
		dl.err = e
		return
	}
	dl.busy, dl.err = true, ""
	gen, server := a.hubGen, v.server
	go func() {
		_, err := a.hub().TunnelsSave(context.Background(), server, t)
		a.Update(func() {
			if gen != a.hubGen || a.tunnelTabs[id] != v || v.dlg != dl {
				return
			}
			dl.busy = false
			if err != nil {
				dl.err = err.Error()
				return
			}
			v.dlg = nil
			if a.ready(gen) {
				a.reloadTunnels()
			}
		})
	}()
}

// endTunnelTabs is a hub stop for the Tunnels tabs: the tunnels died with
// it, and a dialog's request gets no reply.
func (a *App) endTunnelTabs() {
	for _, v := range a.tunnelTabs {
		v.dlg, v.err = nil, ""
	}
}

// buildTunnels draws a Tunnels tab: Add tunnel, the rows with Start or
// Stop, Edit and Delete, and the dialog. Actions run after the build, as
// the tab strip's do.
func (a *App) buildTunnels(c *ui.Context, t model.Tab) {
	id := t.ID
	v := a.tunnelTabs[id]
	th := c.Theme()
	server := v.server
	var act func()
	ui.Column(c).Fill().Gap(8).Padding(8).Label("Tunnels on " + model.DisplayText(server)).Children(func() {
		ui.Row(c).Children(func() {
			if ui.Button(c, "Add tunnel").Clicked() {
				act = func() { v.dlg = &tunnelDialog{form: model.FormFor(nil)} }
			}
		})
		if v.err != "" {
			ui.Text(c, model.DisplayText(v.err)).TextColor(th.Danger)
		}
		var mine []hubapi.TunnelView
		for _, x := range a.tunnels {
			if x.Server == server {
				mine = append(mine, x)
			}
		}
		if len(mine) == 0 {
			ui.Text(c, "No tunnels yet. Add one to reach a port on or through this host.").TextColor(th.TextMuted)
		} else {
			ui.Scroll(c).Grow(1).Gap(6).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					// Named apart from the dialog's Label field: the Tester clicks the first match.
					ui.Text(c, "Label").Label("Label column").Bold().Width(120)
					ui.Text(c, "Forward").Bold().Grow(1)
					ui.Text(c, "Status").Bold().Width(160)
					ui.Text(c, "Connections").Bold().Width(90)
					ui.Box(c).Width(tunnelActionsW)
				})
				for _, x := range mine {
					if f := a.buildTunnelRow(c, id, v, x); f != nil {
						act = f
					}
				}
			})
		}
		ui.Text(c, "While a tunnel runs, any program on this computer can use its local port, and any program on the server can use a remote tunnel’s port.").TextColor(th.TextMuted)
	})
	if v.dlg != nil {
		a.buildTunnelDialog(c, id, v)
	}
	if act != nil {
		act()
	}
}

// buildTunnelRow is one tunnel; its buttons are named after its label (or
// its forward), so each row's Start is its own.
func (a *App) buildTunnelRow(c *ui.Context, tab string, v *tunnelsTab, x hubapi.TunnelView) (act func()) {
	th := c.Theme()
	id, tunnel, server := x.ID, x.Tunnel, v.server
	summary := model.DisplayText(model.TunnelSummary(x.Tunnel))
	label := model.DisplayText(x.Label)
	name := label
	if name == "" {
		name = summary
	}
	live := model.TunnelLive(x.Status)
	status := model.DisplayText(x.Status)
	if status == "error" && x.Error != "" {
		status = model.DisplayText(x.Error)
	}
	conns := ""
	if x.Status == "running" {
		conns = strconv.Itoa(x.Conns)
	}
	tip := ""
	if live {
		tip = "Stop it first"
	}
	ui.Row(c.Key("tunnel-" + id)).Gap(8).Children(func() {
		ui.Text(c, label).Width(120)
		ui.Text(c, summary).Font("monospace").Grow(1)
		st := ui.Text(c, status).Width(160)
		if x.Status == "error" {
			st.TextColor(th.Danger)
		}
		ui.Text(c, conns).Width(90)
		ui.Row(c).Gap(6).Width(tunnelActionsW).Children(func() {
			if live {
				if ui.Button(c, "Stop").Label("Stop " + name).Clicked() {
					act = func() { a.tunnelsStop(server, id) }
				}
			} else if ui.Button(c, "Start").Label("Start " + name).Clicked() {
				act = func() { a.tunnelAct(tab, id, false) }
			}
			if ui.Button(c, "Edit").Label("Edit " + name).Tooltip(tip).Disabled(live).Clicked() {
				act = func() { v.dlg = &tunnelDialog{edit: &tunnel, form: model.FormFor(&tunnel)} }
			}
			if ui.Button(c, "Delete").Label("Delete " + name).Tooltip(tip).Disabled(live).Clicked() {
				act = func() { a.tunnelAct(tab, id, true) }
			}
		})
	})
	return act
}

// buildTunnelDialog is Add tunnel or Edit tunnel (Electron's TunnelDialog):
// Enter in a field saves, Esc and Cancel close, an error keeps it open.
func (a *App) buildTunnelDialog(c *ui.Context, id string, v *tunnelsTab) {
	th := c.Theme()
	dl := v.dlg
	title := "Add tunnel"
	if dl.edit != nil {
		title = "Edit tunnel"
	}
	submit, cancel := false, false
	field := func(label string, value *string, auto bool) {
		ui.Field(c, label, func() {
			in := ui.TextInput(c.Key(label), value)
			if auto {
				in.AutoFocus()
			}
			if in.Submitted() {
				submit = true
			}
		})
	}
	open := true
	m := ui.Modal(c.Key(fmt.Sprintf("tunnel-%p", dl)), &open, func() {
		ui.Column(c).Width(440).Gap(10).Padding(20).Children(func() {
			ui.Text(c, title).Bold().FontSize(16)
			ui.Text(c, "On "+model.DisplayText(v.server)+". Listens on 127.0.0.1 only.").TextColor(th.TextMuted)
			ui.Text(c, "Kind")
			ui.RadioGroup(c, func() {
				for _, k := range tunnelKinds {
					ui.Radio(c, &dl.form.Kind, k.kind, k.label).Tooltip(k.hint)
				}
			}).Row().Label("Kind")
			field("Listen port", &dl.form.ListenPort, true)
			if dl.form.Kind != "dynamic" {
				ui.Row(c).Gap(8).Children(func() {
					ui.Column(c).Grow(1).Children(func() { field("Target host", &dl.form.TargetHost, false) })
					ui.Column(c).Width(120).Children(func() { field("Target port", &dl.form.TargetPort, false) })
				})
			}
			field("Label", &dl.form.Label, false)
			if dl.err != "" {
				ui.Text(c, model.DisplayText(dl.err)).TextColor(th.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					cancel = true
				}
				if ui.PrimaryButton(c, "Save").Disabled(dl.busy).Clicked() {
					submit = true
				}
			})
		})
	}).Label(title)
	if m.Dismissed() {
		cancel = true
	}
	switch {
	case cancel && !dl.busy:
		v.dlg = nil
	case submit:
		a.saveTunnel(id)
	}
}
