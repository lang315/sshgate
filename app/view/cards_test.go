package view

import (
	"errors"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

// The timed chip, the AI button's count and the card's Stop follow the
// app's clock; the redraw asks the hub nothing.
func TestChipTicksFromTheAppClock(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Until: time.Date(2026, 10, 15, 12, 14, 12, 0, time.UTC).Format(time.RFC3339)}
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{s}}
	_, tt, c, _ := newApp(t, d)
	tt.flush()
	if _, ok := tt.Find("Stop auto-allow box"); !ok || !tt.HasText("Auto 15m") || !tt.HasText("auto 1") {
		t.Fatalf("texts %q", tt.Texts())
	}
	d.resetCalls()
	c.add(10 * time.Minute)
	tt.Frame()
	if !tt.HasText("Auto 5m") {
		t.Fatalf("the chip did not tick: %q", tt.Texts())
	}
	c.add(5 * time.Minute)
	tt.Frame()
	if _, ok := tt.Find("Stop auto-allow box"); ok || tt.HasText("Auto 0m") || tt.HasText("auto 1") {
		t.Fatalf("an ended grant still shows: %q", tt.Texts())
	}
	if got := d.callList(); len(got) != 0 {
		t.Fatalf("the clock called %v", got)
	}
}

func TestCardStopEndsTheGrant(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true, Paused: true}
	_, tt, d := readyApp(t, []hubapi.Server{s})
	if !tt.HasText("Auto paused") {
		t.Fatal(tt.Texts())
	}
	d.resetCalls()
	clickT(t, tt, "Stop auto-allow box")
	tt.flush()
	if params(d, "servers.setAutoAllow") != `{"mode":"off","server":"box"}` || !hasCall(d, "servers") {
		t.Fatalf("calls %v %s", d.callList(), params(d, "servers.setAutoAllow"))
	}
	d.set(func() { d.errs = map[string]error{"servers.setAutoAllow": errors.New("boom")} })
	clickT(t, tt, "Stop auto-allow box")
	tt.flush()
	if !tt.HasText("Could not stop auto-allow on box: boom") {
		t.Fatal(tt.Texts())
	}
}

// A stop whose reply lands behind the lock screen changes nothing there.
func TestStopReplyBehindTheLockChangesNothing(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true}
	a, tt, d := readyApp(t, []hubapi.Server{s})
	gate := make(chan struct{})
	d.set(func() {
		d.gates = map[string]chan struct{}{"servers.setAutoAllow": gate}
		d.errs = map[string]error{"servers.setAutoAllow": errors.New("Vault is locked; unlock it in the app")}
	})
	clickT(t, tt, "Stop auto-allow box")
	lockNow(t, a, tt, d)
	d.resetCalls()
	close(gate)
	tt.flush()
	if a.alert != "" || hasCall(d, "servers") {
		t.Fatalf("alert %q, calls %v after a reply behind the lock", a.alert, d.callList())
	}
}

func TestTabDotFollowsTheGrant(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true}
	a, tt, _ := readyApp(t, []hubapi.Server{s})
	a.Update(func() { a.tabs.Open("box") })
	tt.flush()
	if _, ok := tt.Find("auto-allow on"); !ok {
		t.Fatal("no auto-allow dot on box's tab")
	}
	note(t, a, tt, "autoAllow.off", map[string]string{"server": "box", "reason": "turned off"})
	if _, ok := tt.Find("auto-allow on"); ok {
		t.Fatal("the dot outlived the grant")
	}
}
