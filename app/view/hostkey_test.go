package view

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/termconn"
)

var unknownBox = hubapi.HostKey{Status: "hostKeyUnknown", Server: "box", Host: "127.0.0.1", Port: 2222, User: "test", Fingerprint: "SHA256:abc", KeyType: "ssh-ed25519", KnownHosts: "absent"}

func unknownKey(fp string) error {
	k := unknownBox
	k.Fingerprint = fp
	return &termconn.OpenError{Status: k.Status, Key: k}
}

// keyDial records each Dial ("id" or "id fingerprint keyType" on a trusted
// retry) and answers the nth call with answer(n).
type keyDial struct {
	mu     sync.Mutex
	calls  []string
	answer func(n int) (*termconn.Conn, error)
}

func (k *keyDial) dial(_ context.Context, id, _ string, trust *hubapi.Trust) (*termconn.Conn, error) {
	k.mu.Lock()
	c := id
	if trust != nil {
		c += " " + trust.Fingerprint + " " + trust.KeyType
	}
	k.calls = append(k.calls, c)
	n := len(k.calls)
	k.mu.Unlock()
	return k.answer(n)
}

func (k *keyDial) list() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.calls...)
}

// hkApp is a ready app with host box whose Dial is k, and its clock.
func hkApp(t *testing.T, k *keyDial) (*App, *tester, *clock, *fakeDoor) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box", User: "test", Host: "127.0.0.1", Port: 2222}}}
	a, tt, c, _ := newApp(t, d)
	a.Dial = k.dial
	tt.flush()
	return a, tt, c, d
}

