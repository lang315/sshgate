package view

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// autoBox is box visible to AI and pinned: a grant can be armed on it.
func autoBox() hubapi.Server {
	s := boxServer
	s.AIVisible = true
	return s
}

// openAI opens the editor's AI access fold, whatever its title says now,
// and lets its opening animation end, so what it holds can be clicked.
func openAI(t *testing.T, tt *tester) {
	t.Helper()
	for _, x := range tt.Texts() {
		if strings.HasPrefix(x, "AI access · ") {
			clickT(t, tt, x)
			tt.flush()
			return
		}
	}
	t.Fatalf("no AI access fold in %q", tt.Texts())
}

// choose picks option in the select now showing cur: a click on mygo's
// select opens it, a click on an option chooses it.
func choose(t *testing.T, tt *tester, cur, option string) {
	t.Helper()
	clickT(t, tt, cur)
	clickT(t, tt, option)
}

// autoApp opens s's editor with the AI access fold open, on a hub whose
// soft-lock limit is limit.
func autoApp(t *testing.T, s hubapi.Server, limit string) (*App, *tester, *fakeDoor, *clock) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, SoftLockLimit: limit}, servers: []hubapi.Server{s}}
	a, tt, c, _ := newApp(t, d)
	tt.SetSize(800, 1200)
	tt.flush()
	clickT(t, tt, "Edit "+model.DisplayText(s.Name))
	openAI(t, tt)
	return a, tt, d, c
}

// confirmApp chooses mode for s and presses Save, which opens the dialog.
func confirmApp(t *testing.T, s hubapi.Server, mode, limit string) (*App, *tester, *fakeDoor, *clock) {
	a, tt, d, c := autoApp(t, s, limit)
	choose(t, tt, "Off", mode)
	d.resetCalls()
	clickT(t, tt, "Save")
	tt.flush()
	if a.editor == nil || a.editor.confirm == nil {
		t.Fatalf("Save did not ask: %q", tt.Texts())
	}
	return a, tt, d, c
}

// pass moves the app's clock past the 500 ms guard and draws.
func pass(c *clock, tt *tester) { c.add(600 * time.Millisecond); tt.Frame() }

func TestAutoAllowSelectAndOptIns(t *testing.T) {
	a, tt, d, _ := autoApp(t, autoBox(), "24h")
	d.resetCalls()
	if !tt.HasText("Auto-allow") || !tt.HasText("Off") {
		t.Fatalf("no Auto-allow select: %q", tt.Texts())
	}
	clickT(t, tt, "Allow on root hosts")
	if a.editor.draft.AutoAllowRoot {
		t.Fatal("an opt-in changed while Auto-allow is Off")
	}
	choose(t, tt, "Off", "15 min")
	if a.editor.draft.AutoAllow != "15m" || !tt.HasText("AI access · On · auto") {
		t.Fatalf("draft %q, texts %q", a.editor.draft.AutoAllow, tt.Texts())
	}
	clickT(t, tt, "Allow on root hosts")
	clickT(t, tt, "Also auto-allow sudo-exec")
	if !a.editor.draft.AutoAllowRoot || !a.editor.draft.AutoAllowSudo {
		t.Fatal("the opt-ins do not tick once a mode is chosen")
	}
	choose(t, tt, "15 min", "Until turned off")
	if a.editor.draft.AutoAllow != "forever" {
		t.Fatal(a.editor.draft.AutoAllow)
	}
	if got := d.callList(); len(got) != 0 {
		t.Fatalf("choosing called %v", got)
	}
}

func TestNewHostCannotChooseAutoAllow(t *testing.T) {
	a, tt, _ := edReady(t, []hubapi.Server{})
	clickT(t, tt, "New host")
	openAI(t, tt)
	if !tt.HasText("Save the host and trust its key first") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Off")
	if tt.HasText("15 min") || a.editor.draft.AutoAllow != "off" {
		t.Fatal("the select opened for a new host")
	}
}

func TestTimedGrantNoteAndRefusalHint(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Until: time.Date(2026, 10, 15, 12, 14, 12, 0, time.UTC).Format(time.RFC3339)}
	a, tt, _, _ := autoApp(t, s, "24h")
	if a.editor.draft.AutoAllow != "off" || !tt.HasText("On for 15 more min — saving ends it; pick a duration to keep auto-allow on") {
		t.Fatalf("draft %q, texts %q", a.editor.draft.AutoAllow, tt.Texts())
	}

	hidden := boxServer // not visible to AI
	_, tt, _, _ = autoApp(t, hidden, "24h")
	if tt.HasText("not visible to AI") {
		t.Fatal("the refusal shows while Auto-allow is Off")
	}
	choose(t, tt, "Off", "15 min")
	if !tt.HasText("not visible to AI") {
		t.Fatal(tt.Texts())
	}
}

