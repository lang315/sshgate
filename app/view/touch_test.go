package view

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/model"
	"github.com/lang315/sshgate/app/touchid"
)

// fakeToucher is Touch ID without a finger: a status, and what Unlock and
// Enable answer. An Unlock that fails with ErrReset turns it off, as the
// real one deletes touchid.json.
type fakeToucher struct {
	mu          sync.Mutex
	status      model.TouchStatus
	statusCalls int
	unlocks     int
	res         touchid.Result
	unlockErr   error
	onUnlock    func() // runs inside Unlock: flips the fake hub to unlocked
	enabled     []string
	enableErr   error
	disabled    int
	cancels     []error
}

func (f *fakeToucher) Status(context.Context) model.TouchStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls++
	return f.status
}

func (f *fakeToucher) Enable(_ context.Context, pw string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled = append(f.enabled, pw)
	if f.enableErr == nil {
		f.status.Enabled = true
	}
	return f.enableErr
}

func (f *fakeToucher) Disable() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disabled++
	f.status.Enabled = false
	return nil
}

func (f *fakeToucher) Unlock(context.Context) (touchid.Result, error) {
	f.mu.Lock()
	f.unlocks++
	res, err, on := f.res, f.unlockErr, f.onUnlock
	if errors.Is(err, touchid.ErrReset) {
		f.status.Enabled = false
	}
	f.mu.Unlock()
	if on != nil && err == nil && !res.Cancelled {
		on()
	}
	return res, err
}

func (f *fakeToucher) Cancel(reason error) {
	f.mu.Lock()
	f.cancels = append(f.cancels, reason)
	f.mu.Unlock()
}

func (f *fakeToucher) count() (unlocks, statuses int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.unlocks, f.statusCalls
}

var touchOn = model.TouchStatus{Supported: true, Available: true, Enabled: true}

// touchApp is newApp with Touch ID; focused tells the app its window has the
// focus before the hub runs, as at start.
func touchApp(t *testing.T, ft *fakeToucher, d *fakeDoor, focused bool) (*App, *tester, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	a := &App{Door: d, Now: c.now, Shell: &fakeShell{}, Touch: ft}
	tt := newTester(t, a)
	if focused {
		a.WindowFocus(true)
	}
	running(a, tt)
	return a, tt, c
}

// lockedDoor is a hub that is locked with password pw; the toucher unlocks it.
func lockedDoor(ft *fakeToucher) *fakeDoor {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true, StorePath: "/v/servers.json", SoftLockLimit: "24h"}, pw: "pw"}
	ft.onUnlock = func() { d.set(func() { d.status.Locked = false }) }
	return d
}

func hasButton(tt *tester, name string) bool { _, ok := tt.Find(name); return ok }

// The Touch ID button shows when it is on and usable (Unlock.test.ts).
func TestUnlockScreenTouchButton(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   model.TouchStatus
		want bool
	}{
		{"on and usable", touchOn, true},
		{"off", model.TouchStatus{Supported: true, Available: true}, false},
		{"on but not usable now", model.TouchStatus{Supported: true, Enabled: true}, false},
		{"not supported", model.TouchStatus{}, false},
	} {
		ft := &fakeToucher{status: tc.st}
		_, tt, _ := touchApp(t, ft, lockedDoor(ft), false)
		if got := hasButton(tt, "Unlock with Touch ID"); got != tc.want {
			t.Errorf("%s: button %v", tc.name, got)
		}
		if n, _ := ft.count(); n != 0 {
			t.Errorf("%s: asked Touch ID without the focus", tc.name)
		}
	}
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true}}
	_, tt, _, _ := newApp(t, d) // an app with no Touch ID at all
	if hasButton(tt, "Unlock with Touch ID") {
		t.Error("a button without Touch ID")
	}
}

