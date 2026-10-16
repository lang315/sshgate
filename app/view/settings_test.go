package view

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// settingsApp opens Settings on a hub whose limit is limit, with servers.
func settingsApp(t *testing.T, limit string, servers []hubapi.Server) (*App, *tester, *fakeDoor, *clock) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, SoftLockLimit: limit}, servers: servers}
	a, tt, c, _ := newApp(t, d)
	tt.flush()
	d.resetCalls()
	clickT(t, tt, "Settings")
	if a.settings == nil {
		t.Fatal("Settings did not open")
	}
	return a, tt, d, c
}

func TestSettingsGearNeedsTheLimit(t *testing.T) {
	a, tt, _ := readyApp(t, nil) // status without softLockLimit
	clickT(t, tt, "Settings")
	if a.settings != nil {
		t.Fatal("Settings opened before the hub reported its limit")
	}
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true}}
	b, tt, _, _ := newApp(t, d)
	tt.flush()
	if b.Screen() != model.ScreenLocked {
		t.Fatalf("screen %v, want the lock screen", b.Screen())
	}
	if _, ok := tt.Find("Settings"); ok {
		t.Fatal("Settings shows on the lock screen")
	}
}

// SettingsDialog.test.ts: opened on the value in force, no notice, Save
// disabled and mouse-only, Cancel focused; Enter cancels.
func TestSettingsOpensOnTheLimitInForce(t *testing.T) {
	a, tt, d, c := settingsApp(t, "7d", nil)
	for _, s := range []string{"Auto-allow", "Keep running while locked", "7 days", model.LimitHelp("7d")} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if tt.HasText("Any program running as your user") || !tt.Focused("Cancel") {
		t.Fatalf("a notice shows, or Cancel lacks the focus: %q", tt.Texts())
	}
	pass(c, tt)
	clickT(t, tt, "Save")
	for i := 0; i < 10; i++ {
		tt.Key(0, ui.KeyTab)
		if tt.Focused("Save") {
			t.Fatal("Tab reached Save")
		}
	}
	tt.flush()
	if hasCall(d, "settings.setSoftLockLimit") {
		t.Fatal("Save stored an unchanged limit")
	}
	clickT(t, tt, "Cancel")
	tt.Frame()
	if a.settings != nil {
		t.Fatal("Cancel left the dialog")
	}
	clickT(t, tt, "Settings")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.settings != nil {
		t.Fatal("Enter did not cancel")
	}
}

func TestSettingsNoLimitWarnsRedAndWaits(t *testing.T) {
	box := autoBox()
	box.AutoAllow = &hubapi.AutoAllow{Forever: true}
	a, tt, d, c := settingsApp(t, "24h", []hubapi.Server{box})
	choose(t, tt, "24 hours (default)", "7 days")
	if !tt.HasText(model.LimitNotice("7d")+" On auto-allow now:") || count(tt.Texts(), "box") < 2 {
		t.Fatalf("7d: %q", tt.Texts())
	}
	pass(c, tt)
	choose(t, tt, "7 days", "No limit")
	if !tt.HasText(model.LimitNotice("none")+" On auto-allow now:") || !tt.HasText(model.LimitHelp("none")) {
		t.Fatalf("none: %q", tt.Texts())
	}
	clickT(t, tt, "Save") // the notice just changed: Save waits again
	tt.flush()
	if hasCall(d, "settings.setSoftLockLimit") {
		t.Fatal("Save worked right after the notice changed")
	}
	pass(c, tt)
	clickT(t, tt, "Save")
	tt.flush()
	if params(d, "settings.setSoftLockLimit") != `{"limit":"none"}` || a.settings != nil {
		t.Fatalf("calls %v %s", d.callList(), params(d, "settings.setSoftLockLimit"))
	}
	if got := d.callList(); got[len(got)-1] != "status" {
		t.Fatalf("calls %v: the save must read status after", got)
	}
}

func TestSettingsNoticeWithNoHostsAndALowerLimit(t *testing.T) {
	_, tt, d, c := settingsApp(t, "3d", nil)
	choose(t, tt, "3 days", "7 days")
	if !tt.HasText(model.LimitNotice("7d") + " No host is on auto-allow now. The limit applies from the next grant.") {
		t.Fatal(tt.Texts())
	}
	choose(t, tt, "7 days", "24 hours (default)")
	if tt.HasText("Any program running as your user") {
		t.Fatal("a lower limit shows the notice")
	}
	pass(c, tt)
	clickT(t, tt, "Save")
	tt.flush()
	if params(d, "settings.setSoftLockLimit") != `{"limit":"24h"}` {
		t.Fatal(d.callList())
	}
}

// An error stays in the dialog; Esc cancels, but not while a save runs.
func TestSettingsErrorStaysAndEscCancels(t *testing.T) {
	a, tt, d, c := settingsApp(t, "24h", nil)
	gate := make(chan struct{})
	d.set(func() {
		d.gates = map[string]chan struct{}{"settings.setSoftLockLimit": gate}
		d.errs = map[string]error{"settings.setSoftLockLimit": errors.New("Vault is locked; unlock it in the app")}
	})
	choose(t, tt, "24 hours (default)", "3 days")
	pass(c, tt)
	clickT(t, tt, "Save")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.settings == nil {
		t.Fatal("Esc closed the dialog while it saved")
	}
	close(gate)
	tt.flush()
	if a.settings == nil || !tt.HasText("Vault is locked; unlock it in the app") {
		t.Fatal(tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.settings != nil {
		t.Fatal("Esc did not cancel")
	}
}

// A lock closes Settings; unlocking does not bring it back.
func TestLockClosesSettings(t *testing.T) {
	a, tt, d, _ := settingsApp(t, "24h", nil)
	lockNow(t, a, tt, d)
	d.set(func() { d.status.Locked = false })
	a.Update(a.refresh)
	tt.flush()
	if a.Screen() != model.ScreenReady || a.settings != nil {
		t.Fatal("Settings outlived the lock")
	}
}

// Save's reply touches only the dialog it was made for: after a lock and an
// unlock, Settings opened again stays open when the old reply lands.
func TestLateSaveReplyLeavesANewSettingsAlone(t *testing.T) {
	a, tt, d, c := settingsApp(t, "24h", nil)
	gate := make(chan struct{})
	d.set(func() { d.gates = map[string]chan struct{}{"settings.setSoftLockLimit": gate} })
	choose(t, tt, "24 hours (default)", "3 days")
	pass(c, tt)
	clickT(t, tt, "Save")
	lockNow(t, a, tt, d)
	d.set(func() { d.status.Locked = false })
	a.Update(a.refresh)
	tt.flush()
	clickT(t, tt, "Settings")
	st := a.settings
	close(gate)
	tt.flush()
	if st == nil || a.settings != st {
		t.Fatal("the old Save's reply closed the Settings opened since")
	}
}
