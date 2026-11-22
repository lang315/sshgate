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
	if ft != nil {
		st := ft.status // as the lock screen at start loaded it
		a.touch = &st
	}
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

func (f *fakeToucher) cancelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cancels)
}

// A Touch ID sheet nobody touched is closed when its reveal goes: else it
// holds the one helper slot (the lock screen's ask would get ErrBusy) and a
// late touch audits a showing nobody sees.
func TestEditorRevealTouchCancelledWhenDropped(t *testing.T) {
	for name, drop := range map[string]func(a *App, tt *tester, d *fakeDoor){
		"close": func(a *App, _ *tester, _ *fakeDoor) { a.closeSheet() }, // the sheet's Close is under the dialog; a hub stop or Esc path closes it
		"save":  func(a *App, tt *tester, _ *fakeDoor) { a.submitHost(a.server("box")) },
		"lock": func(a *App, tt *tester, d *fakeDoor) {
			d.set(func() { d.status.Locked = true })
			note(t, a, tt, "locked", map[string]string{"reason": "manual"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			gate := make(chan struct{})
			defer close(gate)
			ft := &fakeToucher{status: touchOn, revealVal: "late", revealGate: gate}
			a, tt, d, _ := revealBox(t, ft)
			clickT(t, tt, "Show Password")
			tt.flush()
			drop(a, tt, d)
			if n := ft.cancelCount(); n != 1 {
				t.Fatalf("Touch.Cancel calls = %d, want 1", n)
			}
		})
	}
}

func TestEditorRevealNoAutoTouchWhenOff(t *testing.T) {
	ft := &fakeToucher{status: model.TouchStatus{Supported: true, Available: true}}
	_, tt, _, _ := revealBox(t, ft)
	clickT(t, tt, "Show Password")
	tt.flush()
	if len(ft.reveals) != 0 || !tt.HasText("Master password") || tt.HasText("Use Touch ID") {
		t.Fatalf("Touch ID off: reveals %v, texts %q", ft.reveals, tt.Texts())
	}
}

// Typing the password while the Touch ID sheet is up closes the sheet and
// goes on with the typed one.
func TestEditorRevealTypedWhileTouchAsks(t *testing.T) {
	gate := make(chan struct{})
	ft := &fakeToucher{status: touchOn, revealVal: "late", revealGate: gate}
	_, tt, _, _ := revealBox(t, ft)
	clickT(t, tt, "Show Password")
	tt.flush()
	tt.Type("pw")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if ft.cancelCount() != 1 || !tt.HasText(boxValue) {
		t.Fatalf("cancels %d, texts %q", ft.cancelCount(), tt.Texts())
	}
	close(gate) // the sheet's late answer is dropped
	tt.flush()
	if tt.HasText("late") || !tt.HasText(boxValue) {
		t.Fatalf("a late Touch ID answer replaced the value: %q", tt.Texts())
	}
}

func TestEditorRevealShowsHiddenRunes(t *testing.T) {
	ft := &fakeToucher{status: touchOn, revealVal: "a​b"}
	_, tt, _, _ := revealBox(t, ft)
	clickT(t, tt, "Show Password")
	tt.flush()
	if want := model.DisplayText("a​b"); want == "a​b" || !tt.HasText(want) {
		t.Fatalf("want %q in %q", want, tt.Texts())
	}
}

func TestEditorRevealTouchErrorWording(t *testing.T) {
	for err, want := range map[error]string{
		touchid.ErrUnavailable: "Touch ID is not available right now. Type the master password.",
		touchid.ErrReset:       "Touch ID was turned off because your fingerprints changed or your master password is different. Type the master password, and turn Touch ID on again in Settings.",
	} {
		ft := &fakeToucher{status: touchOn, revealErr: err}
		_, tt, _, _ := revealBox(t, ft)
		clickT(t, tt, "Show Password")
		tt.flush()
		if !tt.HasText(want) || tt.HasText(err.Error()) {
			t.Errorf("%v: texts %q", err, tt.Texts())
		}
	}
}

// Clear marks the secret for removal: its shown value goes with it.
func TestEditorRevealHidesOnClear(t *testing.T) {
	_, tt, _, _ := revealBox(t, nil)
	clickT(t, tt, "Show Password")
	tt.Type("pw")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	clickT(t, tt, "Clear Password")
	if tt.HasText(boxValue) {
		t.Fatal("the value outlived Clear")
	}
}
