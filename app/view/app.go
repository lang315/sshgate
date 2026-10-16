package view

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/termconn"
)

// TerminalOptions wires a hub terminal into mygo's terminal: any real key,
// paste or input-method commit marks the connection's writes as the user's
// (the hub's idle lock counts only those).
func TerminalOptions(conn *termconn.Conn) terminal.Options {
	return terminal.Options{
		Conn:     conn,
		OnKey:    func(_ ui.Modifiers, _ ui.Key) bool { conn.MarkUser(); return false },
		OnPaste:  func(string) bool { conn.MarkUser(); return false },
		OnInput:  func(string) { conn.MarkUser() },
		OpenLink: func(string) bool { return true }, // slice 0 opens no links; the confirm comes in slice 2
	}
}

// Door is the part of the UI door client the views call.
type Door interface {
	Call(ctx context.Context, method string, params, result any) error
}

// App is the window's state. Build runs on the main thread; Update runs a
// function there from any goroutine (Window.Update, or a test's).
type App struct {
	Door    Door
	Update  func(func())
	Now     func() time.Time
	OpenTab func(server string) (*terminal.Terminal, error)

	password string
	busy     bool
	err      string
	hub      string // the supervisor's last word when the hub is not running
	term     *terminal.Terminal
}

func (a *App) Build(c *ui.Context) {
	if a.term != nil {
		terminal.View(c, a.term).Fill().AutoFocus()
		return
	}
	ui.Column(c).Fill().Center().Gap(12).Children(func() {
		ui.Text(c, "sshgate is locked")
		in := ui.TextInput(c, &a.password).Password().Label("Master password").AutoFocus().Disabled(a.busy)
		if in.Submitted() || ui.PrimaryButton(c, "Unlock").Disabled(a.busy).Clicked() {
			a.unlock()
		}
		if a.err != "" {
			ui.Text(c, a.err)
		}
		// The hub's last stderr lines come with a crash loop, one per line.
		for _, line := range strings.Split(a.hub, "\n") {
			if line != "" {
				ui.Text(c, line)
			}
		}
	})
}

// HubState shows what the supervisor reports: a failed hub (a protocol
// other than 12 included) is fatal and says why; a restart says which
// attempt. Run it on the main thread, as a.Update does.
func (a *App) HubState(st hubproc.State) {
	switch st.Kind {
	case "failed":
		a.hub = st.Message
	case "restarting":
		a.hub = fmt.Sprintf("hub restarting (attempt %d)", st.Attempt)
	default:
		a.hub = ""
	}
}

// unlock runs off the main thread: Argon2id takes a while.
func (a *App) unlock() {
	pw := a.password
	a.busy, a.err = true, ""
	go func() {
		ctx := context.Background()
		err := a.Door.Call(ctx, "unlock", map[string]string{"password": pw}, nil)
		var servers []struct{ Name string }
		if err == nil {
			err = a.Door.Call(ctx, "servers", nil, &servers)
		}
		// OpenTab dials SSH: keep it off the main thread.
		var t *terminal.Terminal
		if err == nil && len(servers) > 0 && a.OpenTab != nil {
			t, err = a.OpenTab(servers[0].Name)
		}
		a.Update(func() {
			a.busy = false
			if err != nil {
				a.err = err.Error()
				return
			}
			a.password = ""
			if t == nil {
				a.err = "no server to open"
				return
			}
			a.term = t
		})
	}()
}
