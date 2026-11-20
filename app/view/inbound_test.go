package view

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

var (
	devPhone = hubapi.InboundDevice{Name: "iphone", PublicKey: "ssh-ed25519 AAAAPHONE", Fingerprint: "SHA256:phonefp", Allowed: []string{"100.64.0.5"}, Blocked: []string{"203.0.113.9"}, Created: "2026-11-18T10:00:00Z"}
	devIPad  = hubapi.InboundDevice{Name: "ipad", PublicKey: "ssh-ed25519 AAAAPAD", Fingerprint: "SHA256:padfp", Allowed: []string{"100.64.0.6"}, Blocked: []string{}, Created: "2026-11-19T10:00:00Z"}
)

func inboundApp(t *testing.T, devs ...hubapi.InboundDevice) (*App, *tester, *clock, *fakeDoor) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, StorePath: "/v/servers.json"}, servers: []hubapi.Server{boxServer}, inbound: devs}
	a, tt, c, _ := newApp(t, d)
	tt.SetSize(900, 900)
	tt.flush()
	return a, tt, c, d
}

func enterInbound(t *testing.T, tt *tester) {
	t.Helper()
	clickT(t, tt, "Inbound")
	tt.flush()
}

func TestSidebarInbound(t *testing.T) {
	_, tt, _, d := inboundApp(t, devPhone, devIPad)
	d.resetCalls()
	enterInbound(t, tt)
	for _, w := range []string{inboundNote, "iphone", "ssh-ed25519 · SHA256:phonefp", "100.64.0.5", "203.0.113.9", "Allowed", "Blocked", "ipad"} {
		if !tt.HasText(w) {
			t.Fatalf("missing %q in %q", w, tt.Texts())
		}
	}
	if n := countCalls(d, "inbound.list"); n != 1 {
		t.Fatalf("inbound.list called %d times on entering", n)
	}
	tt.Advance(time.Minute)
	tt.flush()
	tt.Advance(time.Minute)
	tt.flush()
	if n := countCalls(d, "inbound.list"); n != 1 {
		t.Fatalf("inbound.list called %d times after a minute", n)
	}
}

// Nothing but a click calls inbound.list: a ready screen that never shows
// the view, and a redraw of it, make no call.
func TestInboundNoListFromBuild(t *testing.T) {
	a, tt, _, d := inboundApp(t, devPhone)
	if n := countCalls(d, "inbound.list"); n != 0 {
		t.Fatalf("inbound.list %d before the view was entered", n)
	}
	a.Update(func() { a.tabs.ShowInbound() }) // shown without the click: nothing reads
	tt.flush()
	tt.Advance(time.Minute)
	tt.flush()
	if n := countCalls(d, "inbound.list"); n != 0 {
		t.Fatalf("inbound.list %d from build or a timer", n)
	}
}

func TestInboundEmptyState(t *testing.T) {
	_, tt, _, _ := inboundApp(t)
	enterInbound(t, tt)
	if !tt.HasText("No devices yet. Add one with New device.") || !tt.HasText(inboundNote) {
		t.Fatalf("%q", tt.Texts())
	}
}

func TestInboundFileErrorBanner(t *testing.T) {
	_, tt, _, d := inboundApp(t, devIPad)
	d.set(func() { d.fileErr = "permission denied" })
	enterInbound(t, tt)
	if !tt.HasText("authorized_keys was not updated: permission denied") {
		t.Fatalf("%q", tt.Texts())
	}
}

func TestNewDeviceFlow(t *testing.T) {
	_, tt, _, d := inboundApp(t)
	enterInbound(t, tt)
	clickT(t, tt, "New device")
	tt.flush()
	if !tt.Focused("Device name") {
		t.Fatalf("name not focused: %q", tt.Texts())
	}
	if dis, _ := keyNode(tt, "Add device"); !dis {
		t.Fatal("Add device enabled with nothing filled")
	}
	fill(t, tt, "Device name", "iphone")
	fill(t, tt, "Public key", "ssh-ed25519 AAAAPHONE")
	fill(t, tt, "First IP address", "not an ip")
	clickT(t, tt, "Add device")
	tt.flush()
	if hasCall(d, "inbound.add") || !tt.HasText(model.InboundIPError("not an ip")) {
		t.Fatalf("bad ip: calls %v, texts %q", d.callList(), tt.Texts())
	}
	fill(t, tt, "First IP address", "100.64.0.5")
	clickT(t, tt, "Add device")
	tt.flush()
	if got := params(d, "inbound.add"); got != `{"ip":"100.64.0.5","name":"iphone","publicKey":"ssh-ed25519 AAAAPHONE"}` {
		t.Fatalf("inbound.add params %q", got)
	}
	if tt.HasText("New device") && tt.Focused("Device name") {
		t.Fatal("dialog still open")
	}
	if n := countCalls(d, "inbound.list"); n != 2 || !tt.HasText("100.64.0.5") || !tt.HasText("Allowed") {
		t.Fatalf("list %d, %q", n, tt.Texts())
	}
}

