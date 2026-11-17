package view

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/model"
	"github.com/lang315/sshgate/app/termconn"
)

// TestShots writes the redesign's screenshots to $SSHGATE_SHOTS; skipped
// without it. terminal-light/dark is the tab-open state of a failed dial;
// terminal-live-* feeds a real tab output (needs MYGO_GHOSTTY_VT).
func TestShots(t *testing.T) {
	dir := os.Getenv("SSHGATE_SHOTS")
	if dir == "" {
		t.Skip("set SSHGATE_SHOTS=<dir> to write screenshots")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ui.SetDefaultThemes(Themes())
	save := func(tt *tester, name string) {
		t.Helper()
		tt.Advance(time.Second) // past every fade
		tt.Frame()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	servers := []hubapi.Server{
		{Name: "build", User: "ci", Host: "10.0.0.5", Port: 22, HostKey: "k", HostKeyAlgo: "ssh-ed25519", AIVisible: true},
		{Name: "staging", User: "deploy", Host: "10.0.0.6", Port: 22, HostKey: "k", HostKeyAlgo: "ssh-ed25519", AIVisible: true, AutoAllow: &hubapi.AutoAllow{Forever: true, Paused: true}},
		{Name: "new-box", User: "root", Host: "10.0.0.7", Port: 22},
		{Name: "db", User: "pg", Host: "10.0.0.8", Port: 22, HostKey: "k", HostKeyAlgo: "ssh-ed25519"},
	}
	rec := execEntry(1, "systemctl restart app", "allowed")
	for _, dark := range []bool{false, true} {
		d := &fakeDoor{status: hubapi.Status{HasVault: true, StorePath: "/v/servers.json"}, servers: servers,
			pending: []hubapi.Request{reqN("r1", "build", "docker compose pull && docker compose up -d")}}
		d.audit = auditDoor(&[]hubapi.AuditEntry{rec, execEntry(2, "df -h", "denied")}, 200)
		a, tt, _, _ := newApp(t, d)
		a.Dial = (&dialRec{err: os.ErrClosed}).dial
		tt.SetDark(dark)
		tt.SetSize(1000, 640)
		a.Update(func() {
			a.servers[0].AutoAllow = &hubapi.AutoAllow{Until: a.Now().Add(25 * time.Minute).Format(time.RFC3339)}
			a.openTab("db")
			a.tabs.ShowHome()
		})
		tt.flush()
		name := "light"
		if dark {
			name = "dark"
		}
		save(tt, "hosts-"+name+".png")
		clickT(t, tt, "Audit")
		tt.flush()
		save(tt, "audit-"+name+".png")
		a.Update(func() { a.tabs.Active = a.tabs.Tabs[0].ID })
		tt.flush()
		save(tt, "terminal-"+name+".png")
	}
	for _, dark := range []bool{false, true} { // locked, one host on auto-allow
		d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true, StorePath: "/v/servers.json", AutoHosts: []string{"staging"}}}
		a, tt, _, _ := newApp(t, d)
		tt.SetDark(dark)
		tt.SetSize(1000, 640)
		a.Update(func() { a.lockReason = "manual" })
		tt.flush()
		name := "light"
		if dark {
			name = "dark"
		}
		save(tt, "unlock-"+name+".png")
	}
	for _, dark := range []bool{false, true} {
		d := &fakeDoor{status: hubapi.Status{HasVault: true, SoftLockLimit: "24h"}, servers: servers}
		a, tt, _, _ := newApp(t, d)
		a.Touch = &fakeToucher{status: model.TouchStatus{Supported: true, Available: true}}
		tt.SetDark(dark)
		tt.SetSize(1000, 640)
		tt.flush()
		clickT(t, tt, "Settings")
		tt.flush()
		a.Update(func() { a.settings.chosen = "none" })
		tt.flush()
		name := "light"
		if dark {
			name = "dark"
		}
		save(tt, "settings-"+name+".png")
	}

	sudo := reqN("r2", "staging", "apt-get upgrade -y")
	sudo.Sudo = true
	xfer := reqN("r3", "build", "")
	xfer.Command = ""
	xfer.Transfer = &hubapi.Transfer{Op: "upload", Root: "/Users/me/proj", Local: []string{"dist/app.tar.gz"}, Remote: []string{"/srv/releases/"}}
	for _, dark := range []bool{false, true} { // all three request kinds, the plain one's Allow enabled
		d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: servers, pending: []hubapi.Request{reqN("r1", "build", "docker compose pull"), sudo, xfer}}
		_, tt, c, _ := newApp(t, d)
		tt.SetDark(dark)
		tt.SetSize(1000, 1100)
		tt.flush()
		adv(tt, c, time.Second) // past the Allow delay
		name := "light"
		if dark {
			name = "dark"
		}
		save(tt, "ai-"+name+".png")
	}

	// Every other screen, light and dark. each builds the screen with the
	// shared fakes, then the theme and size are applied and it is saved.
	each := func(name string, w, h int, build func(dark bool) *tester) {
		for _, dark := range []bool{false, true} {
			tt := build(dark)
			tt.SetDark(dark)
			tt.SetSize(w, h)
			tt.flush()
			suffix := "-light.png"
			if dark {
				suffix = "-dark.png"
			}
			save(tt, name+suffix)
		}
	}
	editS := boxServer
	editS.AIVisible, editS.HasSuPassword = true, true
	each("editor", 1000, 1300, func(bool) *tester {
		_, tt, _ := editBox(t, editS)
		openAI(t, tt) // the lower fold first: opening one moves what is below it
		clickFold(t, tt, "Privilege escalation (su / sudo) · saved")
		return tt
	})
	each("editor-new", 1000, 900, func(bool) *tester {
		_, tt, _ := edReady(t, []hubapi.Server{boxServer})
		clickT(t, tt, "New host")
		return tt
	})
	each("import", 1000, 900, func(bool) *tester {
		_, tt, _ := importApp(t, scanRows)
		return tt
	})
	each("files", 1000, 640, func(bool) *tester {
		a, tt, _, _, sh, _ := jobsApp(t)
		upload(t, tt, sh, "/l/proj")
		note(t, a, tt, "files.planned", plannedNote("f1", 2, 1, 0))
		note(t, a, tt, "files.progress", map[string]any{"id": "f1", "file": "proj/a.txt", "done": 1024, "total": 2048})
		return tt
	})
	each("filejob-confirm", 1000, 640, func(bool) *tester {
		a, tt, _, c, sh, _ := jobsApp(t)
		upload(t, tt, sh, "/l/proj")
		note(t, a, tt, "files.planned", plannedNote("f1", 2, 1, 2, "proj/a.txt", "proj/sub/b.txt"))
		pass(c, tt)
		return tt
	})
	each("newfolder", 1000, 640, func(bool) *tester {
		_, tt, _, _, _, _ := jobsApp(t)
		clickT(t, tt, "New folder")
		return tt
	})
	remote := hubapi.TunnelView{Server: "box", Tunnel: hubapi.Tunnel{ID: "c3", Kind: "remote", ListenPort: 8080, TargetHost: "localhost", TargetPort: 3000, Label: "preview"}, Status: "stopped"}
	each("tunnels", 1000, 640, func(bool) *tester {
		a, tt, _, _ := tunnelsApp(t, pgTunnel("running", 2), socksTunnel("stopped"), remote)
		openTunnelsTab(t, a, tt)
		return tt
	})
	each("tunnel-dialog", 1000, 900, func(bool) *tester {
		a, tt, _, _ := tunnelsApp(t, pgTunnel("stopped", 0))
		openTunnelsTab(t, a, tt)
		clickT(t, tt, "Add tunnel")
		return tt
	})
	each("audit-detail", 1100, 900, func(bool) *tester {
		_, tt, _, _ := auditApp(t, execEntry(1, "systemctl restart app", "allowed"), execEntry(2, "df -h", "denied"))
		clickT(t, tt, "Audit")
		tt.flush()
		clickFold(t, tt, "df -h")
		return tt
	})
	each("hostkey-unknown", 1000, 640, func(bool) *tester {
		_, tt, _, _ := hkApp(t, &keyDial{answer: func(int) (*termconn.Conn, error) { return nil, unknownKey("SHA256:abc") }})
		clickOpenBox(t, tt)
		return tt
	})
	each("hostkey-mismatch", 1000, 640, func(bool) *tester {
		mis := hubapi.HostKey{Status: "hostKeyMismatch", Server: "box", Host: "127.0.0.1", Port: 2222, User: "test", Pinned: "SHA256:old", Presented: "SHA256:new"}
		_, tt, _, _ := hkApp(t, &keyDial{answer: func(int) (*termconn.Conn, error) { return nil, &termconn.OpenError{Status: mis.Status, Key: mis} }})
		clickOpenBox(t, tt)
		return tt
	})
	each("delete-confirm", 1000, 640, func(bool) *tester {
		_, tt, _ := openDelete(t, &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}})
		return tt
	})
	each("autoallow", 1000, 900, func(bool) *tester {
		_, tt, _, c := confirmApp(t, autoBox(), "15 min", "24h")
		pass(c, tt)
		return tt
	})
	each("hub-starting", 1000, 640, func(bool) *tester {
		a, tt, _ := readyApp(t, nil)
		a.Update(func() { a.HubState(hubproc.State{Kind: "starting"}) })
		return tt
	})
	each("hub-failed", 1000, 640, func(bool) *tester {
		a, tt, _ := readyApp(t, nil)
		a.Update(func() {
			a.HubState(hubproc.State{Kind: "failed", Message: "the hub crashed 3 times in 60 s\nsshgate: open /Users/me/.config/sshgate/servers.json: permission denied\nsshgate: another sshgate hub is already running\npanic: runtime error: invalid memory address"})
		})
		return tt
	})
	each("create-vault", 1000, 900, func(bool) *tester {
		kept := []hubapi.Server{{Name: "build", User: "ci", Host: "10.0.0.5", Port: 22}, {Name: "db", User: "pg", Host: "10.0.0.8", Port: 22}}
		a, tt, _, _ := newApp(t, &fakeDoor{status: hubapi.Status{HasVault: false}, servers: kept})
		a.Update(func() { a.newPw, a.again = "password1", "password2" })
		return tt
	})
	each("rename", 1000, 640, func(bool) *tester {
		_, tt, _, _, _, _ := jobsApp(t)
		clickT(t, tt, "proj")
		clickT(t, tt, "Rename")
		return tt
	})
	// The delete delay runs on the app clock (c), which save does not move,
	// so Delete stays disabled here.
	each("files-delete", 1000, 640, func(bool) *tester {
		a, tt, _, _, _, _ := jobsApp(t)
		clickT(t, tt, "proj")
		tt.Key(0, ui.KeyDelete)
		tt.flush()
		note(t, a, tt, "files.planned", plannedNote("f1", 3, 1, 0))
		return tt
	})
	each("hub-restarting", 1000, 640, func(bool) *tester {
		a, tt, _ := readyApp(t, nil)
		a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", Attempt: 1, In: 5 * time.Second}) })
		return tt
	})
	each("store-error", 1000, 640, func(bool) *tester {
		d := &fakeDoor{status: hubapi.Status{HasVault: true, StoreError: "the vault file failed its integrity check or could not be read; the app keeps the last good copy"}, servers: servers}
		_, tt, _, _ := newApp(t, d)
		return tt
	})
	// A real tab open on the fake hub: OpenCount counts every tab of the
	// server that has not exited, so the card's tile turns green.
	each("hosts-connected", 1000, 900, func(bool) *tester {
		a, tt, _, _, _ := tabApp(t)
		tt.SetSize(1000, 900)
		clickT(t, tt, "Open box")
		tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" }, "t1 open")
		clickT(t, tt, "Home")
		return tt
	})
	each("terminal-live", 1000, 640, func(bool) *tester {
		a, tt, _, _, m := tabApp(t)
		clickT(t, tt, "Open box")
		tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" }, "t1 open")
		hubNote(m, "term.data", map[string]any{"id": "t1", "data": []byte("test@box:~$ ls -la\r\ntotal 8\r\ndrwxr-xr-x 2 test test 4096 proj\r\ntest@box:~$ ")})
		tabsUntil(t, tt, func() bool { return strings.Contains(termText(a, "t1"), "proj") }, "output")
		return tt
	})
}