func clickOpenBox(t *testing.T, tt *tester) {
	t.Helper()
	if err := tt.Click("Open box"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
}

func TestUnknownKeyPromptTextsAndCancel(t *testing.T) {
	k := &keyDial{answer: func(int) (*termconn.Conn, error) { return nil, unknownKey("SHA256:abc") }}
	a, tt, _, _ := hkApp(t, k)
	clickOpenBox(t, tt)
	for _, s := range []string{"Trust this host key?", "test@127.0.0.1:2222", "No host key is pinned for this server yet. Check the fingerprint against one you got another way, from the server's console or its admin.",
		"ssh-ed25519 fingerprint", "SHA256:abc", "This host is not in your ~/.ssh/known_hosts.", "Trust only if this matches the key you expect. Once trusted it is pinned, and a different key later is refused."} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	for _, s := range tt.Texts() {
		if strings.Contains(strings.ToLower(s), "first connection") {
			t.Fatalf("says %q", s)
		}
	}
	if !tt.Focused("Cancel") {
		t.Fatal("Cancel is not the default")
	}
	for range 5 {
		tt.Key(0, ui.KeyTab)
		if tt.Focused("Trust and connect") {
			t.Fatal("Tab reached Trust and connect")
		}
	}
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if tt.HasText("Trust this host key?") || !tt.HasText("[exited: open failed: host key not trusted]") || tabState(a, "t1") != "exited" {
		t.Fatalf("after Enter: %q", tt.Texts())
	}
	if got := k.list(); len(got) != 1 {
		t.Fatal(got)
	}
}

// TestTrustIsNotReachableByKeyboard: with the delay passed, no number of
// Tabs focuses Trust and connect, and Space or Enter on whatever has the
// focus never trusts the key.
func TestTrustIsNotReachableByKeyboard(t *testing.T) {
	k := &keyDial{answer: func(int) (*termconn.Conn, error) { return nil, unknownKey("SHA256:abc") }}
	a, tt, c, _ := hkApp(t, k)
	for n := range 11 {
		a.Update(func() { a.openTab("box") })
		tt.flush()
		if !tt.HasText("Trust this host key?") {
			t.Fatalf("%d Tabs: no prompt; screen %q", n, tt.Texts())
		}
		c.add(500 * time.Millisecond) // Trust and connect is enabled now
		tt.Frame()
		for i := range n {
			tt.Key(0, ui.KeyTab)
			if tt.Focused("Trust and connect") {
				t.Fatalf("Tab %d of %d reached Trust and connect", i+1, n)
			}
		}
		tt.Key(0, ui.KeySpace)
		tt.Key(0, ui.KeyEnter)
		tt.flush()
		for _, d := range k.list() {
			if strings.Contains(d, "SHA256:abc") {
				t.Fatalf("%d Tabs then Space and Enter trusted the key: dials %q", n, k.list())
			}
		}
	}
}

func TestTrustWaitsThenRetriesTheSameID(t *testing.T) {
	k := &keyDial{answer: func(n int) (*termconn.Conn, error) {
		if n == 1 {
			return nil, unknownKey("SHA256:abc")
		}
		return nil, errors.New("dial refused")
	}}
	a, tt, c, _ := hkApp(t, k)
	clickOpenBox(t, tt)
	_ = tt.Click("Trust and connect")
	tt.flush()
	if len(k.list()) != 1 || !tt.HasText("Trust this host key?") {
		t.Fatal("Trust worked before 500 ms")
	}
	c.add(500 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Trust and connect"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if got := k.list(); len(got) != 2 || got[1] != "t1 SHA256:abc ssh-ed25519" {
		t.Fatalf("dials %q: the retry reuses the id and sends the key", got)
	}
	if tt.HasText("Trust this host key?") || !tt.HasText("[exited: open failed: dial refused]") || len(a.tabs.Tabs) != 1 {
		t.Fatalf("screen %q", tt.Texts())
	}
}

func TestTrustedOpenReloadsServers(t *testing.T) {
	needLib(t)
	hd, m := &hubDoor{}, termconn.NewMux()
	k := &keyDial{}
	k.answer = func(n int) (*termconn.Conn, error) {
		if n == 1 {
			return nil, unknownKey("SHA256:abc")
		}
		return termconn.Open(context.Background(), hd, m, "t1", "box", 24, 80, nil)
	}
	a, tt, c, d := hkApp(t, k)
	clickOpenBox(t, tt)
	c.add(time.Second)
	tt.Frame()
	d.resetCalls()
	if err := tt.Click("Trust and connect"); err != nil {
		t.Fatal(err)
	}
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" && tt.Focused("Terminal box") }, "the trusted tab open and focused")
	if got := d.callList(); len(got) != 1 || got[0] != "servers" {
		t.Fatalf("calls %v: a trusted open reloads the host list once", got)
	}
}

func TestPromptsShowOneAtATimeAndAClosedTabDropsItsOwn(t *testing.T) {
	k := &keyDial{answer: func(n int) (*termconn.Conn, error) {
		return nil, unknownKey([]string{"", "SHA256:one", "SHA256:two"}[min(n, 2)])
	}}
	a, tt, c, _ := hkApp(t, k)
	a.Update(func() { a.openTab("box") })
	tt.flush()
	a.Update(func() { a.openTab("box") }) // after t1's answer: the queue order is fixed
	tt.flush()
	if !tt.HasText("SHA256:one") || tt.HasText("SHA256:two") {
		t.Fatal("want t1's prompt only:", tt.Texts())
	}
	c.add(time.Second)
	tt.Frame()
	tt.Key(0, ui.KeyEscape) // Esc cancels, as Cancel does
	tt.flush()
	if !tt.HasText("SHA256:two") || !tt.Focused("Cancel") || tabState(a, "t1") != "exited" {
		t.Fatal("t2's prompt must follow, with Cancel focused:", tt.Texts())
	}
	_ = tt.Click("Trust and connect") // a new prompt restarts the delay
	tt.flush()
	if len(k.list()) != 2 {
		t.Fatal("Trust worked right after the prompt changed")
	}
	a.Update(func() { a.closeTab("t2") })
	tt.flush()
	if tt.HasText("Trust this host key?") {
		t.Fatal("a closed tab's prompt still shows")
	}
}

func TestKnownHostsHints(t *testing.T) {
	for kh, want := range map[string]string{
		"match":     "Your ~/.ssh/known_hosts lists this same key for this host.",
		"absent":    "This host is not in your ~/.ssh/known_hosts.",
		"different": "Warning: your ~/.ssh/known_hosts lists a different key for this host. The connection may be intercepted.",
	} {
		k := &keyDial{answer: func(int) (*termconn.Conn, error) {
			key := unknownBox
			key.KnownHosts = kh
			return nil, &termconn.OpenError{Status: key.Status, Key: key}
		}}
		_, tt, _, _ := hkApp(t, k)
		clickOpenBox(t, tt)
		if !tt.HasText(want) {
			t.Fatalf("%s: missing %q in %q", kh, want, tt.Texts())
		}
	}
}

func TestMismatchShowsBothKeysAndCloses(t *testing.T) {
	mis := hubapi.HostKey{Status: "hostKeyMismatch", Server: "box", Host: "127.0.0.1", Port: 2222, User: "test", Pinned: "SHA256:old", Presented: "SHA256:new"}
	k := &keyDial{answer: func(int) (*termconn.Conn, error) { return nil, &termconn.OpenError{Status: mis.Status, Key: mis} }}
	a, tt, _, _ := hkApp(t, k)
	clickOpenBox(t, tt)
	for _, s := range []string{"Host key changed", "test@127.0.0.1:2222", "This server presented a different host key than the one pinned. The connection may be intercepted. Nothing was sent.",
		"Pinned", "SHA256:old", "Presented now", "SHA256:new", "Did the key change on purpose?"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if tt.HasText("Trust and connect") || !tt.HasText("Open host editor") || !tt.Focused("Close") {
		t.Fatal("the mismatch dialog offers Open host editor and Close, never Trust:", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if tt.HasText("Host key changed") || !tt.HasText("[exited: open failed: host key mismatch]") || a.mismatch != nil {
		t.Fatal(tt.Texts())
	}
}

// keyOpen answers term.open with res, a hub term.open result.
type keyOpen struct {
	*hubDoor
	res string
}

func (d keyOpen) Call(ctx context.Context, method string, params, result any) error {
	if method == "term.open" {
		return json.Unmarshal([]byte(d.res), result)
	}
	return d.hubDoor.Call(ctx, method, params, result)
}

// TestMismatchDecodedFromTheHub: a hostKeyMismatch term.open result goes
// through termconn.Open's decode into the dialog, both keys included.
func TestMismatchDecodedFromTheHub(t *testing.T) {
	d := keyOpen{&hubDoor{}, `{"status":"hostKeyMismatch","server":"box","host":"127.0.0.1","port":2222,"user":"test","pinned":"SHA256:old","presented":"SHA256:n\u202eew"}`}
	k := &keyDial{answer: func(int) (*termconn.Conn, error) {
		return termconn.Open(context.Background(), d, termconn.NewMux(), "t1", "box", 24, 80, nil)
	}}
	a, tt, _, _ := hkApp(t, k)
	clickOpenBox(t, tt)
	for _, s := range []string{"Host key changed", "test@127.0.0.1:2222", "SHA256:old", `SHA256:n\u202eew`} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	for _, s := range tt.Texts() {
		if strings.ContainsRune(s, '\u202e') {
			t.Fatalf("raw hidden character shown: %q", s)
		}
	}
	if !tt.Focused("Close") || tabState(a, "t1") != "exited" {
		t.Fatal(tt.Texts())
	}
}

// TestPromptAfterMismatchWaitsAgain: a mismatch dialog that hid a waiting
// prompt is a new appearance of that prompt once closed, so Trust waits
// 500 ms again.
func TestPromptAfterMismatchWaitsAgain(t *testing.T) {
	mis := hubapi.HostKey{Status: "hostKeyMismatch", Server: "box", Host: "127.0.0.1", Port: 2222, User: "test", Pinned: "SHA256:old", Presented: "SHA256:new"}
	k := &keyDial{answer: func(n int) (*termconn.Conn, error) {
		switch n {
		case 1:
			return nil, unknownKey("SHA256:abc")
		case 2:
			return nil, &termconn.OpenError{Status: mis.Status, Key: mis}
		}
		return nil, errors.New("dial refused")
	}}
	a, tt, c, _ := hkApp(t, k)
	clickOpenBox(t, tt)
	c.add(time.Second)
	tt.Frame()
	a.Update(func() { a.openTab("box") })
	tt.flush()
	if !tt.HasText("Host key changed") || tt.HasText("Trust this host key?") {
		t.Fatal("want the mismatch dialog over the prompt:", tt.Texts())
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if !tt.HasText("Trust this host key?") {
		t.Fatal("the prompt did not come back:", tt.Texts())
	}
	_ = tt.Click("Trust and connect")
	tt.flush()
	if got := k.list(); len(got) != 2 {
		t.Fatalf("dials %q: Trust worked right after the mismatch dialog closed", got)
	}
	c.add(600 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Trust and connect"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if got := k.list(); len(got) != 3 || got[2] != "t1 SHA256:abc ssh-ed25519" {
		t.Fatalf("dials %q: Trust did not retry after the delay", got)
	}
}

// TestDialogsGiveFocusBackToTheTerminal: answering a prompt, or closing a
// mismatch, for a background tab focuses the active tab's terminal again.
func TestDialogsGiveFocusBackToTheTerminal(t *testing.T) {
	needLib(t)
	hd, m := &hubDoor{}, termconn.NewMux()
	mis := hubapi.HostKey{Status: "hostKeyMismatch", Server: "box", Host: "127.0.0.1", Port: 2222, User: "test", Pinned: "SHA256:old", Presented: "SHA256:new"}
	k := &keyDial{answer: func(n int) (*termconn.Conn, error) {
		switch n {
		case 1:
			return termconn.Open(context.Background(), hd, m, "t1", "box", 24, 80, nil)
		case 2:
			return nil, unknownKey("SHA256:abc")
		}
		return nil, &termconn.OpenError{Status: mis.Status, Key: mis}
	}}
	a, tt, _, _ := hkApp(t, k)
	clickOpenBox(t, tt)
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" && tt.Focused("Terminal box") }, "t1 open and focused")
	a.Update(func() { a.openTab("box") })
	tt.flush()
	a.Update(func() { a.activate("t1") })
	tt.flush()
	if !tt.HasText("Trust this host key?") || !tt.Focused("Cancel") {
		t.Fatal("want t2's prompt with Cancel focused:", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if tabState(a, "t2") != "exited" || !tt.Focused("Terminal box") {
		t.Fatal("Cancel did not give the focus back to t1's terminal")
	}
	a.Update(func() { a.openTab("box") })
	tt.flush()
	a.Update(func() { a.activate("t1") })
	tt.flush()
	if !tt.HasText("Host key changed") || !tt.Focused("Close") {
		t.Fatal("want t3's mismatch dialog with Close focused:", tt.Texts())
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if !tt.Focused("Terminal box") {
		t.Fatal("Close did not give the focus back to t1's terminal")
	}
}

// TestPromptSurvivesLockWithAFreshDelay: the lock screen hides a waiting
// prompt; after unlock it shows again and Trust waits 500 ms again.
func TestPromptSurvivesLockWithAFreshDelay(t *testing.T) {
	k := &keyDial{answer: func(n int) (*termconn.Conn, error) {
		if n == 1 {
			return nil, unknownKey("SHA256:abc")
		}
		return nil, errors.New("dial refused")
	}}
	a, tt, c, d := hkApp(t, k)
	clickOpenBox(t, tt)
	c.add(time.Second)
	tt.Frame()
	d.set(func() { d.status.Locked = true })
	note(t, a, tt, "locked", map[string]string{"reason": "manual"})
	if tt.HasText("Trust this host key?") {
		t.Fatal("the prompt shows over the lock screen")
	}
	d.set(func() { d.status.Locked = false })
	a.Update(func() { a.refresh() })
	tt.flush()
	if !tt.HasText("Trust this host key?") {
		t.Fatal("the prompt did not come back after unlock")
	}
	_ = tt.Click("Trust and connect")
	tt.flush()
	if len(k.list()) != 1 {
		t.Fatal("Trust worked right after unlock")
	}
	c.add(600 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Trust and connect"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if len(k.list()) != 2 {
		t.Fatal("Trust did not retry after the delay")
	}
}

func TestHostKeyDialogEscapesServerText(t *testing.T) {
	k := &keyDial{answer: func(int) (*termconn.Conn, error) {
		key := unknownBox
		key.Server, key.User, key.Fingerprint, key.KeyType = "bo\u202ex", "te\u200bst", "SHA256:a\u2066b", "ssh-ed25519\u202e"
		return nil, &termconn.OpenError{Status: key.Status, Key: key}
	}}
	_, tt, _, _ := hkApp(t, k)
	clickOpenBox(t, tt)
	if !tt.HasText(`bo\u202ex`) || !tt.HasText(`SHA256:a\u2066b`) {
		t.Fatal(tt.Texts())
	}
	for _, s := range tt.Texts() {
		if strings.ContainsAny(s, "\u202e\u200b\u2066") {
			t.Fatalf("raw hidden character shown: %q", s)
		}
	}
}
