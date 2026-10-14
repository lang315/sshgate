package view

import (
	"context"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

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
	})
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
		a.Update(func() {
			a.busy = false
			if err != nil {
				a.err = err.Error()
				return
			}
			a.password = ""
			if len(servers) == 0 || a.OpenTab == nil {
				a.err = "no server to open"
				return
			}
			t, err := a.OpenTab(servers[0].Name)
			if err != nil {
				a.err = err.Error()
				return
			}
			a.term = t
		})
	}()
}