func TestTouchButtonUnlocks(t *testing.T) {
	ft := &fakeToucher{status: touchOn, res: touchid.Result{Reply: hubapi.UnlockReply{RanWhileLocked: []hubapi.RanLocked{{Server: "box", Count: 2}}}}}
	a, tt, _ := touchApp(t, ft, lockedDoor(ft), false)
	a.Update(func() { a.lockReason, a.unlockErr = "idle", "an old error" })
	tt.flush()
	clickT(t, tt, "Unlock with Touch ID")
	tt.flush()
	if n, _ := ft.count(); n != 1 {
		t.Fatalf("Unlock called %d times", n)
	}
	if a.Screen() != model.ScreenReady || a.UnlockErr() != "" || a.LockReason() != "" {
		t.Fatalf("screen %s, error %q, reason %q", a.Screen(), a.UnlockErr(), a.LockReason())
	}
	if len(a.ranLocked) != 1 || a.ranLocked[0].Server != "box" {
		t.Errorf("ranLocked %+v: the hub's reply was dropped", a.ranLocked)
	}
}

// A cancel shows nothing and leaves the button; the file stays.
func TestTouchCancelShowsNothing(t *testing.T) {
	ft := &fakeToucher{status: touchOn, res: touchid.Result{Cancelled: true}}
	a, tt, _ := touchApp(t, ft, lockedDoor(ft), false)
	clickT(t, tt, "Unlock with Touch ID")
	tt.flush()
	if a.Screen() != model.ScreenLocked || a.UnlockErr() != "" || !hasButton(tt, "Unlock with Touch ID") {
		t.Fatalf("screen %s, error %q, texts %q", a.Screen(), a.UnlockErr(), tt.Texts())
	}
	clickT(t, tt, "Unlock with Touch ID") // the button still works
	tt.flush()
	if n, _ := ft.count(); n != 2 {
		t.Errorf("Unlock called %d times", n)
	}
}

// An error shows on the unlock screen, and the status is read again
// because a reset deleted the file: the button goes.
func TestTouchErrorShowsAndStatusIsReadAgain(t *testing.T) {
	ft := &fakeToucher{status: touchOn, unlockErr: touchid.ErrReset}
	a, tt, _ := touchApp(t, ft, lockedDoor(ft), false)
	_, before := ft.count()
	clickT(t, tt, "Unlock with Touch ID")
	tt.flush()
	if !tt.HasText(touchid.ErrReset.Error()) || a.Screen() != model.ScreenLocked {
		t.Fatalf("texts %q", tt.Texts())
	}
	if _, after := ft.count(); after != before+1 {
		t.Errorf("status read %d times, want %d", after, before+1)
	}
	if hasButton(tt, "Unlock with Touch ID") {
		t.Error("the button stayed after a reset")
	}
	fill(t, tt, "Master password", "pw") // the password still unlocks
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if a.Screen() != model.ScreenReady {
		t.Fatalf("screen %s, texts %q", a.Screen(), tt.Texts())
	}
}

// The auto-prompt: at start with the focus, then on a focus while locked;
// once per locked episode; never at the moment of locking (shell.test.ts).
func TestTouchAsksAtStartWithTheFocus(t *testing.T) {
	ft := &fakeToucher{status: touchOn}
	a, tt, _ := touchApp(t, ft, lockedDoor(ft), true)
	tt.flush()
	if n, _ := ft.count(); n != 1 || a.Screen() != model.ScreenReady {
		t.Fatalf("asked %d times, screen %s", n, a.Screen())
	}
}

func TestTouchAsksOnTheFirstFocusOfAnEpisodeOnly(t *testing.T) {
	ft := &fakeToucher{status: touchOn, res: touchid.Result{Cancelled: true}}
	a, tt, _ := touchApp(t, ft, lockedDoor(ft), false)
	tt.flush()
	if n, _ := ft.count(); n != 0 {
		t.Fatalf("asked %d times before any focus", n)
	}
	a.Update(func() { a.WindowFocus(true) })
	tt.flush()
	if n, _ := ft.count(); n != 1 {
		t.Fatalf("a focus asked %d times", n)
	}
	for i := 0; i < 2; i++ { // a cancel counts as asked: it hands the focus back and must not ask again
		a.Update(func() { a.WindowFocus(false); a.WindowFocus(true) })
		tt.flush()
	}
	if n, _ := ft.count(); n != 1 {
		t.Fatalf("asked %d times in one episode", n)
	}
}

