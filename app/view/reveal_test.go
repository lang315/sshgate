package view

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
	"github.com/lang315/sshgate/app/touchid"
)

// revealBox is editBox on a hub with master password "pw" and a clock the
// test moves; ft is nil for an app without Touch ID.
func revealBox(t *testing.T, ft *fakeToucher) (*App, *tester, *fakeDoor, *clock) {
	t.Helper()
	d := &fakeDoor{status: hubapi.Status{HasVault: true, StorePath: "/v/servers.json"}, servers: []hubapi.Server{boxServer}, pw: "pw"}
	c := &clock{t: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
	a := &App{Door: d, Now: c.now, Shell: &fakeShell{}}
	if ft != nil {
		a.Touch = ft
	}
	tt := newTester(t, a)
	running(a, tt)
	tt.flush()
	tt.SetSize(800, 900)
	tt.Frame()
	clickT(t, tt, "Edit box")
	return a, tt, d, c
}

const boxValue = "shown-box-password"

func TestEditorRevealTyped(t *testing.T) {
	a, tt, d, c := revealBox(t, nil)
	clickT(t, tt, "Show Password")
	if !tt.HasText("Master password") || !tt.Focused("Master password") {
		t.Fatalf("dialog: %q", tt.Texts())
	}
	tt.Type("pw")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if !tt.HasText(boxValue) || a.editor.reveal == nil {
		t.Fatalf("value not shown: %q", tt.Texts())
	}
	if got := params(d, "servers.reveal"); got != `{"field":"password","password":"pw","server":"box"}` {
		t.Errorf("sent %s", got)
	}
	if tt.HasText("Master password") {
		t.Error("the dialog stayed open")
	}
	c.add(revealHide)
	tt.Advance(revealHide)
	tt.Frame()
	if tt.HasText(boxValue) {
		t.Fatal("the value outlived revealHide")
	}
}

func TestEditorRevealWrongPassword(t *testing.T) {
	_, tt, _, _ := revealBox(t, nil)
	clickT(t, tt, "Show Password")
	tt.Type("nope")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if !tt.HasText("Master password") || !tt.HasText("wrong master password") || tt.HasText(boxValue) {
		t.Fatalf("after a wrong password: %q", tt.Texts())
	}
}

func TestEditorRevealTouch(t *testing.T) {
	ft := &fakeToucher{status: touchOn, revealVal: "touch-value"}
	a, tt, d, _ := revealBox(t, ft)
	clickT(t, tt, "Show Password")
	tt.flush()
	if !tt.HasText("touch-value") || len(ft.reveals) != 1 || ft.reveals[0] != "box/password" {
		t.Fatalf("touch reveal: %q, %v", tt.Texts(), ft.reveals)
	}
	if hasCall(d, "servers.reveal") {
		t.Error("the app called servers.reveal itself on the Touch ID path")
	}

	// A cancelled sheet leaves the dialog for the password, with no error.
	ft.revealErr, ft.revealVal = touchid.ErrCancelled, ""
	a.editor.reveal = nil
	tt.Frame()
	clickT(t, tt, "Show Password")
	tt.flush()
	if !tt.HasText("Master password") || tt.HasText(touchid.ErrCancelled.Error()) {
		t.Fatalf("after a cancel: %q", tt.Texts())
	}
}

func TestEditorRevealHidesOnLock(t *testing.T) {
	a, tt, d, _ := revealBox(t, nil)
	clickT(t, tt, "Show Password")
	tt.Type("pw")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if !tt.HasText(boxValue) {
		t.Fatal("not shown")
	}
	d.set(func() { d.status.Locked = true })
	note(t, a, tt, "locked", map[string]string{"reason": "manual"})
	if tt.HasText(boxValue) || a.editor != nil {
		t.Fatal("the value outlived the lock")
	}
}

func TestEditorRevealNotInInput(t *testing.T) {
	a, tt, d, _ := revealBox(t, nil)
	clickT(t, tt, "Show Password")
	tt.Type("pw")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if a.editor.draft.Secrets[model.SecretPassword].Value != "" {
		t.Fatal("the value went into the input")
	}
	clickT(t, tt, "Save")
	tt.flush()
	if got := params(d, "servers.save"); strings.Contains(got, boxValue) || strings.Contains(got, `"password":`) {
		t.Fatalf("save sent %s", got)
	}
}

func TestEditorNoShowWithoutSaved(t *testing.T) {
	s := boxServer
	s.HasPassword = false
	a, tt, _ := editBox(t, s)
	if _, ok := tt.Find("Show Password"); ok {
		t.Error("Show for a host with no saved password")
	}
	a.closeSheet()
	tt.Frame()
	clickT(t, tt, "New host")
	if _, ok := tt.Find("Show Password"); ok {
		t.Error("Show for a new host")
	}
	_, tt, _ = editBox(t, boxServer)
	if !tt.HasText("Leave a field empty to keep it.") || tt.HasText("Saved secrets are never shown. Leave a field empty to keep it.") {
		t.Errorf("hint: %q", tt.Texts())
	}
}

// The value goes at the locked note itself, before the status reply that
// drops the editor: a slow hub must not leave it on screen.
func TestEditorRevealDropsAtTheLockedNote(t *testing.T) {
	a, tt, d, _ := revealBox(t, nil)
	clickT(t, tt, "Show Password")
	tt.Type("pw")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	gate := make(chan struct{})
	d.set(func() { d.status.Locked = true; d.gates = map[string]chan struct{}{"status": gate} })
	defer close(gate)
	b, _ := json.Marshal(map[string]string{"reason": "manual"})
	a.Update(func() { a.Note("locked", b) })
	tt.flushOnce()
	tt.Frame()
	if tt.HasText(boxValue) {
		t.Fatal("the value is still drawn while status is in flight")
	}
}
