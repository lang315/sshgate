package view

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
)

var (
	keyUsed = hubapi.Key{Name: "laptop", Type: "se-p256", PublicKey: "ecdsa-sha2-nistp256 AAAAUSED laptop", Created: "2026-11-01T10:00:00Z", UsedBy: []string{"box"}}
	keyFree = hubapi.Key{Name: "spare", Type: "se-p256", PublicKey: "ecdsa-sha2-nistp256 AAAAFREE spare", Created: "2026-11-02T10:00:00Z", UsedBy: []string{}}
)

func keysApp(t *testing.T, keys ...hubapi.Key) (*App, *tester, *clock, *fakeDoor) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, StorePath: "/v/servers.json"}, servers: []hubapi.Server{boxServer}, keys: keys}
	a, tt, c, _ := newApp(t, d)
	tt.SetSize(900, 900)
	tt.flush()
	return a, tt, c, d
}

func countCalls(d *fakeDoor, method string) int {
	n := 0
	for _, m := range d.callList() {
		if m == method {
			n++
		}
	}
	return n
}

func keyNode(tt *tester, label string) (disabled, found bool) {
	for _, n := range tt.Nodes() {
		if n.Label == label {
			return n.Disabled, true
		}
	}
	return false, false
}

func TestSidebarKeychain(t *testing.T) {
	_, tt, _, d := keysApp(t, keyUsed)
	d.resetCalls()
	clickT(t, tt, "Keychain")
	tt.flush()
	if !tt.HasText("laptop") || !tt.HasText("ECDSA P-256 · Secure Enclave") || !tt.HasText("Used by 1 host") || !tt.HasText(keyUsed.PublicKey) {
		t.Fatalf("card: %q", tt.Texts())
	}
	if n := countCalls(d, "keys.list"); n != 1 {
		t.Fatalf("keys.list called %d times on entering", n)
	}
	tt.Advance(time.Minute)
	tt.flush()
	tt.Advance(time.Minute)
	tt.flush()
	if n := countCalls(d, "keys.list"); n != 1 {
		t.Fatalf("keys.list called %d times after a minute", n)
	}
}

func TestNewKeyDialog(t *testing.T) {
	_, tt, _, d := keysApp(t)
	clickT(t, tt, "Keychain")
	tt.flush()
	clickT(t, tt, "New key")
	tt.flush()
	if !tt.Focused("Key name") {
		t.Fatalf("name field not focused: %q", tt.Texts())
	}
	tt.Type("bad name")
	clickT(t, tt, "Create")
	tt.flush()
	if countCalls(d, "keys.create") != 0 || !tt.HasText("Use 1-64 characters of A-Z a-z 0-9 . _ -") {
		t.Fatalf("invalid name: calls %v, texts %q", d.callList(), tt.Texts())
	}
	fill(t, tt, "Key name", "deploy")
	clickT(t, tt, "Create")
	tt.flush()
	if d.params["keys.create"] != `{"name":"deploy"}` {
		t.Fatalf("keys.create params %q", d.params["keys.create"])
	}
	if !tt.HasText("ecdsa-sha2-nistp256 AAAANEW deploy") || !tt.HasText("Copy public key") {
		t.Fatalf("created: %q", tt.Texts())
	}
	clickT(t, tt, "Copy public key")
	if tt.Clipboard() != "ecdsa-sha2-nistp256 AAAANEW deploy" {
		t.Fatalf("clipboard %q", tt.Clipboard())
	}
	if n := countCalls(d, "keys.list"); n != 2 { // entering, then after the create
		t.Fatalf("keys.list %d", n)
	}
}

func TestKeyCardCopy(t *testing.T) {
	_, tt, _, _ := keysApp(t, keyUsed)
	clickT(t, tt, "Keychain")
	tt.flush()
	clickT(t, tt, "Copy public key")
	if tt.Clipboard() != keyUsed.PublicKey {
		t.Fatalf("clipboard %q", tt.Clipboard())
	}
}

