package view

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/model"
	"github.com/lang315/sshgate/app/termconn"
)

func TestForgetUnpinsAndKeepsTheEditorOpen(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	if !tt.HasText("Takes effect immediately.") {
		t.Fatal(tt.Texts())
	}
	unpinned := boxServer
	unpinned.HostKey, unpinned.HostKeyAlgo = "", ""
	d.set(func() { d.servers = []hubapi.Server{unpinned} })
	d.resetCalls()
	clickT(t, tt, "Forget host key")
	tt.flush()
	if got := d.callList(); len(got) != 2 || got[0] != "servers.forgetHostKey" || got[1] != "servers" || params(d, "servers.forgetHostKey") != `{"name":"box"}` {
		t.Fatalf("calls %v %s", got, params(d, "servers.forgetHostKey"))
	}
	if a.editor == nil || !tt.HasText("Not pinned. You will be asked to confirm it on the next connect.") || tt.HasText("Forget host key") {
		t.Fatal(tt.Texts())
	}
}

func TestForgetErrorShowsInTheEditor(t *testing.T) {
	_, tt, d := editBox(t, boxServer)
	d.set(func() {
		d.errs = map[string]error{"servers.forgetHostKey": errors.New("Vault is locked; unlock it in the app")}
	})
	clickT(t, tt, "Forget host key")
	tt.flush()
	if !tt.HasText("Vault is locked; unlock it in the app") || !tt.HasText("Forget host key") {
		t.Fatal(tt.Texts())
	}
}

// A forget reply from a hub that has stopped since reads nothing again and
// reopens nothing.
func TestForgetReplyAfterAHubStopChangesNothing(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	gate := make(chan struct{})
	d.set(func() { d.gates = map[string]chan struct{}{"servers.forgetHostKey": gate} })
	clickT(t, tt, "Forget host key")
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", In: time.Second}) })
	tt.flush()
	d.resetCalls()
	close(gate)
	tt.flush()
	if a.editor != nil || a.alert != "" || hasCall(d, "servers") {
		t.Fatalf("editor %v, alert %q, calls %v after the dead hub's reply", a.editor != nil, a.alert, d.callList())
	}
}

// lockNow locks the hub under the app, as a lock from elsewhere does.
func lockNow(t *testing.T, a *App, tt *tester, d *fakeDoor) {
	t.Helper()
	d.set(func() { d.status.Locked = true })
	note(t, a, tt, "locked", map[string]string{"reason": "manual"})
	if a.Screen() != model.ScreenLocked {
		t.Fatal(a.Screen())
	}
}

// A forget reply that lands behind the lock screen asks the locked hub for
// nothing.
func TestForgetReplyAfterALockChangesNothing(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	gate := make(chan struct{})
	d.set(func() { d.gates = map[string]chan struct{}{"servers.forgetHostKey": gate} })
	clickT(t, tt, "Forget host key")
	lockNow(t, a, tt, d)
	d.resetCalls()
	close(gate)
	tt.flush()
	if a.editor != nil || a.alert != "" || hasCall(d, "servers") {
		t.Fatalf("editor %v, alert %q, calls %v after a reply behind the lock", a.editor != nil, a.alert, d.callList())
	}
}

// openDelete shows box's delete confirm on the Hosts tab.
func openDelete(t *testing.T, d *fakeDoor) (*App, *tester, *clock) {
	a, tt, c, _ := newApp(t, d)
	tt.flush()
	clickT(t, tt, "Delete box")
	return a, tt, c
}

