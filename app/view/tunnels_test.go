package view

import (
	"errors"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/model"
)

func pgTunnel(status string, conns int) hubapi.TunnelView {
	return hubapi.TunnelView{Server: "box", Tunnel: hubapi.Tunnel{ID: "a1", Kind: "local", ListenPort: 5433, TargetHost: "db", TargetPort: 5432, Label: "pg"}, Status: status, Conns: conns}
}

func socksTunnel(status string) hubapi.TunnelView {
	return hubapi.TunnelView{Server: "box", Tunnel: hubapi.Tunnel{ID: "b2", Kind: "dynamic", ListenPort: 1080}, Status: status}
}

// tunnelsApp is a ready app on box whose tunnels.list answers list, in a
// window tall enough for the dialog on any platform's fonts.
func tunnelsApp(t *testing.T, list ...hubapi.TunnelView) (*App, *tester, *fakeDoor, *clock) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}, tunnels: list}
	a, tt, c, _ := newApp(t, d)
	tt.SetSize(1000, 900)
	tt.flush()
	return a, tt, d, c
}

// openTunnelsTab clicks the card's Tunnels button.
func openTunnelsTab(t *testing.T, a *App, tt *tester) string {
	t.Helper()
	if a.tabs.Active != "" {
		clickT(t, tt, "Hosts")
	}
	clickT(t, tt, "Tunnels box")
	tt.flush()
	return a.tabs.Active
}

func tunnelState(t *testing.T, a *App, tt *tester, id, status string, conns int) {
	t.Helper()
	note(t, a, tt, "tunnels.state", map[string]any{"server": "box", "id": id, "status": status, "conns": conns})
}

