package view

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
	"github.com/lang315/sshgate/app/termconn"
)

// termTab is a tab's terminal. term and conn stay nil until term.open
// succeeds; h is the terminal view's focus handle (mygo restores no focus).
type termTab struct {
	term *terminal.Terminal
	conn *termconn.Conn
	h    ui.Handle
}

// openTab adds a tab for server, activates it and dials it.
func (a *App) openTab(server string) string {
	id := a.tabs.Open(server)
	a.terms[id] = &termTab{}
	a.dial(id, server, nil)
	return id
}

// dial runs term.open off the main thread; the reply comes back through
// Update, where opened checks that its hub and its tab are still there.
func (a *App) dial(id, server string, trust *hubapi.Trust) {
	d := a.Dial // read here: the goroutine must not race a later assignment
	if d == nil {
		a.fail(id, "the hub is not running")
		return
	}
	gen := a.hubGen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		conn, err := d(ctx, id, server, trust)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("hub did not answer in %d s", int(callTimeout.Seconds()))
		}
		a.Update(func() { a.opened(gen, id, conn, err, trust != nil) })
	}()
}

func (a *App) opened(gen int, id string, conn *termconn.Conn, err error, trusted bool) {
	t, ok := a.tabs.Get(id)
	if gen != a.hubGen || !ok || t.State != "opening" {
		if conn != nil {
			go conn.Close() // its tab closed, or its hub stopped, while it opened
		}
		return
	}
	var oe *termconn.OpenError
	switch {
	case errors.As(err, &oe) && oe.Status == "hostKeyUnknown":
		a.prompts.Ask(id, oe.Key) // the tab stays opening until the person answers
		return
	case errors.As(err, &oe) && oe.Status == "hostKeyMismatch":
		k := oe.Key
		a.mismatch = &k
		a.fail(id, "host key mismatch")
		return
	case err != nil:
		a.fail(id, err.Error())
		return
	}
	term, err := terminal.New(TerminalOptions(conn, func() { a.Update(func() { a.exited(id, conn.ExitReason()) }) }))
	if err != nil {
		go conn.Close()
		a.fail(id, err.Error())
		return
	}
	tab := a.terms[id]
	tab.term, tab.conn = term, conn
	a.tabs.Opened(id)
	go func() { // Send to tab pastes once the remote printed something
		select {
		case <-conn.Output():
			a.Update(func() { a.tabs.Output(id); a.flushSends(id) })
		case <-term.Done():
		}
	}()
	if trusted {
		a.reloadServers() // the card's New key goes
	}
	if a.tabs.Active == id {
		tab.h.Focus()
	}
}

// fail ends a tab whose open failed; it has no terminal, so its page
// shows the reason.
func (a *App) fail(id, msg string) { a.exited(id, "open failed: "+msg) }

// exited marks a tab ended; the terminal already shows why (termconn).
func (a *App) exited(id, reason string) {
	if t, ok := a.tabs.Get(id); !ok || t.State == "exited" {
		return
	}
	a.tabs.Exited(id, reason)
	a.prompts.Drop(id)
	a.flushSends(id)
}

func (a *App) activate(id string) {
	a.tabs.Activate(id)
	a.focusActive()
}

// focusActive focuses the active tab's terminal, if it has one, or its
// file table.
func (a *App) focusActive() {
	if t := a.terms[a.tabs.Active]; t != nil && t.term != nil {
		t.h.Focus()
	}
	if v := a.files[a.tabs.Active]; v != nil {
		v.ls.Handle.Focus()
	}
}

// closeTab removes a tab; its terminal closes off the main thread
// (term.close may wait up to 5 s on a hung hub). An opening tab's late
// reply is closed by opened.
func (a *App) closeTab(id string) {
	a.tabs.Close(id)
	a.prompts.Drop(id)
	if t := a.terms[id]; t != nil && t.term != nil {
		go t.term.Close()
	}
	delete(a.terms, id)
	delete(a.files, id)
	a.flushSends(id)
	a.focusActive()
}

// endTabs ends every live terminal tab when the hub stops: its terminals
// died with it. The tabs stay, showing why, with Reconnect. Files tabs stay
// open (endFiles).
func (a *App) endTabs() {
	a.endFiles()
	for _, t := range a.tabs.Tabs {
		if t.State == "exited" || t.Kind == model.FilesKind {
			continue
		}
		a.tabs.Exited(t.ID, "hub restarted")
		if tt := a.terms[t.ID]; tt != nil && tt.conn != nil {
			tt.conn.End("hub restarted")
		}
	}
}

// buildTabStrip draws one button per tab, with Reconnect once it exited and
// a close button. Clicks run after the loop: closing a tab while ranging
// over the tabs would build one twice.
func (a *App) buildTabStrip(c *ui.Context) {
	th := c.Theme()
	addr, auto := map[string]string{}, map[string]bool{}
	for _, s := range a.servers {
		addr[s.Name] = model.Addr(s)
	}
	for _, n := range model.AutoHostNames(a.servers, a.Now()) {
		auto[n] = true // on or paused: Electron's .autodot
	}
	var click func()
	ui.ScrollHorizontal(c).Grow(1).Gap(6).Children(func() {
		for _, t := range a.tabs.Tabs {
			id, server := t.ID, t.Server
			name := model.DisplayText(server)
			tip := name
			if s, ok := addr[server]; ok {
				tip = model.DisplayText(s)
			}
			label := name
			switch {
			case t.Kind == model.FilesKind:
				label += " · files"
			case t.State == "exited":
				label += " · exited"
			}
			ui.Row(c.Key("tab-" + id)).Gap(2).Children(func() {
				if auto[server] {
					ui.Text(c, "\u25cf").Label("auto-allow on").TextColor(th.Warning)
				}
				if ui.Button(c, label).Selected(a.tabs.Active == id).Tooltip(tip).Clicked() {
					click = func() { a.activate(id) }
				}
				if t.State == "exited" && ui.Button(c, "Reconnect").Clicked() {
					click = func() { a.closeTab(id); a.openTab(server) }
				}
				if ui.Button(c, "×").Label("Close tab").Tooltip("Close").Clicked() {
					click = func() { a.closeTab(id) }
				}
			})
		}
	})
	if click != nil {
		click()
	}
}

// buildTab shows the active tab: its file browser, its terminal, or why it
// has none.
func (a *App) buildTab(c *ui.Context, t model.Tab) {
	if t.Kind == model.FilesKind {
		a.buildFiles(c, t)
		return
	}
	tt := a.terms[t.ID]
	if tt == nil || tt.term == nil {
		if t.State == "exited" {
			ui.Column(c).Padding(12).Children(func() {
				ui.Text(c, "[exited: "+model.DisplayText(t.ExitReason)+"]").Font("monospace")
			})
		}
		return
	}
	// The chord is claimed around the terminal only: a window shortcut would
	// take the key from the remote wherever the focus is.
	box := ui.Box(c).Fill().Children(func() {
		terminal.View(c.Key("term-"+t.ID), tt.term).Fill().Label("Terminal " + model.DisplayText(t.Server)).Bind(&tt.h)
	})
	if box.Shortcut(approvalsChord(runtime.GOOS), ui.KeyA) {
		a.focusApprovals()
	}
}
