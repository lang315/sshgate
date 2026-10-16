package view

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
)

const keyBackspace = ui.KeyBackspace

func readyApp(t *testing.T, servers []hubapi.Server) (*App, *tester, *fakeDoor) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, StorePath: "/v/servers.json"}, servers: servers}
	a, tt, _, _ := newApp(t, d)
	tt.flush()
	return a, tt, d
}

func TestHostListSearchAndEmptyStates(t *testing.T) {
	_, tt, _ := readyApp(t, []hubapi.Server{{Name: "box", User: "test", Host: "127.0.0.1", Port: 2222, AIVisible: true}, {Name: "db", User: "pg", Host: "10.0.0.5", Port: 22}})
	for _, s := range []string{"Hosts · 2", "box", "test@127.0.0.1:2222", "AI", "New key", "Vault file: /v/servers.json — copy it to back up."} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if err := tt.Click("Search hosts"); err != nil {
		t.Fatal(err)
	}
	tt.Type("nomatch")
	tt.Frame()
	if !tt.HasText(`No hosts match "nomatch"`) || !tt.HasText("Hosts · 0") {
		t.Fatal(tt.Texts())
	}
	for range "nomatch" {
		tt.Key(0, keyBackspace)
	}
	tt.Type("BO")
	tt.Frame()
	if !tt.HasText("Hosts · 1") || !tt.HasText("box") {
		t.Fatal(tt.Texts())
	}
}

func TestNoHostsYet(t *testing.T) {
	_, tt, _ := readyApp(t, []hubapi.Server{})
	if !tt.HasText("No hosts yet. Add one with New host.") {
		t.Fatal(tt.Texts())
	}
}

func TestStoreErrorBanner(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, StoreError: "the vault file failed its integrity check or could not be read; the app keeps the last good copy"}}
	_, tt, _, _ := newApp(t, d)
	tt.flush()
	if !tt.HasText("The vault file failed its integrity check") {
		t.Fatal(tt.Texts())
	}
}

func TestLockButtonAndAIButton(t *testing.T) {
	_, tt, d := readyApp(t, []hubapi.Server{{Name: "box", AutoAllow: &hubapi.AutoAllow{Forever: true}}})
	if !tt.HasText("AI requests") {
		t.Fatal("AI button hidden while a host is on auto-allow:", tt.Texts())
	}
	if !tt.HasText("auto 1") {
		t.Fatal(tt.Texts())
	}
	d.resetCalls()
	if err := tt.Click("Lock"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if got := d.callList(); len(got) != 1 || got[0] != "lock" {
		t.Fatal(got)
	}
}

// The host list scrolls when it is taller than the window.
func TestHostListScrolls(t *testing.T) {
	var servers []hubapi.Server
	for i := 0; i < 40; i++ {
		servers = append(servers, hubapi.Server{Name: fmt.Sprintf("host%02d", i), User: "u", Host: "h", Port: 22})
	}
	_, tt, _ := readyApp(t, servers)
	tt.SetSize(800, 400)
	tt.Frame()
	r, ok := tt.Find("host39")
	if !ok || r.Y+r.H <= 400 {
		t.Fatalf("last host should start below the window: %+v %v", r, ok)
	}
	tt.Move(400, 200)
	tt.Scroll(400, 200, 0, 20000)
	tt.Frame()
	r, ok = tt.Find("host39")
	if !ok || r.Y < 0 || r.Y+r.H > 400 {
		t.Fatalf("last host not in view after scrolling: %+v %v", r, ok)
	}
}

func TestHostNameIsDisplayText(t *testing.T) {
	_, tt, _ := readyApp(t, []hubapi.Server{{Name: "\u202eevil", User: "u", Host: "h", Port: 22}})
	for _, s := range tt.Texts() {
		if strings.ContainsRune(s, '\u202e') {
			t.Fatalf("raw U+202E in %q", s)
		}
	}
}

// The card's Files, Tunnels, Edit and Delete are icon buttons: a word would
// push the row past the 240 px card on wider fonts. Only their names remain.
func TestCardActionsAreIcons(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}}
	_, tt, _, _ := newApp(t, d)
	tt.flush()
	for _, w := range []string{"Files", "Tunnels", "Edit", "Delete"} {
		if _, ok := tt.Find(w + " box"); !ok {
			t.Errorf("no button named %q in %q", w+" box", tt.Texts())
		}
		if count(tt.Texts(), w) != 0 {
			t.Errorf("the card shows the word %q: %q", w, tt.Texts())
		}
	}
}