// tunnels.spec.ts, the view's part: the card's button and badge, one tab
// per host, the rows, and Stop as a notification.
func TestTunnelsTabFromTheCard(t *testing.T) {
	a, tt, d, _ := tunnelsApp(t, pgTunnel("running", 2), socksTunnel("stopped"))
	if !tt.HasText("1 ⇄") {
		t.Fatalf("no running badge: %q", tt.Texts())
	}
	d.resetCalls()
	id := openTunnelsTab(t, a, tt)
	if tab, _ := a.tabs.Get(id); tab.Kind != model.TunnelsKind || tab.State != "open" || !tt.HasText("box · tunnels") {
		t.Fatalf("%+v %q", tab, tt.Texts())
	}
	for _, s := range []string{"Tunnels on box", "pg", "L 127.0.0.1:5433 → db:5432", "running", "2", "Stop pg", "D 127.0.0.1:1080 SOCKS5", "stopped", "Start D 127.0.0.1:1080 SOCKS5"} {
		if count(tt.Texts(), s) == 0 {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
	if len(d.callList()) != 0 {
		t.Fatalf("opening the tab called %v", d.callList())
	}
	clickT(t, tt, "Hosts")
	clickT(t, tt, "Tunnels box")
	if a.tabs.Active != id || len(a.tabs.Tabs) != 1 {
		t.Fatalf("a second tab: active %q, %d tabs", a.tabs.Active, len(a.tabs.Tabs))
	}
	clickT(t, tt, "Edit pg") // disabled while it runs
	clickT(t, tt, "Delete pg")
	tt.flush()
	if a.tunnelTabs[id].dlg != nil || len(d.callList()) != 0 {
		t.Fatalf("a running tunnel was edited or deleted: %v", d.callList())
	}
	clickT(t, tt, "Stop pg")
	tt.flush()
	if got := d.sentList(); !slices.Equal(got, []string{`tunnels.stop {"id":"a1","server":"box"}`}) || len(d.callList()) != 0 {
		t.Fatalf("sent %v, calls %v", got, d.callList())
	}
	tunnelState(t, a, tt, "a1", "stopped", 0)
	if !tt.HasText("Start pg") || tt.HasText("Stop pg") {
		t.Fatalf("after stopped: %q", tt.Texts())
	}
	clickT(t, tt, "Hosts")
	if tt.HasText("1 ⇄") {
		t.Fatal("the badge outlived the tunnel")
	}
	if a.tabs.OpenCount("box") != 0 || a.tabs.MostRecentFor("box") != "" {
		t.Fatal("a Tunnels tab counted as a terminal")
	}
}

func TestTunnelDialogChecksThenSaves(t *testing.T) {
	a, tt, d, _ := tunnelsApp(t)
	id := openTunnelsTab(t, a, tt)
	if !tt.HasText("No tunnels yet. Add one to reach a port on or through this host.") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Add tunnel")
	if !tt.Focused("Listen port") || !tt.HasText("On box. Listens on 127.0.0.1 only.") {
		t.Fatalf("focus or note: %q", tt.Texts())
	}
	d.resetCalls()
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if !tt.HasText("Listen port must be 1-65535") || hasCall(d, "tunnels.save") {
		t.Fatalf("an empty port: %q %v", tt.Texts(), d.callList())
	}
	tt.Type("5433")
	fill(t, tt, "Target port", "5432")
	fill(t, tt, "Label", "pg")
	d.set(func() {
		d.errs = map[string]error{"tunnels.save": errors.New("another tunnel already listens on local port 5433")}
	})
	clickT(t, tt, "Save")
	tt.flush()
	if !tt.HasText("another tunnel already listens on local port 5433") || a.tunnelTabs[id].dlg == nil {
		t.Fatalf("the hub's refusal: %q", tt.Texts())
	}
	d.set(func() {
		d.errs = nil
		d.tunnels = []hubapi.TunnelView{pgTunnel("stopped", 0)}
	})
	d.resetCalls()
	clickT(t, tt, "Label")
	tt.Key(0, ui.KeyEnter) // Enter in a field saves
	tt.flush()
	if got := params(d, "tunnels.save"); got != `{"server":"box","tunnel":{"id":"","kind":"local","listenPort":5433,"targetHost":"localhost","targetPort":5432,"label":"pg"}}` {
		t.Fatal(got)
	}
	if a.tunnelTabs[id].dlg != nil || !hasCall(d, "tunnels.list") || !tt.HasText("Start pg") {
		t.Fatalf("after the save: %v %q", d.callList(), tt.Texts())
	}

	clickT(t, tt, "Add tunnel")
	clickT(t, tt, "Dynamic")
	if tt.HasText("Target host") || tt.HasText("Target port") {
		t.Fatal("a dynamic tunnel asks for a target")
	}
	fill(t, tt, "Listen port", "1080")
	d.resetCalls()
	clickT(t, tt, "Save")
	tt.flush()
	if got := params(d, "tunnels.save"); got != `{"server":"box","tunnel":{"id":"","kind":"dynamic","listenPort":1080,"targetHost":"","targetPort":0,"label":""}}` {
		t.Fatal(got)
	}

	clickT(t, tt, "Add tunnel")
	tt.Key(0, ui.KeyEscape)
	tt.flush()
	if a.tunnelTabs[id].dlg != nil {
		t.Fatal("Esc left the dialog open")
	}
}

func TestTunnelEditStartAndDelete(t *testing.T) {
	pg := pgTunnel("error", 0)
	pg.Error = "port 5433 is already in use"
	a, tt, d, _ := tunnelsApp(t, pg)
	id := openTunnelsTab(t, a, tt)
	if !tt.HasText("port 5433 is already in use") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Edit pg")
	if !tt.HasText("Edit tunnel") || a.tunnelTabs[id].dlg.form != (model.TunnelForm{Kind: "local", ListenPort: "5433", TargetHost: "db", TargetPort: "5432", Label: "pg"}) {
		t.Fatalf("%q", tt.Texts())
	}
	fill(t, tt, "Listen port", "6543")
	d.resetCalls()
	clickT(t, tt, "Save")
	tt.flush()
	if got := params(d, "tunnels.save"); got != `{"server":"box","tunnel":{"id":"a1","kind":"local","listenPort":6543,"targetHost":"db","targetPort":5432,"label":"pg"}}` {
		t.Fatal(got)
	}

	d.set(func() { d.errs = map[string]error{"tunnels.start": errors.New("connection failed")} })
	d.resetCalls()
	clickT(t, tt, "Start pg")
	tt.flush()
	if params(d, "tunnels.start") != `{"id":"a1","server":"box"}` || !tt.HasText("connection failed") || hasCall(d, "tunnels.list") {
		t.Fatalf("%s %q %v", params(d, "tunnels.start"), tt.Texts(), d.callList())
	}
	d.set(func() { d.errs, d.tunnels = nil, nil })
	d.resetCalls()
	clickT(t, tt, "Delete pg")
	tt.flush()
	if got := d.callList(); len(got) != 2 || got[0] != "tunnels.delete" || got[1] != "tunnels.list" || tt.HasText("connection failed") {
		t.Fatalf("calls %v, %q", got, tt.Texts())
	}
	if !tt.HasText("No tunnels yet. Add one to reach a port on or through this host.") {
		t.Fatal(tt.Texts())
	}
}

// App.tsx's tunnelBuffers: a state that lands while tunnels.list is in
// flight is applied to its older snapshot.
func TestTunnelStatesDuringAListAreReplayed(t *testing.T) {
	gate := make(chan struct{})
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}, tunnels: []hubapi.TunnelView{pgTunnel("stopped", 0)},
		gates: map[string]chan struct{}{"tunnels.list": gate}}
	a, tt, _, _ := newApp(t, d)
	tunnelState(t, a, tt, "a1", "running", 3)
	close(gate)
	tt.flush()
	if len(a.tunnels) != 1 || a.tunnels[0].Status != "running" || a.tunnels[0].Conns != 3 || len(a.tunnelBufs) != 0 {
		t.Fatalf("%+v, %d buffers left", a.tunnels, len(a.tunnelBufs))
	}
}