func TestNewDeviceHubErrorShownAsIs(t *testing.T) {
	_, tt, _, d := inboundApp(t)
	d.set(func() { d.errs = map[string]error{"inbound.add": errors.New("that key is already on device ipad")} })
	enterInbound(t, tt)
	clickT(t, tt, "New device")
	fill(t, tt, "Device name", "iphone")
	fill(t, tt, "Public key", "ssh-ed25519 AAAAPHONE")
	fill(t, tt, "First IP address", "100.64.0.5")
	clickT(t, tt, "Add device")
	tt.flush()
	if !tt.HasText("that key is already on device ipad") || !tt.HasText("Add device") {
		t.Fatalf("%q", tt.Texts())
	}
}

// Enter in a field does what its button does, and nothing when the button is disabled.
func TestInboundEnterDoesNotBypassDisabled(t *testing.T) {
	_, tt, _, d := inboundApp(t, devIPad)
	enterInbound(t, tt)
	clickT(t, tt, "IP address") // empty: Allow IP for ipad is disabled
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if hasCall(d, "inbound.setIP") {
		t.Fatalf("Enter on an empty IP wrote: %v", d.callList())
	}
	clickT(t, tt, "New device")
	tt.flush()
	fill(t, tt, "Device name", "iphone") // no key, no IP: Add device is disabled
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if hasCall(d, "inbound.add") {
		t.Fatalf("Enter with an empty key wrote: %v", d.callList())
	}
	clickT(t, tt, "First IP address")
	tt.Type("100.64.0.9")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if hasCall(d, "inbound.add") {
		t.Fatalf("Enter with an empty key wrote: %v", d.callList())
	}
}

func TestInboundIPFlows(t *testing.T) {
	_, tt, _, d := inboundApp(t, devPhone)
	enterInbound(t, tt)
	step := func(label, want string) {
		t.Helper()
		d.resetCalls()
		clickT(t, tt, label)
		tt.flush()
		if got := params(d, "inbound.setIP"); got != want {
			t.Fatalf("%s: params %q, want %q", label, got, want)
		}
		if n := countCalls(d, "inbound.list"); n != 1 {
			t.Fatalf("%s: inbound.list %d", label, n)
		}
	}
	step("Unblock 203.0.113.9 on iphone", `{"ip":"203.0.113.9","name":"iphone","state":"allowed"}`)
	step("Block 100.64.0.5 on iphone", `{"ip":"100.64.0.5","name":"iphone","state":"blocked"}`)
	step("Remove 100.64.0.5 from iphone", `{"ip":"100.64.0.5","name":"iphone","state":"removed"}`)
	fill(t, tt, "IP address", "198.51.100.0/24")
	step("Allow IP for iphone", `{"ip":"198.51.100.0/24","name":"iphone","state":"allowed"}`)
	if !tt.HasText("198.51.100.0/24") {
		t.Fatalf("%q", tt.Texts())
	}
	fill(t, tt, "IP address", "2001:db8::1")
	step("Block IP for iphone", `{"ip":"2001:db8::1","name":"iphone","state":"blocked"}`)
}

func TestInboundEnterAllows(t *testing.T) {
	_, tt, _, d := inboundApp(t, devIPad)
	enterInbound(t, tt)
	fill(t, tt, "IP address", "100.64.0.7")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if got := params(d, "inbound.setIP"); got != `{"ip":"100.64.0.7","name":"ipad","state":"allowed"}` {
		t.Fatalf("params %q", got)
	}
}

func TestInboundBadIPMakesNoCall(t *testing.T) {
	_, tt, _, d := inboundApp(t, devIPad)
	enterInbound(t, tt)
	fill(t, tt, "IP address", "999.1.1.1")
	clickT(t, tt, "Allow IP for ipad")
	tt.flush()
	if hasCall(d, "inbound.setIP") || !tt.HasText(model.InboundIPError("999.1.1.1")) {
		t.Fatalf("calls %v, %q", d.callList(), tt.Texts())
	}
}

func TestInboundDeleteGuarded(t *testing.T) {
	_, tt, c, d := inboundApp(t, devIPad, devPhone)
	enterInbound(t, tt)
	clickT(t, tt, "Delete device ipad")
	if !tt.HasText("Delete device ipad? Its key is removed from authorized_keys and it can no longer log in.") || !tt.Focused("Cancel") {
		t.Fatalf("confirm: %q", tt.Texts())
	}
	if dis, _ := keyNode(tt, "Delete device"); !dis {
		t.Fatal("Delete device enabled at once")
	}
	tt.Key(0, ui.KeyEnter) // Enter takes the focused Cancel, never the delete
	tt.flush()
	if hasCall(d, "inbound.delete") || tt.HasText("it can no longer log in") {
		t.Fatalf("Enter: calls %v", d.callList())
	}
	clickT(t, tt, "Delete device ipad")
	clickT(t, tt, "Delete device")
	if hasCall(d, "inbound.delete") {
		t.Fatal("deleted inside the delay")
	}
	adv(tt, c, 600*time.Millisecond)
	tt.Frame()
	if dis, _ := keyNode(tt, "Delete device"); dis {
		t.Fatal("Delete device still disabled after 500 ms")
	}
	clickT(t, tt, "Delete device")
	tt.flush()
	if got := params(d, "inbound.delete"); got != `{"name":"ipad"}` {
		t.Fatalf("params %q", got)
	}
	if tt.HasText("SHA256:padfp") || !tt.HasText("SHA256:phonefp") {
		t.Fatalf("list after delete: %q", tt.Texts())
	}
}

