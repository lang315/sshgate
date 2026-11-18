package view

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
)

// TestShots writes the redesign's screenshots to $SSHGATE_SHOTS; skipped
// without it. The terminal shot is the tab-open state of a failed dial: the
// terminal plugin does not render in a Tester.
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
	until := time.Date(2026, 10, 15, 12, 25, 0, 0, time.UTC).Format(time.RFC3339)
	servers := []hubapi.Server{
		{Name: "build", User: "ci", Host: "10.0.0.5", Port: 22, HostKey: "k", HostKeyAlgo: "ssh-ed25519", AIVisible: true, AutoAllow: &hubapi.AutoAllow{Until: until}},
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
		a.Update(func() { a.openTab("db"); a.tabs.ShowHome() })
		tt.flush()
		name := "light"
		if dark {
			name = "dark"
		}
		save(tt, "hosts-"+name+".png")
		if !dark {
			continue
		}
		clickT(t, tt, "Audit")
		tt.flush()
		save(tt, "audit-dark.png")
		a.Update(func() { a.tabs.Active = a.tabs.Tabs[0].ID })
		tt.flush()
		save(tt, "terminal-dark.png")
	}
}
