package view

import (
	"errors"
	"testing"

	"github.com/lang315/sshgate/app/hubapi"
)

const pausedBox = "Auto-allow is paused on box. Once resumed, the AI keeps running there while the app is locked."

func pausedApp(t *testing.T) (*App, *tester, *fakeDoor, *clock) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true, Paused: true}
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{s}}
	a, tt, c, _ := newApp(t, d)
	tt.flush()
	return a, tt, d, c
}

func TestResumeWaitsThenResumesThePausedHosts(t *testing.T) {
	a, tt, d, c := pausedApp(t)
	if !tt.HasText(pausedBox) {
		t.Fatal(tt.Texts())
	}
	d.resetCalls()
	clickT(t, tt, "Resume")
	tt.flush()
	if hasCall(d, "servers.setAutoAllow") {
		t.Fatal("Resume worked before the delay")
	}
	// A host joining the list moves the banner: Resume waits again.
	db := autoBox()
	db.Name, db.AutoAllow = "db", &hubapi.AutoAllow{Forever: true, Paused: true}
	d.set(func() { d.servers = append(d.servers, db) })
	pass(c, tt)
	a.Update(a.reloadServers)
	tt.flush()
	clickT(t, tt, "Resume")
	tt.flush()
	if hasCall(d, "servers.setAutoAllow") || !tt.HasText("Auto-allow is paused on box, db.") {
		t.Fatalf("calls %v, texts %q", d.callList(), tt.Texts())
	}
	pass(c, tt)
	clickT(t, tt, "Resume")
	tt.flush()
	if n := count(d.callList(), "servers.setAutoAllow"); n != 2 || params(d, "servers.setAutoAllow") != `{"mode":"forever","server":"db"}` || !hasCall(d, "servers") {
		t.Fatalf("calls %v, last %s", d.callList(), params(d, "servers.setAutoAllow"))
	}
	d.set(func() { d.errs = map[string]error{"servers.setAutoAllow": errors.New("boom")} })
	clickT(t, tt, "Stop")
	tt.flush()
	if params(d, "servers.setAutoAllow") != `{"mode":"off","server":"db"}` || !tt.HasText("Could not stop auto-allow on box, db: boom") {
		t.Fatalf("last %s, texts %q", params(d, "servers.setAutoAllow"), tt.Texts())
	}
}

// The AI column shows the banner too, below its request list: a request's
// Allow does not move when it appears.
func TestPausedBannerInTheAIColumnLeavesAllow(t *testing.T) {
	a, tt, d, c := pausedApp(t)
	tt.SetSize(800, 700) // the list fits with the banner too, so no rect is clipped (Windows text is taller)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "ls")})
	pass(c, tt)
	if count(tt.Texts(), pausedBox) != 2 {
		t.Fatalf("want the banner on Hosts and in the AI column: %q", tt.Texts())
	}
	allow, ok := tt.Find("Allow")
	if !ok {
		t.Fatalf("no Allow in %q", tt.Texts())
	}
	d.set(func() { d.servers[0].AutoAllow = nil })
	a.Update(a.reloadServers)
	tt.flush()
	if after, ok := tt.Find("Allow"); !ok || after != allow || tt.HasText(pausedBox) {
		t.Fatalf("Allow moved from %+v to %+v", allow, after)
	}
}

// The paused banner shown again is a new banner, so Resume waits AllowDelay
// again: in the AI column after it reopens, and on Hosts after a tab.
func TestResumeWaitsAgainWhenShownAgain(t *testing.T) {
	a, tt, d, c := pausedApp(t)
	pass(c, tt) // Hosts' Resume is live
	a.Update(func() { a.tabs.Open("box") })
	tt.flush()
	clickT(t, tt, "AI requests")
	tt.flush()
	if count(tt.Texts(), pausedBox) != 1 {
		t.Fatalf("want the banner in the AI column only: %q", tt.Texts())
	}
	pass(c, tt) // the column's Resume is live
	clickT(t, tt, "Close AI requests")
	clickT(t, tt, "AI requests")
	tt.flush()
	d.resetCalls()
	clickT(t, tt, "Resume")
	tt.flush()
	if hasCall(d, "servers.setAutoAllow") {
		t.Fatal("Resume worked at once in the reopened AI column")
	}
	clickT(t, tt, "Close AI requests")
	clickT(t, tt, "Hosts")
	tt.flush()
	clickT(t, tt, "Resume")
	tt.flush()
	if hasCall(d, "servers.setAutoAllow") {
		t.Fatal("Resume worked at once on Hosts after a tab")
	}
	pass(c, tt)
	clickT(t, tt, "Resume")
	tt.flush()
	if !hasCall(d, "servers.setAutoAllow") {
		t.Fatal("Resume did not work once the delay passed")
	}
}

func TestRanWhileLockedBanner(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true, Locked: true}, pw: "right", ran: []hubapi.RanLocked{{Server: "box", Count: 1}}}
	a, tt, _, _ := newApp(t, d)
	a.Update(func() { a.unlock("right") })
	tt.flush()
	const ran = "While the app was locked the AI ran 1 command on box. See the Audit tab."
	if !tt.HasText(ran) {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Dismiss")
	if tt.HasText(ran) {
		t.Fatal("Dismiss left the banner")
	}
	lockNow(t, a, tt, d)
	a.Update(func() { a.unlock("right") })
	tt.flush()
	if !tt.HasText(ran) {
		t.Fatal("a second soft lock's runs did not show")
	}
	lockNow(t, a, tt, d)
	d.set(func() { d.ran = nil })
	a.Update(func() { a.unlock("right") })
	tt.flush()
	if tt.HasText(ran) {
		t.Fatal("the last lock's runs outlived the next lock")
	}
}