// Save asks only when it arms a grant the host does not already run.
func TestSaveAsksOnlyWhenItArms(t *testing.T) {
	for _, tc := range []struct {
		auto *hubapi.AutoAllow
		ask  bool
		want string
	}{
		{&hubapi.AutoAllow{Forever: true}, false, `"autoAllow":"forever"`},
		{&hubapi.AutoAllow{Forever: true, Paused: true}, true, ""},
		{&hubapi.AutoAllow{Until: "2026-10-15T13:00:00Z"}, false, `"autoAllow":"off"`},
		{nil, false, `"autoAllow":"off"`},
	} {
		s := autoBox()
		s.AutoAllow = tc.auto
		a, tt, d := editBox(t, s)
		clickT(t, tt, "Save")
		tt.flush()
		if asked := a.editor != nil && a.editor.confirm != nil; asked != tc.ask {
			t.Errorf("%+v: asked %v", tc.auto, asked)
		}
		if got := params(d, "servers.save"); !tc.ask && !strings.HasSuffix(got, tc.want+"}") {
			t.Errorf("%+v: sent %s", tc.auto, got)
		}
	}
}

func TestEnableWaitsThenSavesTheMode(t *testing.T) {
	a, tt, d, c := confirmApp(t, autoBox(), "15 min", "24h")
	if !tt.HasText("Auto-allow AI commands on box") || !tt.HasText("sudo-exec still asks.") || !tt.HasText("including commands planted by what it reads") {
		t.Fatalf("dialog: %q", tt.Texts())
	}
	ends := regexp.MustCompile(`^For 15 min \(ends at \d{1,2}:\d{2}\)$`)
	found := false
	for _, x := range tt.Texts() {
		found = found || ends.MatchString(x)
	}
	if !found {
		t.Fatalf("no duration line in %q", tt.Texts())
	}
	if got := d.callList(); len(got) != 1 || got[0] != "servers.autoAllowCheck" || params(d, "servers.autoAllowCheck") != `{"server":"box"}` {
		t.Fatalf("calls %v", got)
	}
	clickT(t, tt, "Enable") // before the delay
	for i := 0; i < 20; i++ {
		tt.Key(0, ui.KeyTab)
		if tt.Focused("Enable") {
			t.Fatal("Tab reached Enable")
		}
	}
	tt.flush()
	if hasCall(d, "servers.save") {
		t.Fatal("Enable worked before the delay")
	}
	pass(c, tt)
	clickT(t, tt, "Enable")
	tt.flush()
	if got := params(d, "servers.save"); !strings.HasSuffix(got, `"autoAllow":"15m"}`) || a.editor != nil {
		t.Fatalf("sent %s, editor open %v", got, a.editor != nil)
	}
}

func TestForeverNeedsTheNameTyped(t *testing.T) {
	_, tt, d, c := confirmApp(t, autoBox(), "Until turned off", "24h")
	for _, s := range []string{"Until turned off", "Type box to confirm", model.LimitText("24h")} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	pass(c, tt)
	clickT(t, tt, "Enable")
	fill(t, tt, "Type box to confirm", "Box")
	clickT(t, tt, "Enable")
	tt.flush()
	if hasCall(d, "servers.save") {
		t.Fatal("Enable worked without the exact name")
	}
	fill(t, tt, "Type box to confirm", "box")
	clickT(t, tt, "Enable") // typing is not a change: no new delay
	tt.flush()
	if got := params(d, "servers.save"); !strings.HasSuffix(got, `"autoAllow":"forever"}`) {
		t.Fatalf("sent %s", got)
	}
}

func TestConfirmTextFollowsTheLimit(t *testing.T) {
	stops := "Stop auto-allow and lock on the unlock screen, Stop on the host card, or Stop all auto-allow"
	_, tt, _, _ := confirmApp(t, autoBox(), "15 min", "24h")
	if !tt.HasText("Locking the app, by hand or from inactivity, does not stop it: the AI keeps running on this host until then.") || !tt.HasText(stops) {
		t.Fatalf("timed: %q", tt.Texts())
	}
	_, tt, _, _ = confirmApp(t, autoBox(), "Until turned off", "24h")
	for _, s := range []string{"does not stop it: the AI keeps running on this host for up to 24 hours.", stops, "After a restart, the 24-hour limit, or Stop auto-allow and lock, it waits for you to click Resume."} {
		if !tt.HasText(s) {
			t.Fatalf("forever: missing %q", s)
		}
	}
	for _, s := range []string{"lock by hand to stop", "or when you lock the vault", "After a manual lock"} {
		if tt.HasText(s) {
			t.Fatalf("stale copy %q", s)
		}
	}
	_, tt, _, _ = confirmApp(t, autoBox(), "Until turned off", "none")
	if !tt.HasText("with no time limit") || tt.HasText("24 hours") {
		t.Fatalf("none: %q", tt.Texts())
	}
	_, tt, _, _ = confirmApp(t, autoBox(), "Until turned off", "7d")
	if !tt.HasText("for up to 7 days") {
		t.Fatalf("7d: %q", tt.Texts())
	}
}