func TestKeyDeleteGuarded(t *testing.T) {
	_, tt, c, d := keysApp(t, keyUsed, keyFree)
	clickT(t, tt, "Keychain")
	tt.flush()
	if dis, ok := keyNode(tt, "Delete laptop"); !ok || !dis {
		t.Fatalf("Delete laptop (in use) disabled=%v found=%v", dis, ok)
	}
	if dis, ok := keyNode(tt, "Delete spare"); !ok || dis {
		t.Fatalf("Delete spare disabled=%v found=%v", dis, ok)
	}
	clickT(t, tt, "Delete laptop")
	if tt.HasText("cannot be recovered") {
		t.Fatal("a key in use opened the confirm")
	}
	clickT(t, tt, "Delete spare")
	if !tt.HasText("cannot be recovered") || !tt.Focused("Cancel") {
		t.Fatalf("confirm: %q", tt.Texts())
	}
	if dis, _ := keyNode(tt, "Delete key"); !dis {
		t.Fatal("Delete key enabled at once")
	}
	clickT(t, tt, "Delete key")
	if countCalls(d, "keys.delete") != 0 {
		t.Fatal("deleted inside the delay")
	}
	adv(tt, c, 600*time.Millisecond)
	tt.Frame()
	if dis, _ := keyNode(tt, "Delete key"); dis {
		t.Fatal("Delete key still disabled after 500 ms")
	}
	clickT(t, tt, "Delete key")
	tt.flush()
	if d.params["keys.delete"] != `{"name":"spare"}` {
		t.Fatalf("keys.delete params %q", d.params["keys.delete"])
	}
	if tt.HasText("ecdsa-sha2-nistp256 AAAAFREE spare") {
		t.Fatalf("deleted key still listed: %q", tt.Texts())
	}
}

func TestEditorKeychainAuth(t *testing.T) {
	_, tt, d := edReady(t, []hubapi.Server{boxServer})
	d.set(func() { d.keys = []hubapi.Key{keyFree, keyUsed} })
	clickT(t, tt, "Edit box")
	clickT(t, tt, "+ Key or agent")
	clickT(t, tt, "Keychain")
	tt.flush()
	choose(t, tt, "Choose a key", "spare")
	tt.flush()
	if !tt.HasText("spare") || !tt.HasText(keyFree.PublicKey) {
		t.Fatalf("select / public key: %q", tt.Texts())
	}
	for _, s := range tt.Texts() {
		if strings.Contains(s, "passphrase") {
			t.Fatalf("passphrase field for Keychain: %q", s)
		}
	}
	clickT(t, tt, "Save")
	tt.flush()
	got := d.params["servers.save"]
	if !strings.Contains(got, `"auth":"keychain"`) || !strings.Contains(got, `"keyName":"spare"`) {
		t.Fatalf("servers.save %s", got)
	}
}

func TestKeychainEmptyState(t *testing.T) {
	_, tt, _, _ := keysApp(t)
	clickT(t, tt, "Keychain")
	tt.flush()
	if !tt.HasText("No keys yet. Create one with New key.") || !tt.HasText("New key") {
		t.Fatalf("empty: %q", tt.Texts())
	}
}

func TestEditorKeychainHostLoadsOnce(t *testing.T) {
	s := boxServer
	s.Auth, s.KeyName, s.KeyPublicKey = "keychain", "spare", keyFree.PublicKey
	_, tt, d := edReady(t, []hubapi.Server{s})
	d.set(func() { d.keys = []hubapi.Key{keyFree} })
	d.resetCalls()
	clickT(t, tt, "Edit box")
	tt.flush()
	for i := 0; i < 5; i++ {
		tt.Frame()
	}
	tt.Advance(time.Minute)
	tt.flush()
	if n := countCalls(d, "keys.list"); n != 1 {
		t.Fatalf("keys.list %d", n)
	}
	if !tt.HasText(keyFree.PublicKey) {
		t.Fatalf("no public key: %q", tt.Texts())
	}
}

