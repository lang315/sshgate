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
)

// boxServer is hostForm.test.ts's fixture: password auth, a saved password,
// pinned.
var boxServer = hubapi.Server{Name: "box", Host: "h", Port: 22, User: "u", Auth: "password", HostKey: "SHA256:x", HostKeyAlgo: "ssh-ed25519", HasPassword: true}

func clickT(t *testing.T, tt *tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

// fill replaces a text field's value, as the e2e's fill does.
func fill(t *testing.T, tt *tester, label, text string) {
	t.Helper()
	clickT(t, tt, label)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(text)
	tt.Frame()
}

func count(texts []string, s string) int {
	n := 0
	for _, x := range texts {
		if x == s {
			n++
		}
	}
	return n
}

func hasCall(d *fakeDoor, method string) bool {
	for _, m := range d.callList() {
		if m == method {
			return true
		}
	}
	return false
}

// params is the JSON params of the last call the door saw for method.
func params(d *fakeDoor, method string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.params[method]
}

// edReady is readyApp in a window tall enough for the whole editor: the
// Tester clicks where an element is, scrolled out of view or not.
func edReady(t *testing.T, servers []hubapi.Server) (*App, *tester, *fakeDoor) {
	a, tt, d := readyApp(t, servers)
	tt.SetSize(800, 900)
	tt.Frame()
	return a, tt, d
}

func editBox(t *testing.T, s hubapi.Server) (*App, *tester, *fakeDoor) {
	a, tt, d := edReady(t, []hubapi.Server{s})
	clickT(t, tt, "Edit box")
	return a, tt, d
}

func TestNewHostOpensAtAddressWithExtrasFolded(t *testing.T) {
	a, tt, d := edReady(t, []hubapi.Server{boxServer})
	d.resetCalls()
	clickT(t, tt, "New host")
	if !tt.HasText("New host") || !tt.HasText("Close host editor") || !tt.Focused("Address") {
		t.Fatalf("editor: %q", tt.Texts())
	}
	addr, _ := tt.Find("Address")
	label, _ := tt.Find("Label")
	if addr.Y >= label.Y {
		t.Fatalf("Address %+v not above Label %+v", addr, label)
	}
	for _, s := range []string{"+ Key or agent", "AI access · Off", "Privilege escalation (su / sudo)"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	for _, s := range []string{"key", "Saved secrets are never shown. Leave a field empty to keep it.", "su password", "Host key"} {
		if count(tt.Texts(), s) > 0 {
			t.Fatalf("%q shown for a new host: %q", s, tt.Texts())
		}
	}
	if a.editor.draft.AIVisible {
		t.Fatal("Visible to AI is on for a new host")
	}
	clickT(t, tt, "AI access · Off")
	for _, s := range []string{"Visible to AI", "Needs a pinned host key."} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	tt.Type("10.0.0.9") // the typing and the folds call nothing: only a save does
	tt.flush()
	if got := d.callList(); len(got) != 0 {
		t.Fatalf("calls while editing: %v", got)
	}
}

func TestEditShowsThePinAndOnlySavedHints(t *testing.T) {
	_, tt, _ := editBox(t, boxServer)
	texts := tt.Texts()
	for _, s := range []string{"Edit box", "ssh-ed25519 SHA256:x", "Saved secrets are never shown. Leave a field empty to keep it."} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, texts)
		}
	}
	if count(texts, "saved") != 1 || count(texts, "Clear") != 1 || count(texts, "Close") != 1 {
		t.Fatalf("want one saved, one Clear, one Close in %q", texts)
	}
}

func TestKeyAuthShowsOneRadioStopOnKey(t *testing.T) {
	s := boxServer
	s.Auth, s.KeyPath = "key", "~/.ssh/id"
	a, tt, _ := editBox(t, s)
	if tt.HasText("+ Key or agent") || !tt.HasText("Key path") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Key path")
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Frame()
	if !tt.Focused("key") || a.editor.draft.Auth != "key" {
		t.Fatal("Shift+Tab from Key path should land on the checked radio, key")
	}
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Frame()
	if tt.Focused("password") || tt.Focused("agent") {
		t.Fatal("the Auth radios are more than one Tab stop")
	}
}

// The Auth radios: the arrows choose and wrap (Electron's arrowStep rows),
// and the focus follows although the Password field goes with the choice.
func TestArrowsMoveTheAuthChoiceAndKeepTheFocus(t *testing.T) {
	a, tt, _ := edReady(t, []hubapi.Server{})
	clickT(t, tt, "New host")
	clickT(t, tt, "+ Key or agent")
	clickT(t, tt, "password")
	for _, step := range []struct {
		key  ui.Key
		want string
	}{{ui.KeyRight, "key"}, {ui.KeyLeft, "password"}, {ui.KeyLeft, "agent"}, {ui.KeyDown, "password"}, {ui.KeyUp, "agent"}} {
		tt.Key(0, step.key)
		tt.Frame()
		if !tt.Focused(step.want) || a.editor.draft.Auth != step.want {
			t.Fatalf("after %v: auth %q, want %q focused and chosen", step.key, a.editor.draft.Auth, step.want)
		}
	}
}