func TestTouchNeverAsksAtTheMomentOfLockingAndAsksAgainNextEpisode(t *testing.T) {
	ft := &fakeToucher{status: touchOn}
	d := &fakeDoor{status: hubapi.Status{HasVault: true, SoftLockLimit: "24h", StorePath: "/v/servers.json"}, pw: "pw"}
	ft.onUnlock = func() { d.set(func() { d.status.Locked = false }) }
	a, tt, _ := touchApp(t, ft, d, true)
	tt.flush()
	if a.Screen() != model.ScreenReady {
		t.Fatalf("screen %s", a.Screen())
	}
	lockNow(t, a, tt, d)
	if n, _ := ft.count(); n != 0 {
		t.Fatalf("locking asked Touch ID %d times", n)
	}
	a.Update(func() { a.WindowFocus(false); a.WindowFocus(true) })
	tt.flush()
	if n, _ := ft.count(); n != 1 || a.Screen() != model.ScreenReady {
		t.Fatalf("a focus asked %d times, screen %s", n, a.Screen())
	}
	lockNow(t, a, tt, d) // a new episode
	a.Update(func() { a.WindowFocus(false); a.WindowFocus(true) })
	tt.flush()
	if n, _ := ft.count(); n != 2 {
		t.Fatalf("the next episode asked %d times in all", n)
	}
}

func TestTouchNeverAsksWhenOffOrNotUsable(t *testing.T) {
	for _, st := range []model.TouchStatus{{Supported: true, Available: true}, {Supported: true, Enabled: true}, {}} {
		ft := &fakeToucher{status: st}
		a, tt, _ := touchApp(t, ft, lockedDoor(ft), true)
		a.Update(func() { a.WindowFocus(true) })
		tt.flush()
		if n, _ := ft.count(); n != 0 {
			t.Errorf("%+v: asked %d times", st, n)
		}
	}
}

// A typed password that wins closes a Touch ID dialog still up; a hub that
// stops closes it with the reason shown on the next unlock screen.
func TestTouchCancelsOnATypedUnlockAndOnAHubStop(t *testing.T) {
	ft := &fakeToucher{status: touchOn}
	d := lockedDoor(ft)
	a, tt, _ := touchApp(t, ft, d, false)
	tt.Type("pw")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	ft.mu.Lock()
	got := slices.Clone(ft.cancels)
	ft.mu.Unlock()
	if a.Screen() != model.ScreenReady || len(got) != 1 || got[0] != nil {
		t.Fatalf("screen %s, cancels %v", a.Screen(), got)
	}
	a.Update(func() { a.HubState(hubprocStopped()) })
	tt.flush()
	ft.mu.Lock()
	got = slices.Clone(ft.cancels)
	ft.mu.Unlock()
	if len(got) != 2 || !errors.Is(got[1], touchid.ErrInterrupted) {
		t.Fatalf("cancels %v", got)
	}
}

// A wrong typed password cancels nothing.
func TestTouchKeepsAskingAfterAWrongTypedPassword(t *testing.T) {
	ft := &fakeToucher{status: touchOn}
	_, tt, _ := touchApp(t, ft, lockedDoor(ft), false)
	tt.Type("bad")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if len(ft.cancels) != 0 {
		t.Fatalf("cancels %v", ft.cancels)
	}
}

