// Command sshgate-app is sshgate's desktop app on mygo: it runs sshgate hub
// and shows its UI door in native views.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/door"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/termconn"
	"github.com/lang315/sshgate/app/view"
)

func main() {
	packaged := mygo.App.IsPackaged()
	resources, err := mygo.App.Path(mygo.PathResources)
	if packaged && (err != nil || resources == "") {
		// "" would resolve to the cwd: the hub that gets the master password must not come from there
		log.Fatalf("cannot find the app's Resources directory: %v", err)
	}
	if err := prepareLibrary(packaged, resources); err != nil {
		log.Fatal(err)
	}
	exe, _ := os.Executable()
	repoRoot := filepath.Join(filepath.Dir(exe), "..", "..") // built to app/build/: the repo root, where `go build ./cmd/sshgate` puts sshgate
	launch := hubproc.LaunchFor(os.Environ(), packaged, resources, repoRoot, runtime.GOOS, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})

	mux := termconn.NewMux()
	var client atomic.Pointer[door.Client]
	a := &view.App{Now: time.Now}
	a.Door = doorFunc(func(ctx context.Context, method string, params, result any) error {
		c := client.Load()
		if c == nil {
			return errors.New("the hub is not running")
		}
		return c.Call(ctx, method, params, result)
	})
	var tabs atomic.Int64 // OpenTab runs off the main thread
	a.OpenTab = func(server string) (*terminal.Terminal, error) {
		c := client.Load()
		if c == nil {
			return nil, errors.New("the hub is not running")
		}
		conn, err := termconn.Open(context.Background(), c, mux, fmt.Sprintf("t%d", tabs.Add(1)), server, 24, 80, nil)
		if err != nil {
			return nil, err
		}
		t, err := terminal.New(view.TerminalOptions(conn))
		if err != nil {
			conn.Close()
			return nil, err
		}
		return t, nil
	}

	// Only the Edit shortcuts: the default menu's View has Reload, which
	// would drop terminals the hub still holds.
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{{Role: mygo.RoleAppMenu}, {Role: mygo.RoleEditMenu}}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	supDone := make(chan struct{})
	mygo.App.WhenReady(func() {
		w := mygo.NewWindow(mygo.WindowOptions{Title: "sshgate", Width: 1000, Height: 640, Content: ui.View(a.Build)})
		a.Update = w.Update
		a.Shell = newTrayShell(w)
		sup := &hubproc.Supervisor{
			Launch: launch,
			OnNote: func(method string, params json.RawMessage) {
				if mux.Note(method, params) {
					return
				}
				a.Update(func() { a.Note(method, params) })
			},
			OnProc: func(p *hubproc.Proc) { client.Store(p.Client()) },
			OnState: func(st hubproc.State) {
				if st.Kind == "failed" {
					log.Print(st.Message)
				}
				a.Update(func() { a.HubState(st) })
			},
		}
		go func() { defer close(supDone); sup.Run(ctx) }()
	})
	go func() { <-ctx.Done(); mygo.App.Quit() }() // SIGTERM quits like Cmd+Q
	err = mygo.App.Run()
	stop() // ends the supervisor, which stops the hub
	select {
	case <-supDone:
	case <-time.After(5 * time.Second):
	}
	if err != nil {
		log.Fatal(err)
	}
}

type doorFunc func(ctx context.Context, method string, params, result any) error

func (f doorFunc) Call(ctx context.Context, method string, params, result any) error {
	return f(ctx, method, params, result)
}

// prepareLibrary makes sure the terminal loads the libghostty-vt built from
// source: in a packaged app, only the one shipped in Resources with the
// embedded hash; unpackaged, only MYGO_GHOSTTY_VT, never a download.
func prepareLibrary(packaged bool, resources string) error {
	if !packaged {
		if os.Getenv("MYGO_GHOSTTY_VT") == "" {
			return errors.New("set MYGO_GHOSTTY_VT to a library built by scripts/build-ghostty-vt.sh")
		}
		return terminal.Load()
	}
	os.Unsetenv("MYGO_GHOSTTY_VT")
	path, err := terminal.LibraryPath()
	if err != nil {
		return err
	}
	if err := checkLibrary(path, resources, strings.TrimSpace(ghosttySHA)); err != nil {
		return err
	}
	return terminal.Load()
}