func TestEditorRadioLoadsOnceAndSaveNeedsPick(t *testing.T) {
	_, tt, d := edReady(t, []hubapi.Server{boxServer})
	d.set(func() { d.keys = []hubapi.Key{keyFree} })
	clickT(t, tt, "Edit box")
	clickT(t, tt, "+ Key or agent")
	d.resetCalls()
	if countCalls(d, "keys.list") != 0 {
		t.Fatal("loaded before the choice")
	}
	clickT(t, tt, "Keychain")
	tt.flush()
	tt.Advance(time.Minute)
	tt.flush()
	if n := countCalls(d, "keys.list"); n != 1 {
		t.Fatalf("keys.list %d", n)
	}
	if !tt.HasText("Choose a key") {
		t.Fatalf("no placeholder: %q", tt.Texts())
	}
	if dis, _ := keyNode(tt, "Save"); !dis {
		t.Fatal("Save enabled with no key chosen")
	}
}

func TestUnlockOnKeychainTabMakesNoCall(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, StorePath: "/v/servers.json"}, pw: "pw", keys: []hubapi.Key{keyUsed}}
	a, tt, _, _ := newApp(t, d)
	tt.SetSize(900, 900)
	tt.flush()
	clickT(t, tt, "Keychain")
	tt.flush()
	d.set(func() { d.status.Locked = true })
	a.Update(func() { a.refresh() })
	tt.flush()
	d.set(func() { d.status.Locked = false })
	d.resetCalls()
	a.Update(func() { a.refresh() })
	tt.flush()
	tt.Advance(time.Minute)
	tt.flush()
	if n := countCalls(d, "keys.list"); n != 0 {
		t.Fatalf("keys.list %d after unlock", n)
	}
}

func TestEditorKeychainListErrorOffersRetry(t *testing.T) {
	s := boxServer
	s.Auth, s.KeyName, s.KeyPublicKey = "keychain", "spare", keyFree.PublicKey
	_, tt, d := edReady(t, []hubapi.Server{s})
	d.set(func() {
		d.keys = []hubapi.Key{keyFree}
		d.errs = map[string]error{"keys.list": errors.New("boom list")}
	})
	clickT(t, tt, "Edit box")
	tt.flush()
	if tt.HasText("No keys yet. Create one in Keychain.") || !tt.HasText("boom list") || !tt.HasText("Try again") {
		t.Fatalf("no error and retry: %q", tt.Texts())
	}
	d.set(func() { d.errs = nil })
	d.resetCalls()
	clickT(t, tt, "Try again")
	tt.flush()
	if n := countCalls(d, "keys.list"); n != 1 || !tt.HasText(keyFree.PublicKey) {
		t.Fatalf("retry: %d calls, %q", n, tt.Texts())
	}
}

// Enter in a field must not save a Keychain host whose key is not chosen:
// Save is disabled then, and Enter agrees.
func TestEnterDoesNotSaveKeychainHostWithoutKey(t *testing.T) {
	a, tt, d := edReady(t, []hubapi.Server{boxServer})
	d.set(func() { d.keys = []hubapi.Key{keyFree, keyUsed} })
	clickT(t, tt, "Edit box")
	clickT(t, tt, "+ Key or agent")
	clickT(t, tt, "Keychain")
	tt.flush()
	clickT(t, tt, "User")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if hasCall(d, "servers.save") || a.editor == nil {
		t.Fatalf("calls %v", d.callList())
	}
}

// A failed keys.list in the Keychain view offers Try again; it reads on the
// click only, never on a redraw or a timer.
func TestKeychainListErrorOffersRetry(t *testing.T) {
	_, tt, _, d := keysApp(t, keyFree)
	d.set(func() { d.errs = map[string]error{"keys.list": errors.New("boom list")} })
	clickT(t, tt, "Keychain")
	tt.flush()
	if !tt.HasText("boom list") || !tt.HasText("Try again") {
		t.Fatalf("no error and retry: %q", tt.Texts())
	}
	d.set(func() { d.errs = nil })
	d.resetCalls()
	tt.Advance(time.Minute)
	tt.flush()
	if n := countCalls(d, "keys.list"); n != 0 {
		t.Fatalf("keys.list %d without a click", n)
	}
	clickT(t, tt, "Try again")
	tt.flush()
	if n := countCalls(d, "keys.list"); n != 1 || !tt.HasText(keyFree.PublicKey) || tt.HasText("boom list") {
		t.Fatalf("retry: %d calls, %q", n, tt.Texts())
	}
}
