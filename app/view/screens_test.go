package view

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
)

func TestHubScreens(t *testing.T) {
	d := &fakeDoor{}
	c := &clock{t: time.Unix(1000, 0)}
	a := &App{Door: d, Now: c.now}
	tt := newTester(t, a)
	tt.Frame()
	if !tt.HasText("Starting the hub…") {
		t.Fatal(tt.Texts())
	}
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", Attempt: 2, In: 3 * time.Second}) })
	tt.flush()
	if !tt.HasText("Hub crashed. Restarting in 3 s (attempt 2).") || !tt.HasText("The vault will be locked again after the restart. Open terminals will end.") {
		t.Fatal(tt.Texts())
	}
	c.add(2 * time.Second)
	tt.Frame()
	if !tt.HasText("Restarting in 1 s") {
		t.Fatal(tt.Texts())
	}
	a.Update(func() { a.HubState(hubproc.State{Kind: "failed", Message: "the hub crashed 4 times in 60 s\nboom"}) })
	tt.flush()
	if !tt.HasText("The hub stopped") || !tt.HasText("the hub crashed 4 times in 60 s") || !tt.HasText("boom") {
		t.Fatal(tt.Texts())
	}
}

func TestCreateVault(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: false}, servers: []hubapi.Server{{Name: "old", User: "u", Host: "h", Port: 22}}}
	a, tt, _, _ := newApp(t, d)
	tt.flush()
	for _, s := range []string{"Create your vault", "The master password encrypts saved passwords and protects the host list.",
		"At least 8 characters (not yet)", "Both passwords match (not yet)", "The master password cannot be recovered.", "you'll confirm each key on the next connect.", "u@h:22"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	tt.Type("password1")
	tt.Key(0, ui.KeyTab)
	tt.Type("password1")
	tt.Frame()
	if tt.HasText("(not yet)") {
		t.Fatal("checks still failing")
	}
	d.mu.Lock()
	d.status = hubapi.Status{HasVault: true}
	d.mu.Unlock()
	if err := tt.Click("Create vault"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if d.params["vault.create"] != `{"password":"password1"}` || a.Screen() != "ready" {
		t.Fatal(d.params["vault.create"], a.Screen())
	}
}

func TestUnlockScreen(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true, StorePath: "/v/servers.json", AutoHosts: []string{"box", "db"}}, pw: "pw"}
	a, tt, _, _ := newApp(t, d)
	a.Update(func() { a.lockReason = "idle" })
	tt.flush()
	for _, s := range []string{"Unlock vault", "Locked after inactivity.", "AI auto-allow is still running on: box, db", "Stop auto-allow and lock", "Vault file /v/servers.json"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if tt.HasText("AI requests are refused until you unlock.") {
		t.Fatal("refusal line shown while hosts are on auto-allow")
	}
	tt.Key(0, ui.KeyEnter) // empty password: nothing
	tt.flush()
	for _, m := range d.callList() {
		if m == "unlock" {
			t.Fatal("unlock sent with an empty password")
		}
	}
	tt.Type("bad")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if !tt.HasText("wrong master password") || !tt.Focused("Master password") {
		t.Fatal(tt.Texts())
	}
	for _, s := range tt.Texts() {
		if s == "bad" {
			t.Fatal("the password shows as text")
		}
	}
	if err := tt.Click("Stop auto-allow and lock"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if d.params["lock"] != `{"stopAuto":true}` {
		t.Fatal(d.params["lock"])
	}
}

func TestUnlockRefusalLineWithoutAutoHosts(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true}}
	a, tt, _, _ := newApp(t, d)
	tt.Frame()
	if tt.HasText("AI requests are refused") {
		t.Fatal("shown with no lock reason (fresh start)")
	}
	a.Update(func() { a.lockReason = "manual" })
	tt.flush()
	if !tt.HasText("Open terminals stay connected while locked. AI requests are refused until you unlock.") || tt.HasText("Stop auto-allow and lock") || tt.HasText("Locked after inactivity.") {
		t.Fatal(tt.Texts())
	}
}

// Ruling P19: a click on a password field must leave it focused.
func TestClickKeepsPasswordFieldFocused(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true}}
	a, tt, _, _ := newApp(t, d)
	if err := tt.Click("Master password"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.Focused("Master password") {
		t.Fatal("unlock field lost focus on click")
	}
	tt.Type("pw1")
	tt.Frame()
	if a.password != "pw1" {
		t.Fatalf("typed text landed in %q", a.password)
	}

	d2 := &fakeDoor{status: hubapi.Status{HasVault: false}}
	a2, tt2, _, _ := newApp(t, d2)
	for _, f := range []struct {
		label string
		get   func() string
	}{{"New master password", func() string { return a2.newPw }}, {"Confirm master password", func() string { return a2.again }}} {
		if err := tt2.Click(f.label); err != nil {
			t.Fatal(err)
		}
		tt2.Frame()
		if !tt2.Focused(f.label) {
			t.Fatalf("%s lost focus on click", f.label)
		}
		tt2.Type("abc")
		tt2.Frame()
		if f.get() != "abc" {
			t.Fatalf("%s: typed text landed in %q", f.label, f.get())
		}
	}
}