// Settings: an Unlock section before Auto-allow, only when Touch ID is
// supported (SettingsDialog.test.ts).
func touchSettings(t *testing.T, ft *fakeToucher) (*App, *tester, *fakeDoor, *clock) {
	t.Helper()
	d := &fakeDoor{status: hubapi.Status{HasVault: true, SoftLockLimit: "24h", StorePath: "/v/servers.json"}, pw: "pw"}
	a, tt, c := touchApp(t, ft, d, false)
	tt.flush()
	d.resetCalls()
	clickT(t, tt, "Settings")
	tt.flush()
	if a.settings == nil {
		t.Fatal("Settings did not open")
	}
	return a, tt, d, c
}

func TestSettingsUnlockSection(t *testing.T) {
	_, tt, _, _ := touchSettings(t, &fakeToucher{status: model.TouchStatus{Supported: true, Available: true}})
	texts := tt.Texts()
	u, au := slices.Index(texts, "Unlock"), slices.Index(texts, "Auto-allow")
	if u < 0 || au < 0 || u > au || !tt.HasText("Unlock with Touch ID") {
		t.Fatalf("texts %q", texts)
	}
	if tt.HasText("Touch ID is not available on this Mac right now.") || hasButton(tt, "Master password") {
		t.Fatalf("a notice or a password field shows with the switch off: %q", texts)
	}
	_, tt, _, _ = touchSettings(t, &fakeToucher{})
	if tt.HasText("Unlock") || tt.HasText("Unlock with Touch ID") {
		t.Fatalf("an unsupported Mac shows the section: %q", tt.Texts())
	}
	d := &fakeDoor{status: hubapi.Status{HasVault: true, SoftLockLimit: "24h"}}
	_, tt, _, _ = newApp(t, d) // an app with no Touch ID: Settings opens at once, as before
	tt.flush()
	clickT(t, tt, "Settings")
	if tt.HasText("Unlock with Touch ID") {
		t.Fatal("the section shows without Touch ID")
	}
}

