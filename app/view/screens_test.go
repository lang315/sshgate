package view

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/model"
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

func TestUnlockCardCentred(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true}}
	_, tt, _, _ := newApp(t, d)
	tt.SetSize(900, 500)
	tt.flush()
	f, ok := tt.Find("Master password")
	if !ok {
		t.Fatal(tt.Texts())
	}
	if cx := f.X + f.W/2; cx < 449 || cx > 451 || f.W > 312 {
		t.Fatalf("field %+v: centre %v, width %v", f, cx, f.W)
	}
}

func TestCreateVaultErrorUnderFields(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: false}, servers: []hubapi.Server{{Name: "old", User: "u", Host: "h", Port: 22}}}
	a, tt, _, _ := newApp(t, d)
	a.Update(func() { a.createErr = "disk full" })
	tt.flush()
	match, ok1 := tt.Find("Both passwords match (not yet)")
	msg, ok2 := tt.Find("disk full")
	btn, ok3 := tt.Find("Create vault")
	if !ok1 || !ok2 || !ok3 {
		t.Fatal(tt.Texts())
	}
	warn, ok4 := tt.Find("old  u@h:22") // the kept-servers list sits below the error
	if !ok4 {
		t.Fatal(tt.Texts())
	}
	if msg.Y < match.Y+match.H || msg.Y+msg.H > btn.Y || msg.Y+msg.H > warn.Y {
		t.Fatalf("error %+v not directly under checks %+v (server row %+v, button %+v)", msg, match, warn, btn)
	}
}

func TestCardOnSurface(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	for _, dark := range []bool{false, true} {
		d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true}}
		_, tt, _, _ := newApp(t, d)
		tt.SetDark(dark)
		tt.SetSize(900, 500)
		tt.flush()
		tt.Advance(time.Second)
		tt.Frame()
		f, ok := tt.Find("Master password")
		title, ok2 := tt.Find("Unlock vault")
		if !ok || !ok2 {
			t.Fatal(tt.Texts())
		}
		card := grow(f, 24)
		// 8 px inside the card's left edge, level with the title (the card's top is above it)
		patch := ui.Rect{X: card.X + 8, Y: title.Y, W: 4, H: 4}
		if n := countNear(shot(tt), patch, palette(dark).Surface, 2); n != 16 {
			t.Fatalf("dark=%v: %d of 16 pixels on Surface", dark, n)
		}
	}
}

// The tallest unlock card (auto-allow box, Touch ID, an error) in the
// smallest window: password field and both buttons stay on screen, or the
// card scrolls and clicking Unlock still works.
func TestUnlockFitsAtMinimumWindow(t *testing.T) {
	ft := &fakeToucher{status: touchOn}
	d := lockedDoor(ft)
	d.status.AutoHosts = []string{"box", "db"}
	a, tt, _ := touchApp(t, ft, d, false)
	tt.SetSize(900, 500)
	a.Update(func() { a.unlockErr = "wrong master password" })
	tt.flush()
	inside := func(label string) {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("%q missing: %q", label, tt.Texts())
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > 900 || r.Y+r.H > 500 {
			t.Fatalf("%q at %+v lies outside the 900x500 window", label, r)
		}
	}
	for _, l := range []string{"Master password", "Unlock", "Stop auto-allow and lock"} {
		inside(l)
	}
	tt.Type("pw")
	clickT(t, tt, "Unlock")
	tt.flush()
	if a.Screen() != model.ScreenReady {
		t.Fatal("Unlock did not unlock")
	}
}

// Four kept servers make the create-vault card taller than the minimum
// window: the fields and the button must be reachable, scrolling if need be.
func TestCreateVaultFitsAtMinimumWindow(t *testing.T) {
	var kept []hubapi.Server
	for _, n := range []string{"a", "b", "c", "d"} {
		kept = append(kept, hubapi.Server{Name: n, User: "u", Host: n + ".h", Port: 22})
	}
	d := &fakeDoor{status: hubapi.Status{HasVault: false}, servers: kept}
	a, tt, _, _ := newApp(t, d)
	tt.SetSize(900, 500)
	tt.flush()
	reach(t, tt, "Create vault", 900, 500)
	reach(t, tt, "New master password", 900, 500)
	tt.Type("password1")
	reach(t, tt, "Confirm master password", 900, 500)
	tt.Key(0, ui.KeyTab)
	tt.Type("password1")
	tt.Frame()
	d.mu.Lock()
	d.status = hubapi.Status{HasVault: true}
	d.mu.Unlock()
	reach(t, tt, "Create vault", 900, 500)
	if err := tt.Click("Create vault"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if d.params["vault.create"] != `{"password":"password1"}` || a.Screen() != "ready" {
		t.Fatal(d.params["vault.create"], a.Screen())
	}
}

func TestHubFailedFitsAtMinimumWindow(t *testing.T) {
	a, tt, _, _ := newApp(t, &fakeDoor{status: hubapi.Status{HasVault: true}})
	tt.SetSize(900, 500)
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("stderr line %d", i))
	}
	a.Update(func() { a.HubState(hubproc.State{Kind: "failed", Message: "boom\n" + strings.Join(lines, "\n")}) })
	tt.flush()
	reach(t, tt, "stderr line 40", 900, 500)
}