func TestSaveSendsUntouchedClearedAndTypedSecrets(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	clickT(t, tt, "Clear Password")
	if !tt.HasText("will be cleared") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Privilege escalation (su / sudo)")
	clickT(t, tt, "su password")
	tt.Type("su!")
	d.resetCalls()
	clickT(t, tt, "Save")
	tt.flush()
	want := `{"original":"box","server":{"name":"box","host":"h","port":22,"user":"u","auth":"password","keyPath":"","aiVisible":false,"autoAllowRoot":false,"autoAllowSudo":false,"password":"","suPassword":"su!"},"autoAllow":"off"}`
	if got := params(d, "servers.save"); got != want {
		t.Fatalf("sent %s\nwant %s", got, want)
	}
	if a.editor != nil || !tt.Focused("Search hosts") {
		t.Fatal("a saved editor stays open, or the focus went nowhere")
	}
	if got := d.callList(); len(got) != 2 || got[0] != "servers.save" || got[1] != "servers" {
		t.Fatalf("calls %v: want the save, then the host list", got)
	}
}

func TestTypingAfterClearSendsTheNewValue(t *testing.T) {
	_, tt, d := editBox(t, boxServer)
	clickT(t, tt, "Clear Password")
	clickT(t, tt, "Password")
	tt.Type("new")
	clickT(t, tt, "Save")
	tt.flush()
	if got := params(d, "servers.save"); !strings.Contains(got, `"password":"new"`) {
		t.Fatal(got)
	}
}

func TestEnterInAFieldSavesANewHost(t *testing.T) {
	a, tt, d := edReady(t, []hubapi.Server{})
	clickT(t, tt, "New host")
	tt.Type(" 10.0.4.21 ")
	clickT(t, tt, "User")
	tt.Type("u")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	want := `{"server":{"name":"10.0.4.21","host":"10.0.4.21","port":22,"user":"u","auth":"password","keyPath":"","aiVisible":false,"autoAllowRoot":false,"autoAllowSudo":false},"autoAllow":"off"}`
	if got := params(d, "servers.save"); got != want || a.editor != nil {
		t.Fatalf("sent %s\nwant %s", got, want)
	}
}

func TestBadPortIsNeverSent(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	fill(t, tt, "Port", "70000")
	if !tt.HasText("Port must be 1-65535.") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Save")
	clickT(t, tt, "Port")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if hasCall(d, "servers.save") || a.editor == nil {
		t.Fatalf("calls %v", d.callList())
	}
}