func TestDeleteWaitsHalfASecondThenDeletes(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}}
	a, tt, c := openDelete(t, d)
	if !tt.HasText("Delete box? Its saved passwords and host key are removed and its open tabs close.") || !tt.Focused("Cancel") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Delete host")
	tt.flush()
	if hasCall(d, "servers.delete") || a.del == nil {
		t.Fatal("Delete worked before 500 ms")
	}
	c.add(600 * time.Millisecond)
	tt.Frame()
	for i := range 5 {
		tt.Key(0, ui.KeyTab)
		if tt.Focused("Delete host") {
			t.Fatalf("Tab %d reached Delete host", i+1)
		}
	}
	d.resetCalls()
	clickT(t, tt, "Delete host")
	tt.flush()
	if got := d.callList(); len(got) != 2 || got[0] != "servers.delete" || got[1] != "servers" || params(d, "servers.delete") != `{"name":"box"}` {
		t.Fatalf("calls %v", got)
	}
	if a.del != nil || !tt.Focused("Search hosts") {
		t.Fatal("the confirm stays, or the focus went nowhere")
	}
}

func TestDeleteConfirmCancelsWithEnterAndEsc(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}}
	a, tt, c, _ := newApp(t, d)
	tt.flush()
	for _, key := range []ui.Key{ui.KeyEnter, ui.KeyEscape} {
		clickT(t, tt, "Delete box")
		c.add(600 * time.Millisecond)
		tt.Key(0, key)
		tt.flush()
		if a.del != nil || hasCall(d, "servers.delete") {
			t.Fatalf("key %v deleted, or left the confirm open", key)
		}
	}
}

func TestDeleteRefusalAlerts(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}, errs: map[string]error{"servers.delete": errors.New(`server "box" not found`)}}
	_, tt, c := openDelete(t, d)
	c.add(600 * time.Millisecond)
	tt.Frame()
	clickT(t, tt, "Delete host")
	tt.flush()
	if !tt.HasText(`server "box" not found`) || !hasCall(d, "servers") {
		t.Fatal(tt.Texts())
	}
}

// A delete refusal from a hub that has stopped since alerts nothing and
// reads nothing again.
func TestDeleteReplyAfterAHubStopChangesNothing(t *testing.T) {
	gate := make(chan struct{})
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer},
		gates: map[string]chan struct{}{"servers.delete": gate}, errs: map[string]error{"servers.delete": errors.New(`server "box" not found`)}}
	a, tt, c := openDelete(t, d)
	c.add(600 * time.Millisecond)
	tt.Frame()
	clickT(t, tt, "Delete host")
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", In: time.Second}) })
	tt.flush()
	d.resetCalls()
	close(gate)
	tt.flush()
	if a.del != nil || a.alert != "" || hasCall(d, "servers") {
		t.Fatalf("confirm %v, alert %q, calls %v after the dead hub's reply", a.del != nil, a.alert, d.callList())
	}
}

// A delete refusal that lands behind the lock screen alerts nothing and
// asks the locked hub for nothing.
func TestDeleteReplyAfterALockChangesNothing(t *testing.T) {
	gate := make(chan struct{})
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer},
		gates: map[string]chan struct{}{"servers.delete": gate}, errs: map[string]error{"servers.delete": errors.New(`server "box" not found`)}}
	a, tt, c := openDelete(t, d)
	c.add(600 * time.Millisecond)
	tt.Frame()
	clickT(t, tt, "Delete host")
	lockNow(t, a, tt, d)
	d.resetCalls()
	close(gate)
	tt.flush()
	if a.del != nil || a.alert != "" || hasCall(d, "servers") {
		t.Fatalf("confirm %v, alert %q, calls %v after a reply behind the lock", a.del != nil, a.alert, d.callList())
	}
}

// Untrusted text: the server name in the delete confirm shows only escaped.
func TestDeleteConfirmShowsTheNameEscaped(t *testing.T) {
	s := boxServer
	s.Name = "bo\u202ex"
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{s}}
	_, tt, _, _ := newApp(t, d)
	tt.flush()
	clickT(t, tt, `Delete bo\u202ex`)
	if !tt.HasText(`Delete bo\u202ex? Its saved passwords`) {
		t.Fatalf("missing the escaped name in %q", tt.Texts())
	}
	for _, x := range tt.Texts() {
		if strings.ContainsRune(x, '\u202e') {
			t.Fatalf("%q shown unescaped", x)
		}
	}
}