// reach scrolls the element labelled label into view and fails when it still
// lies outside a w x h window.
func reach(t *testing.T, tt *tester, label string, w, h float32) {
	t.Helper()
	for _, n := range tt.Nodes() {
		if n.Label == label {
			tt.ScrollIntoView(n.ID)
			break
		}
	}
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("%q missing: %q", label, tt.Texts())
	}
	if r.Y+r.H > h || r.H == 0 { // a disabled control takes no scroll-into-view
		tt.Move(w/2, h/2)
		tt.Scroll(w/2, h/2, 0, 20000)
		tt.Frame()
		r, _ = tt.Find(label)
	}
	if r.X < 0 || r.Y < 0 || r.X+r.W > w || r.Y+r.H > h || r.W == 0 || r.H == 0 {
		t.Fatalf("%q at %+v not reachable in %vx%v", label, r, w, h)
	}
}

// Muted text on WaitTint is 4.18:1 in dark, so the auto-allow sentence on the
// unlock screen uses Text: none of its pixels is Muted, some are Text.
func TestUnlockWaitSentenceNotMuted(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	for _, dark := range []bool{false, true} {
		d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true, AutoHosts: []string{"box"}}}
		_, tt, _, _ := newApp(t, d)
		tt.SetDark(dark)
		tt.SetSize(900, 700)
		tt.flush()
		tt.Advance(time.Second)
		tt.Frame()
		r, ok := tt.Find("AI commands on these hosts run without asking while the vault is locked.")
		if !ok {
			t.Fatal(tt.Texts())
		}
		p := palette(dark)
		img := shot(tt)
		if n := countNear(img, r, p.Muted, 3); n != 0 {
			t.Fatalf("dark=%v: %d Muted pixels in the sentence", dark, n)
		}
		if n := countNear(img, r, p.Text, 3); n == 0 {
			t.Fatalf("dark=%v: no Text pixels in the sentence at %+v", dark, r)
		}
	}
}

func TestHubFailedLook(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	a, tt, _, _ := newApp(t, &fakeDoor{status: hubapi.Status{HasVault: true}})
	tt.SetDark(true)
	tt.SetSize(900, 500)
	a.Update(func() { a.HubState(hubproc.State{Kind: "failed", Message: "boom\nline one\nline two"}) })
	tt.flush()
	tt.Advance(time.Second)
	tt.Frame()
	p := palette(true)
	f, ok := tt.Find("The hub stopped")
	if !ok {
		t.Fatal(tt.Texts())
	}
	shield := ui.Rect{X: 450 - 20, Y: f.Y - 12 - 40, W: 40, H: 40}
	img := shot(tt)
	if countNear(img, shield, p.Danger, 6) == 0 || countNear(img, shield, p.Accent, 6) != 0 {
		t.Fatalf("shield: danger %d accent %d", countNear(img, shield, p.Danger, 6), countNear(img, shield, p.Accent, 6))
	}
	r, ok := tt.Find("line one")
	if !ok {
		t.Fatal(tt.Texts())
	}
	if n := countNear(img, ui.Rect{X: r.X - 4, Y: r.Y, W: 2, H: r.H}, p.TermBg, 3); n < int(r.H)*2-2 {
		t.Fatalf("stderr not on TermBg (%d)", n)
	}
}

// The starting and restarting screens show mygo's Spinner: local drawing,
// no hub call.
func TestHubStartingShowsSpinner(t *testing.T) {
	d := &fakeDoor{}
	c := &clock{t: time.Unix(1000, 0)}
	a := &App{Door: d, Now: c.now}
	tt := newTester(t, a)
	tt.Frame()
	sp, ok := tt.AccessByRole(ui.RoleProgress, "")
	if !ok {
		t.Fatal("starting: no spinner")
	}
	before, calls := shot(tt), len(d.callList())
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if sameRegion(before, shot(tt), sp.Bounds) || len(d.callList()) != calls {
		t.Fatalf("spinner did not turn by itself, or called the hub: %v", d.callList())
	}
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", Attempt: 1, In: 3 * time.Second}) })
	tt.flush()
	if _, ok := tt.AccessByRole(ui.RoleProgress, ""); !ok {
		t.Fatal("restarting: no spinner")
	}
}