func TestPortChangeWarnsAndCountsTheTabsASaveCloses(t *testing.T) {
	a, tt, _ := edReady(t, []hubapi.Server{boxServer})
	a.Update(func() { a.tabs.Open("box"); a.tabs.Open("box"); a.tabs.ShowHome() })
	tt.flush()
	clickT(t, tt, "Edit box")
	fill(t, tt, "Port", "2222")
	for _, s := range []string{"Changing host or port forgets the host key and saved passwords unless you re-enter them.", "Saving will close 2 open tabs.", "will be cleared", "· changed"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	fill(t, tt, "Port", "22")
	clickT(t, tt, "AI access · Off")
	clickT(t, tt, "Visible to AI")
	if tt.HasText("Saving will close 2 open tabs.") || !tt.HasText("AI access · On") {
		t.Fatalf("Visible to AI alone warns: %q", tt.Texts())
	}
}

func TestSaveErrorStaysInTheEditor(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	d.set(func() { d.errs = map[string]error{"servers.save": errors.New(`a server named "db" already exists`)} })
	clickT(t, tt, "Save")
	tt.flush()
	if a.editor == nil || !tt.HasText(`a server named "db" already exists`) {
		t.Fatal(tt.Texts())
	}
}

func TestSavedButArmingFailedAlertsAndCloses(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	d.set(func() {
		d.errs = map[string]error{"servers.save": errors.New("saved, but auto-allow was not turned on: auto-allow refused: root login")}
	})
	clickT(t, tt, "Save")
	tt.flush()
	if a.editor != nil || !tt.HasText("Saved, but auto-allow was not turned on: auto-allow refused: root login") || !hasCall(d, "servers") {
		t.Fatal(tt.Texts())
	}
}

func TestSaveKeepsOnlyALiveForeverGrant(t *testing.T) {
	for _, tc := range []struct {
		auto *hubapi.AutoAllow
		want string
	}{
		{&hubapi.AutoAllow{Forever: true}, `"autoAllow":"forever"`},
		{&hubapi.AutoAllow{Forever: true, Paused: true}, `"autoAllow":"off"`},
		{&hubapi.AutoAllow{Until: "2026-10-15T13:00:00Z"}, `"autoAllow":"off"`},
	} {
		s := boxServer
		s.AutoAllow = tc.auto
		_, tt, d := editBox(t, s)
		clickT(t, tt, "Save")
		tt.flush()
		if got := params(d, "servers.save"); !strings.HasSuffix(got, tc.want+"}") {
			t.Errorf("%+v: sent %s", tc.auto, got)
		}
	}
}

func TestEscClosesOnlyFromInsideTheEditor(t *testing.T) {
	a, tt, _ := editBox(t, boxServer)
	clickT(t, tt, "Search hosts")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.editor == nil {
		t.Fatal("Esc outside the editor closed it")
	}
	clickT(t, tt, "Label")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.editor != nil || !tt.Focused("Search hosts") {
		t.Fatal("Esc inside did not close the editor, or left the focus nowhere")
	}
}

// A save in flight blocks Esc and a second save; its late reply never
// touches an editor opened since, nor anything once its hub stopped.
func TestLateSaveReplyLeavesANewEditorAlone(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	gate := make(chan struct{})
	d.set(func() { d.gates = map[string]chan struct{}{"servers.save": gate} })
	clickT(t, tt, "Save")
	clickT(t, tt, "Save")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.editor == nil || count(d.callList(), "servers.save") != 1 {
		t.Fatalf("busy editor: open %v, calls %v", a.editor != nil, d.callList())
	}
	clickT(t, tt, "Close")
	clickT(t, tt, "New host")
	close(gate) // the save succeeds now
	tt.flush()
	if a.editor == nil || a.editor.name != "" || a.editor.err != "" || !hasCall(d, "servers") {
		t.Fatalf("the late reply closed or touched the new editor: %+v", a.editor)
	}

	clickT(t, tt, "Close host editor")
	clickT(t, tt, "Edit box")
	gate = make(chan struct{})
	d.set(func() {
		d.gates = map[string]chan struct{}{"servers.save": gate}
		d.errs = map[string]error{"servers.save": errors.New("saved, but auto-allow was not turned on: server changed; try again")}
	})
	clickT(t, tt, "Save")
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", In: time.Second}) })
	tt.flush()
	close(gate)
	tt.flush()
	if a.editor != nil || a.alert != "" {
		t.Fatalf("a hub stop leaves the editor open, or the dead hub's reply showed: %q", a.alert)
	}
}

func TestLockDropsTheEditorAndItsTypedSecret(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	clickT(t, tt, "Password")
	tt.Type("typed-secret")
	d.set(func() { d.status.Locked = true })
	note(t, a, tt, "locked", map[string]string{"reason": "manual"})
	if a.Screen() != model.ScreenLocked || a.editor != nil {
		t.Fatal("the editor outlived the lock")
	}
	d.set(func() { d.status.Locked = false })
	a.Update(a.refresh)
	tt.flush()
	if a.Screen() != model.ScreenReady || a.editor != nil || tt.HasText("Host editor") {
		t.Fatal("an unlock brought the editor back")
	}
}

func TestSaveIsReachableWithEveryFoldOpen(t *testing.T) {
	s := boxServer
	s.Auth, s.HasSuPassword = "key", true
	a, tt, _ := editBox(t, s)
	clickT(t, tt, "AI access · Off") // the lower fold first: opening one moves what is below it
	clickT(t, tt, "Privilege escalation (su / sudo) · saved")
	if !a.editor.escOpen || !a.editor.aiOpen {
		t.Fatal("a fold did not open")
	}
	tt.SetSize(800, 500)
	tt.Frame()
	r, ok := tt.Find("Save")
	if !ok || r.Y < 0 || r.Y+r.H > 500 {
		t.Fatalf("Save at %+v in a 500-high window", r)
	}
	// The body really scrolls: the pin, last in it, starts below the window
	// and comes into it.
	pin := "ssh-ed25519 SHA256:x"
	if r, _ := tt.Find(pin); r.Y+r.H <= 500 {
		t.Fatalf("pin at %+v: already in the window, the scroll proves nothing", r)
	}
	body, _ := tt.Find("Address")
	for i := 0; i < 20; i++ {
		tt.Scroll(body.X+body.W/2, body.Y+body.H/2, 0, 100)
		tt.Frame()
	}
	if r, ok := tt.Find(pin); !ok || r.Y < 0 || r.Y+r.H > 500 {
		t.Fatalf("pin at %+v after scrolling the editor body", r)
	}
	if r, ok := tt.Find("Save"); !ok || r.Y < 0 || r.Y+r.H > 500 {
		t.Fatalf("Save at %+v after scrolling", r)
	}
}

// Untrusted text: a server name in the editor's title and a hub error show
// only escaped.
func TestEditorShowsNameAndHubErrorEscaped(t *testing.T) {
	s := boxServer
	s.Name = "bo\u202ex"
	a, tt, d := edReady(t, []hubapi.Server{s})
	clickT(t, tt, `Edit bo\u202ex`)
	d.set(func() { d.errs = map[string]error{"servers.save": errors.New("name \u202eexists")} })
	clickT(t, tt, "Save")
	tt.flush()
	if a.editor == nil || !tt.HasText(`Edit bo\u202ex`) || !tt.HasText(`name \u202eexists`) {
		t.Fatalf("missing the escaped title or error in %q", tt.Texts())
	}
	for _, x := range tt.Texts() {
		if strings.ContainsRune(x, '\u202e') {
			t.Fatalf("%q shown unescaped", x)
		}
	}
}