func TestInboundListErrorOffersRetry(t *testing.T) {
	_, tt, _, d := inboundApp(t, devIPad)
	d.set(func() { d.errs = map[string]error{"inbound.list": errors.New("boom list")} })
	enterInbound(t, tt)
	if !tt.HasText("boom list") || !tt.HasText("Try again") {
		t.Fatalf("no error and retry: %q", tt.Texts())
	}
	d.set(func() { d.errs = nil })
	d.resetCalls()
	tt.Advance(time.Minute)
	tt.flush()
	if n := countCalls(d, "inbound.list"); n != 0 {
		t.Fatalf("inbound.list %d without a click", n)
	}
	clickT(t, tt, "Try again")
	tt.flush()
	if n := countCalls(d, "inbound.list"); n != 1 || !tt.HasText("SHA256:padfp") || tt.HasText("boom list") {
		t.Fatalf("retry: %d calls, %q", n, tt.Texts())
	}
}

// A write that changed the list but not authorized_keys shows its error and still reloads.
func TestInboundPartialFailureShowsErrorAndReloads(t *testing.T) {
	_, tt, _, d := inboundApp(t, devIPad)
	msg := partialPrefix + "permission denied"
	d.set(func() { d.afterErr = map[string]error{"inbound.setIP": errors.New(msg)} })
	enterInbound(t, tt)
	d.resetCalls()
	clickT(t, tt, "Block 100.64.0.6 on ipad")
	tt.flush()
	if n := countCalls(d, "inbound.list"); n != 1 || !tt.HasText(msg) || !tt.HasText("Blocked") {
		t.Fatalf("%d lists, %q", n, tt.Texts())
	}
}

// Any other write error is shown and the list is left alone.
func TestInboundWriteErrorDoesNotReload(t *testing.T) {
	_, tt, _, d := inboundApp(t, devIPad)
	d.set(func() { d.errs = map[string]error{"inbound.setIP": errors.New("vault is locked")} })
	enterInbound(t, tt)
	d.resetCalls()
	clickT(t, tt, "Block 100.64.0.6 on ipad")
	tt.flush()
	if n := countCalls(d, "inbound.list"); n != 0 || !tt.HasText("vault is locked") {
		t.Fatalf("%d lists, %q", n, tt.Texts())
	}
}

func TestUnlockOnInboundTabMakesNoCall(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, StorePath: "/v/servers.json"}, pw: "pw", inbound: []hubapi.InboundDevice{devIPad}}
	a, tt, _, _ := newApp(t, d)
	tt.SetSize(900, 900)
	tt.flush()
	enterInbound(t, tt)
	d.set(func() { d.status.Locked = true })
	a.Update(func() { a.refresh() })
	tt.flush()
	d.set(func() { d.status.Locked = false })
	d.resetCalls()
	a.Update(func() { a.refresh() })
	tt.flush()
	tt.Advance(time.Minute)
	tt.flush()
	if n := countCalls(d, "inbound.list"); n != 0 {
		t.Fatalf("inbound.list %d after unlock", n)
	}
}

func loginEntry(seq int, device, from, session string, tty bool) hubapi.AuditEntry {
	b, _ := json.Marshal(map[string]any{"time": "2026-10-15T12:00:00Z", "kind": "login", "device": device, "from": from, "session": session, "tty": tty})
	return hubapi.AuditEntry{Seq: seq, Record: b}
}

func TestAuditLoginsChipAndRow(t *testing.T) {
	_, tt, d, _ := auditApp(t, execEntry(1, "echo one", "allowed"), loginEntry(2, "iphone", "100.64.0.5:51234", "shell", true))
	clickT(t, tt, "Audit")
	tt.flush()
	if !tt.HasText("iphone from 100.64.0.5") || !tt.HasText("Login") || !tt.HasText("shell") || !tt.HasText("tty") {
		t.Fatalf("%q", tt.Texts())
	}
	clickT(t, tt, "Filter Logins")
	tt.flush()
	if got := auditParams(d); !strings.Contains(got, `"kinds":["login"]`) {
		t.Fatalf("params %s", got)
	}
	if tt.HasText("echo one") || !tt.HasText("iphone from 100.64.0.5") {
		t.Fatalf("filtered: %q", tt.Texts())
	}
}