// A tunnel is the hub's: it outlives its tab and a lock, its states keep
// coming behind the lock, and the unlock reads the list again. A hub stop
// ends them all; the tab stays.
func TestTunnelsAcrossCloseLockAndHubStop(t *testing.T) {
	a, tt, d, _ := tunnelsApp(t, pgTunnel("running", 0))
	d.pw = "pw"
	id := openTunnelsTab(t, a, tt)
	clickT(t, tt, "Close tab")
	if len(d.sentList()) != 0 || a.tunnelTabs[id] != nil || model.RunningTunnels(a.tunnels, "box") != 1 {
		t.Fatalf("closing the tab sent %v", d.sentList())
	}
	id = openTunnelsTab(t, a, tt)
	clickT(t, tt, "Add tunnel")
	d.set(func() { d.status.Locked = true })
	note(t, a, tt, "locked", map[string]string{"reason": "manual"})
	d.resetCalls()
	tunnelState(t, a, tt, "a1", "running", 4)
	if a.Screen() != model.ScreenLocked || a.tunnels[0].Conns != 4 || len(d.callList()) != 0 {
		t.Fatalf("behind the lock: %s %+v %v", a.Screen(), a.tunnels, d.callList())
	}
	d.set(func() { d.tunnels = []hubapi.TunnelView{pgTunnel("running", 5)} })
	clickT(t, tt, "Master password")
	tt.Type("pw")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if a.Screen() != model.ScreenReady || !hasCall(d, "tunnels.list") || a.tunnels[0].Conns != 5 || a.tunnelTabs[id].dlg == nil || a.tabs.Active != id {
		t.Fatalf("after the unlock: %s %v %+v", a.Screen(), d.callList(), a.tunnels)
	}
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", Attempt: 1}) })
	tt.flush()
	if a.tunnels != nil || a.tunnelTabs[id].dlg != nil {
		t.Fatalf("after a hub stop: %+v", a.tunnels)
	}
	if tab, ok := a.tabs.Get(id); !ok || tab.State != "open" {
		t.Fatalf("%+v", tab)
	}
}

// A Tunnels tab is not a terminal: the editor's close warning ignores it.
func TestEditorWarningIgnoresTunnelsTabs(t *testing.T) {
	a, tt, _, _ := tunnelsApp(t)
	tt.SetSize(800, 1200)
	openTunnelsTab(t, a, tt)
	clickT(t, tt, "Hosts")
	clickT(t, tt, "Edit box")
	fill(t, tt, "Port", "2222")
	if !tt.HasText("Changing host or port forgets the host key and saved passwords unless you re-enter them.") || tt.HasText("Saving will") {
		t.Fatal(tt.Texts())
	}
}

// AutoAllowDialog.test.ts:104-109 and the tunnels half of :153-158: the
// dialog lists the host's running remote tunnels, and a change to that
// list restarts Enable's delay.
func TestAutoAllowListsRemoteTunnels(t *testing.T) {
	remote := func(id string, port, to int, status string) hubapi.TunnelView {
		return hubapi.TunnelView{Server: "box", Tunnel: hubapi.Tunnel{ID: id, Kind: "remote", ListenPort: port, TargetHost: "localhost", TargetPort: to}, Status: status}
	}
	d := &fakeDoor{status: hubapi.Status{HasVault: true, SoftLockLimit: "24h"}, servers: []hubapi.Server{autoBox()},
		tunnels: []hubapi.TunnelView{remote("a1", 2222, 22, "running"), remote("b2", 3333, 80, "running"), remote("c3", 4444, 1, "stopped"), socksTunnel("running")}}
	a, tt, c, _ := newApp(t, d)
	tt.SetSize(800, 1200)
	tt.flush()
	clickT(t, tt, "Edit box")
	openAI(t, tt)
	choose(t, tt, "Off", "15 min")
	clickT(t, tt, "Save")
	tt.flush()
	if a.editor == nil || a.editor.confirm == nil {
		t.Fatal("no dialog")
	}
	for _, s := range []string{"Remote tunnels running on this host reach your machine:", "R server 127.0.0.1:2222 → localhost:22", "R server 127.0.0.1:3333 → localhost:80"} {
		if count(tt.Texts(), s) != 1 {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
	if tt.HasText("4444") || tt.HasText("SOCKS5") {
		t.Fatal("a stopped or dynamic tunnel listed")
	}
	pass(c, tt)
	tunnelState(t, a, tt, "b2", "stopped", 0)
	if tt.HasText("3333") {
		t.Fatal("a stopped tunnel still listed")
	}
	clickT(t, tt, "Enable")
	tt.flush()
	if hasCall(d, "servers.save") {
		t.Fatal("Enable worked right after the list changed")
	}
	pass(c, tt)
	clickT(t, tt, "Enable")
	tt.flush()
	if !hasCall(d, "servers.save") {
		t.Fatal("Enable did not work once the delay passed")
	}
}