func TestConfirmWarnsOnlyForNewOptIns(t *testing.T) {
	root := "Allowing root: the AI runs as root, and a command it plants can capture sudo or su passwords you type or store"
	sudoNew := "sudo-exec will run without asking: the AI has full root on this host"
	still, too := "sudo-exec still asks.", "sudo-exec also runs without asking on this host, the same as plain exec."
	_, tt, _, _ := autoApp(t, autoBox(), "24h")
	choose(t, tt, "Off", "15 min")
	clickT(t, tt, "Allow on root hosts")
	clickT(t, tt, "Also auto-allow sudo-exec")
	clickT(t, tt, "Save")
	tt.flush()
	if !tt.HasText(root) || !tt.HasText(sudoNew) || tt.HasText(still) || tt.HasText(too) {
		t.Fatalf("both newly ticked: %q", tt.Texts())
	}

	s := autoBox()
	s.AutoAllowRoot, s.AutoAllowSudo = true, true
	_, tt, _, _ = confirmApp(t, s, "15 min", "24h")
	if tt.HasText(root) || tt.HasText(sudoNew) || tt.HasText(still) || !tt.HasText(too) {
		t.Fatalf("already ticked: %q", tt.Texts())
	}
}

// The root check landing moves the buttons: Enable waits again.
func TestRootCheckRestartsTheDelay(t *testing.T) {
	gate := make(chan struct{})
	_, tt, d, c := autoApp(t, autoBox(), "24h")
	d.set(func() {
		d.gates = map[string]chan struct{}{"servers.autoAllowCheck": gate}
		d.check = &hubapi.AutoCheck{UID: 0}
	})
	choose(t, tt, "Off", "15 min")
	clickT(t, tt, "Save")
	pass(c, tt)
	close(gate)
	tt.flush()
	if !tt.HasText("This is root access: the AI can do anything on this host.") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Enable")
	tt.flush()
	if hasCall(d, "servers.save") {
		t.Fatal("Enable worked right after the check moved the buttons")
	}
	pass(c, tt)
	clickT(t, tt, "Enable")
	tt.flush()
	if !hasCall(d, "servers.save") {
		t.Fatal("Enable did not work once the delay passed")
	}
}

func TestCheckFailureSaysSo(t *testing.T) {
	_, tt, d, _ := autoApp(t, autoBox(), "24h")
	d.set(func() { d.errs = map[string]error{"servers.autoAllowCheck": errors.New("could not check this host")} })
	choose(t, tt, "Off", "15 min")
	clickT(t, tt, "Save")
	tt.flush()
	if !tt.HasText("Could not check this host's sudo access.") || tt.HasText("This is root access: the AI can do anything on this host.") {
		t.Fatal(tt.Texts())
	}
}

// A host the hub would refuse never gets an Enable, nor a root check.
func TestRefusedHostCannotEnable(t *testing.T) {
	s := autoBox()
	s.AutoAllowRefused = "root login"
	_, tt, d, c := autoApp(t, s, "24h")
	choose(t, tt, "Off", "15 min")
	if !tt.HasText("root login") {
		t.Fatal("no refusal hint in the editor:", tt.Texts())
	}
	d.resetCalls()
	clickT(t, tt, "Save")
	tt.flush()
	if !tt.HasText("Auto-allow is not available here: root login") || tt.HasText("sudo-exec still asks.") || hasCall(d, "servers.autoAllowCheck") {
		t.Fatalf("texts %q, calls %v", tt.Texts(), d.callList())
	}
	pass(c, tt)
	clickT(t, tt, "Enable")
	tt.flush()
	if hasCall(d, "servers.save") {
		t.Fatal("Enable saved a refused host")
	}
}