func TestMismatchOpensTheEditorOnForget(t *testing.T) {
	mis := hubapi.HostKey{Status: "hostKeyMismatch", Server: "box", Host: "127.0.0.1", Port: 2222, User: "test", Pinned: "SHA256:old", Presented: "SHA256:new"}
	k := &keyDial{answer: func(int) (*termconn.Conn, error) { return nil, &termconn.OpenError{Status: mis.Status, Key: mis} }}
	a, tt, _, d := hkApp(t, k)
	d.set(func() { d.servers[0].HostKey, d.servers[0].HostKeyAlgo = "SHA256:old", "ssh-ed25519" })
	a.Update(a.reloadServers)
	tt.flush()
	clickOpenBox(t, tt)
	if !tt.HasText("Open host editor") || !tt.Focused("Close") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Open host editor")
	tt.Frame()
	if a.mismatch != nil || a.editor == nil || a.editor.name != "box" || !tt.HasText("Edit box") {
		t.Fatal(tt.Texts())
	}
	if !tt.Focused("Forget host key") || tt.Focused("Address") {
		t.Fatal("Forget host key is not focused")
	}
}

// The editor opened from a mismatch focuses Forget once: a bad port or an
// Auth change above it must not re-mount it and hand it the focus, where
// the next Enter would forget the key.
func TestForgetTakesTheFocusOnlyWhenTheEditorOpens(t *testing.T) {
	a, tt, _ := edReady(t, []hubapi.Server{boxServer})
	a.Update(func() { a.openEditor("box", true) })
	tt.flush()
	if !tt.Focused("Forget host key") {
		t.Fatal("Forget host key is not focused")
	}
	clickT(t, tt, "Port")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("x")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Port must be 1-65535.") || !tt.Focused("Port") || tt.Focused("Forget host key") {
		t.Fatalf("a bad port moved the focus to Forget: %q", tt.Texts())
	}
	clickT(t, tt, "+ Key or agent")
	clickT(t, tt, "key")
	tt.Frame()
	if !tt.HasText("Key path") || !tt.Focused("key") || tt.Focused("Forget host key") {
		t.Fatalf("an Auth change moved the focus to Forget: %q", tt.Texts())
	}
}

// After a forget, the key pinned again (a trusted reconnect while the
// editor stays open) brings the button back without the focus.
func TestForgetComesBackUnfocusedWhenThePinDoes(t *testing.T) {
	a, tt, d := edReady(t, []hubapi.Server{boxServer})
	a.Update(func() { a.openEditor("box", true) })
	tt.flush()
	unpinned := boxServer
	unpinned.HostKey, unpinned.HostKeyAlgo = "", ""
	d.set(func() { d.servers = []hubapi.Server{unpinned} })
	clickT(t, tt, "Forget host key")
	tt.flush()
	if tt.HasText("Forget host key") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Address")
	d.set(func() { d.servers = []hubapi.Server{boxServer} })
	a.Update(a.reloadServers)
	tt.flush()
	if !tt.HasText("Forget host key") || !tt.Focused("Address") || tt.Focused("Forget host key") {
		t.Fatalf("the pin coming back moved the focus to Forget: %q", tt.Texts())
	}
}

// A save reply closes the editor, not a delete confirm opened while the
// save ran.
func TestLateSaveReplyKeepsTheDeleteConfirm(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	gate := make(chan struct{})
	d.set(func() { d.gates = map[string]chan struct{}{"servers.save": gate} })
	clickT(t, tt, "Save")
	clickT(t, tt, "Delete box")
	close(gate)
	tt.flush()
	if a.editor != nil || a.del == nil || !tt.HasText("Delete box? Its saved passwords and host key are removed and its open tabs close.") {
		t.Fatalf("editor open %v, confirm %v", a.editor != nil, a.del)
	}
}