// Not available: the switch is off and cannot be turned on; an enabled one
// can still be turned off.
func TestSettingsTouchUnavailable(t *testing.T) {
	a, tt, _, _ := touchSettings(t, &fakeToucher{status: model.TouchStatus{Supported: true}})
	if !tt.HasText("Touch ID is not available on this Mac right now.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	clickT(t, tt, "Unlock with Touch ID")
	if a.settings.touchOn || hasButton(tt, "Master password") {
		t.Fatal("the switch turned on")
	}
	a, tt, _, c := touchSettings(t, &fakeToucher{status: model.TouchStatus{Supported: true, Enabled: true}})
	if !a.settings.touchOn {
		t.Fatal("the switch is not on for an enabled Touch ID")
	}
	clickT(t, tt, "Unlock with Touch ID")
	if a.settings.touchOn {
		t.Fatal("an enabled Touch ID could not be turned off")
	}
	pass(c, tt)
}

// Turning it on asks the master password again; Save needs it, waits out the
// guard, and enrolls; the dialog closes and reopens with the switch on.
func TestSettingsTurnsTouchIDOn(t *testing.T) {
	ft := &fakeToucher{status: model.TouchStatus{Supported: true, Available: true}}
	a, tt, d, c := touchSettings(t, ft)
	clickT(t, tt, "Unlock with Touch ID")
	tt.Frame()
	if !tt.Focused("Master password") {
		t.Fatalf("the password field lacks the focus: %q", tt.Texts())
	}
	tt.Type("pw")
	tt.Frame()
	clickT(t, tt, "Save") // the field just appeared: the guard restarted
	tt.flush()
	if len(ft.enabled) != 0 {
		t.Fatal("Save worked right after the password field appeared")
	}
	pass(c, tt)
	clickT(t, tt, "Save")
	tt.flush()
	if len(ft.enabled) != 1 || ft.enabled[0] != "pw" || a.settings != nil {
		t.Fatalf("enabled %q, dialog open %v", ft.enabled, a.settings != nil)
	}
	if hasCall(d, "settings.setSoftLockLimit") {
		t.Fatal("Save stored a limit that did not change")
	}
	clickT(t, tt, "Settings") // reopened with no lock between: on, and nothing to save
	tt.flush()
	if !a.settings.touchOn {
		t.Fatal("the switch is off after enabling")
	}
	pass(c, tt)
	clickT(t, tt, "Save")
	tt.flush()
	if len(ft.enabled) != 1 || a.settings == nil {
		t.Fatal("Save with nothing changed did something")
	}
}

// Turning it on without typing the password leaves Save disabled.
func TestSettingsTouchOnNeedsThePassword(t *testing.T) {
	ft := &fakeToucher{status: model.TouchStatus{Supported: true, Available: true}}
	a, tt, _, c := touchSettings(t, ft)
	clickT(t, tt, "Unlock with Touch ID")
	pass(c, tt)
	clickT(t, tt, "Save")
	tt.flush()
	if len(ft.enabled) != 0 || a.settings == nil {
		t.Fatal("Save worked without the master password")
	}
}

// A Touch ID error stays in the dialog and the limit is not saved; Enter in
// the password field cancels.
func TestSettingsTouchErrorKeepsTheLimitUnsaved(t *testing.T) {
	ft := &fakeToucher{status: model.TouchStatus{Supported: true, Available: true}, enableErr: errors.New("wrong master password")}
	a, tt, d, c := touchSettings(t, ft)
	choose(t, tt, "24 hours (default)", "3 days")
	clickT(t, tt, "Unlock with Touch ID")
	tt.Type("nope")
	pass(c, tt)
	clickT(t, tt, "Save")
	tt.flush()
	if a.settings == nil || !tt.HasText("wrong master password") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if hasCall(d, "settings.setSoftLockLimit") {
		t.Fatal("the limit was saved after Touch ID failed")
	}
}

// Enter in the password field means Cancel, as everywhere in this dialog:
// Save is mouse-only, so a typed password is never sent by Enter.
func TestSettingsEnterInThePasswordFieldCancels(t *testing.T) {
	ft := &fakeToucher{status: model.TouchStatus{Supported: true, Available: true}}
	a, tt, _, c := touchSettings(t, ft)
	clickT(t, tt, "Unlock with Touch ID")
	tt.Frame()
	tt.Type("pw")
	pass(c, tt)
	if !tt.Focused("Master password") {
		t.Fatal("the password field lost the focus")
	}
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if a.settings != nil || len(ft.enabled) != 0 {
		t.Fatalf("dialog open %v, enabled %q", a.settings != nil, ft.enabled)
	}
}

// Both changed: Touch ID first, then the limit, then status.
func TestSettingsSavesTouchIDThenTheLimit(t *testing.T) {
	ft := &fakeToucher{status: model.TouchStatus{Supported: true, Available: true}}
	a, tt, d, c := touchSettings(t, ft)
	choose(t, tt, "24 hours (default)", "3 days")
	clickT(t, tt, "Unlock with Touch ID")
	tt.Type("pw")
	pass(c, tt)
	clickT(t, tt, "Save")
	tt.flush()
	if len(ft.enabled) != 1 || params(d, "settings.setSoftLockLimit") != `{"limit":"3d"}` || a.settings != nil {
		t.Fatalf("enabled %q, calls %v", ft.enabled, d.callList())
	}
}

// Turning it off needs no password and stores no limit.
func TestSettingsTurnsTouchIDOff(t *testing.T) {
	ft := &fakeToucher{status: touchOn}
	a, tt, d, c := touchSettings(t, ft)
	clickT(t, tt, "Unlock with Touch ID")
	if hasButton(tt, "Master password") {
		t.Fatal("turning off asks for the password")
	}
	pass(c, tt)
	clickT(t, tt, "Save")
	tt.flush()
	if ft.disabled != 1 || a.settings != nil || hasCall(d, "settings.setSoftLockLimit") {
		t.Fatalf("disabled %d, calls %v", ft.disabled, d.callList())
	}
}

// hubprocStopped is the state the supervisor reports when the hub is down.
func hubprocStopped() hubproc.State { return hubproc.State{Kind: "restarting"} }