// Cancel, Enter (Cancel has the focus), Esc and Enter in the typed name
// all go back to the editor with the draft kept, and save nothing.
func TestCancelReturnsToTheEditor(t *testing.T) {
	for _, how := range []string{"Cancel", "Enter", "Esc", "Enter in the name"} {
		a, tt, d, _ := confirmApp(t, autoBox(), "Until turned off", "24h")
		switch how {
		case "Cancel":
			clickT(t, tt, "Cancel")
		case "Enter":
			if !tt.Focused("Cancel") {
				t.Fatal("Cancel does not have the focus")
			}
			tt.Key(0, ui.KeyEnter)
		case "Esc":
			tt.Key(0, ui.KeyEscape)
		default:
			clickT(t, tt, "Type box to confirm")
			tt.Type("box")
			tt.Key(0, ui.KeyEnter)
		}
		tt.flush()
		if a.editor == nil || a.editor.confirm != nil || a.editor.draft.AutoAllow != "forever" || hasCall(d, "servers.save") || tt.HasText("Auto-allow AI commands on box") {
			t.Fatalf("%s: editor %+v, calls %v", how, a.editor, d.callList())
		}
	}
}

// Enable's error shows in the dialog, which stays, and waits again.
func TestEnableErrorStaysInTheDialog(t *testing.T) {
	a, tt, d, c := confirmApp(t, autoBox(), "15 min", "24h")
	d.set(func() { d.errs = map[string]error{"servers.save": errors.New("server changed; try again")} })
	pass(c, tt)
	clickT(t, tt, "Enable")
	tt.flush()
	if a.editor == nil || a.editor.confirm == nil || !tt.HasText("server changed; try again") || a.editor.err != "" {
		t.Fatalf("texts %q", tt.Texts())
	}
	clickT(t, tt, "Enable")
	tt.flush()
	if n := count(d.callList(), "servers.save"); n != 1 {
		t.Fatalf("%d saves: the error did not restart the delay", n)
	}
}

func TestEnableSavedButAlertsAndCloses(t *testing.T) {
	a, tt, d, c := confirmApp(t, autoBox(), "15 min", "24h")
	d.set(func() {
		d.errs = map[string]error{"servers.save": errors.New("saved, but auto-allow was not turned on: auto-allow refused: root login")}
	})
	pass(c, tt)
	clickT(t, tt, "Enable")
	tt.flush()
	if a.editor != nil || !tt.HasText("Saved, but auto-allow was not turned on: auto-allow refused: root login") || !hasCall(d, "servers") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

// A lock takes the dialog with the editor; the check's late reply and an
// unlock bring nothing back.
func TestLockDropsTheDialog(t *testing.T) {
	gate := make(chan struct{})
	a, tt, d, _ := autoApp(t, autoBox(), "24h")
	d.set(func() { d.gates = map[string]chan struct{}{"servers.autoAllowCheck": gate} })
	choose(t, tt, "Off", "15 min")
	clickT(t, tt, "Save")
	lockNow(t, a, tt, d)
	close(gate)
	d.set(func() { d.status.Locked = false })
	a.Update(a.refresh)
	tt.flush()
	if a.Screen() != model.ScreenReady || a.editor != nil || tt.HasText("Auto-allow AI commands on box") {
		t.Fatal("the dialog outlived the lock")
	}
}

func TestConfirmShowsTheNameEscaped(t *testing.T) {
	s := autoBox()
	s.Name = "bo\u202ex"
	_, tt, _, _ := confirmApp(t, s, "Until turned off", "24h")
	if !tt.HasText(`Auto-allow AI commands on bo\u202ex`) || !tt.HasText(`Type bo\u202ex to confirm`) {
		t.Fatal(tt.Texts())
	}
	for _, x := range tt.Texts() {
		if strings.ContainsRune(x, '\u202e') {
			t.Fatalf("%q shown unescaped", x)
		}
	}
}

// A servers reload that failed leaves no host to check against: Save opens
// the dialog, which refuses, and nothing is sent.
func TestSaveAsksWhenTheListLostTheHost(t *testing.T) {
	a, tt, d, _ := autoApp(t, autoBox(), "24h")
	choose(t, tt, "Off", "15 min")
	d.set(func() { d.errs = map[string]error{"servers": errors.New("boom")} })
	a.Update(a.reloadServers)
	tt.flush()
	d.resetCalls()
	clickT(t, tt, "Save")
	tt.flush()
	if hasCall(d, "servers.save") {
		t.Fatalf("armed without the Auto-allow dialog: calls %v", d.callList())
	}
}

func TestDialogRefusesWhenTheListLostTheHost(t *testing.T) {
	a, tt, d, _ := autoApp(t, autoBox(), "24h")
	choose(t, tt, "Off", "15 min")
	d.set(func() { d.errs = map[string]error{"servers": errors.New("boom")} })
	a.Update(a.reloadServers)
	tt.flush()
	d.resetCalls()
	clickT(t, tt, "Save")
	tt.flush()
	if !tt.HasText("Auto-allow is not available here: no pinned host key") || hasCall(d, "servers.autoAllowCheck") {
		t.Fatalf("no refusal, or a root check for a host the app lost: calls %v", d.callList())
	}
}
