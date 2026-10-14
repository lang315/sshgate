package view

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/termconn"
)

// sendApp is tabApp with the clock, a request r1 for box shown, and the
// Allow delay passed.
func sendApp(t *testing.T) (*App, *tester, *fakeDoor, *hubDoor, *termconn.Mux) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box", User: "test", Host: "127.0.0.1", Port: 2222}}}
	a, tt, c, _ := newApp(t, d)
	hd, m := &hubDoor{}, termconn.NewMux()
	a.Dial = func(ctx context.Context, id, server string, trust *hubapi.Trust) (*termconn.Conn, error) {
		return termconn.Open(ctx, hd, m, id, server, 24, 80, trust)
	}
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "echo sent")})
	c.add(time.Second)
	tt.Frame()
	return a, tt, d, hd, m
}

func click(t *testing.T, tt *tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.flush()
}

func TestSendToTabOpensATabPastesOnOutputThenDecides(t *testing.T) {
	needLib(t)
	a, tt, d, hd, m := sendApp(t)
	d.resetCalls()
	click(t, tt, "Send to tab")
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" && a.tabs.Active == "t1" }, "a new box tab")
	if decided(d) != "" || strings.Contains(hd.dataFor("t1"), "echo sent") {
		t.Fatal("pasted or decided before the remote printed anything")
	}
	if calls := d.callList(); len(calls) != 0 {
		t.Fatalf("waiting for the tab called the hub: %v", calls) // local state and a timer only
	}
	hubNote(m, "term.data", map[string]any{"id": "t1", "data": []byte("$ ")})
	tabsUntil(t, tt, func() bool { return decided(d) != "" }, "the decision")
	if decided(d) != `{"id":"r1","outcome":"sent_to_tab","reason":""}` {
		t.Fatal(decided(d))
	}
	if got := hd.dataFor("t1"); got != "echo sent" {
		t.Fatalf("the tab got %q: the command alone, no Enter", got)
	}
}

func TestSendToTabUsesTheMostRecentTab(t *testing.T) {
	needLib(t)
	a, tt, d, hd, m := sendApp(t)
	a.Update(func() { a.openTab("box"); a.openTab("box") })
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" && tabState(a, "t2") == "open" }, "two tabs")
	hubNote(m, "term.data", map[string]any{"id": "t1", "data": []byte("$ ")})
	hubNote(m, "term.data", map[string]any{"id": "t2", "data": []byte("$ ")})
	a.Update(func() { a.activate("t1") })
	a.Update(func() { a.tabs.ShowHome() })
	tt.flush()
	click(t, tt, "Send to tab")
	tabsUntil(t, tt, func() bool { return decided(d) != "" }, "the decision")
	if hd.dataFor("t1") != "echo sent" || hd.dataFor("t2") != "" || len(a.tabs.Tabs) != 2 || a.tabs.Active != "t1" {
		t.Fatalf("t1 %q t2 %q tabs %d active %q", hd.dataFor("t1"), hd.dataFor("t2"), len(a.tabs.Tabs), a.tabs.Active)
	}
}

func TestSendToTabGivesUpAfterTheTimeout(t *testing.T) {
	old := sendReadyTimeout
	sendReadyTimeout = 50 * time.Millisecond
	t.Cleanup(func() { sendReadyTimeout = old })
	a, tt, d, _, _ := sendApp(t)
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	a.Dial = func(ctx context.Context, _, _ string, _ *hubapi.Trust) (*termconn.Conn, error) {
		<-gate
		return nil, ctx.Err()
	}
	click(t, tt, "Send to tab")
	tabsUntil(t, tt, func() bool { return tt.HasText("terminal did not become ready") }, "the timeout")
	if decided(d) != "" || len(a.Items()) != 1 || a.itemBusy["r1"] {
		t.Fatal("a failed Send to tab must leave the request pending and usable")
	}
}

func TestSendToTabFailsWhenItsTabCloses(t *testing.T) {
	a, tt, d, _, _ := sendApp(t)
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	a.Dial = func(ctx context.Context, _, _ string, _ *hubapi.Trust) (*termconn.Conn, error) {
		<-gate
		return nil, ctx.Err()
	}
	click(t, tt, "Send to tab")
	click(t, tt, "Close tab")
	if !tt.HasText("terminal closed") || decided(d) != "" || a.itemBusy["r1"] {
		t.Fatal(tt.Texts())
	}
}

// TestSendToTabRetryOutlivesTheFirstTimer: a retry after the first
// attempt's tab closed gets its own full wait; the first attempt's timer,
// still running, must not end it.
func TestSendToTabRetryOutlivesTheFirstTimer(t *testing.T) {
	old := sendReadyTimeout
	sendReadyTimeout = 3 * time.Second
	t.Cleanup(func() { sendReadyTimeout = old })
	a, tt, _, _, _ := sendApp(t)
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	a.Dial = func(ctx context.Context, _, _ string, _ *hubapi.Trust) (*termconn.Conn, error) {
		<-gate
		return nil, ctx.Err()
	}
	start := time.Now()
	click(t, tt, "Send to tab")
	click(t, tt, "Close tab")
	if !tt.HasText("terminal closed") {
		t.Fatal(tt.Texts())
	}
	time.Sleep(time.Until(start.Add(time.Second)))
	retry := time.Now()
	if d := retry.Sub(start); d > 2*time.Second {
		t.Fatalf("the first attempt took %v: too slow to retry well before its timer", d)
	}
	// Called directly: the error text moved the list, so the button waits
	// 500 ms on the test's clock.
	a.Update(func() { a.sendToTab(a.items[0].Request) })
	tabsUntil(t, tt, func() bool { return tt.HasText("terminal did not become ready") }, "the retry's timeout")
	if d := time.Since(retry); d < sendReadyTimeout {
		t.Fatalf("the retry ended after %v: the first attempt's timer ended it", d)
	}
}

// TestSendToTabSkipsARequestDecidedMeanwhile: a request decided while its
// tab opened is never pasted.
func TestSendToTabSkipsARequestDecidedMeanwhile(t *testing.T) {
	needLib(t)
	a, tt, d, hd, m := sendApp(t)
	click(t, tt, "Send to tab")
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" }, "the tab")
	note(t, a, tt, "decided", map[string]any{"request": reqN("r1", "box", "echo sent"), "decision": map[string]string{"outcome": "denied"}})
	hubNote(m, "term.data", map[string]any{"id": "t1", "data": []byte("$ ")})
	tabsUntil(t, tt, func() bool { t1, _ := a.tabs.Get("t1"); return t1.SawOutput }, "the output")
	if hd.dataFor("t1") != "" || decided(d) != "" || len(a.sends) != 0 {
		t.Fatalf("pasted %q, decided %q", hd.dataFor("t1"), decided(d))
	}
}

// TestSendToTabWaitingAcrossAHubStopLeavesNoBusyFlag: a send waiting when
// the hub stops must not leave its request busy after the restart.
func TestSendToTabWaitingAcrossAHubStopLeavesNoBusyFlag(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}}
	a, tt, c, _ := newApp(t, d)
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	a.Dial = func(ctx context.Context, _, _ string, _ *hubapi.Trust) (*termconn.Conn, error) {
		<-gate
		return nil, ctx.Err()
	}
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "echo sent")})
	c.add(time.Second)
	tt.Frame()
	click(t, tt, "Send to tab")
	if !a.itemBusy["r1"] || len(a.sends) != 1 {
		t.Fatal("the send is not waiting")
	}
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", Attempt: 1, In: time.Second}) })
	tt.flush()
	running(a, tt)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "echo sent")})
	c.add(time.Second)
	tt.Frame()
	click(t, tt, "Allow")
	tabsUntil(t, tt, func() bool { return decided(d) != "" }, "the decision")
	if decided(d) != `{"id":"r1","outcome":"allowed","reason":""}` {
		t.Fatal(decided(d))
	}
}

// TestSendToTabWaitingAcrossALockIsNotPasted: a send waiting when the
// vault locks fails; the tab's first output, behind the lock screen, gets
// no paste and the request is not decided.
func TestSendToTabWaitingAcrossALockIsNotPasted(t *testing.T) {
	needLib(t)
	a, tt, d, hd, m := sendApp(t)
	click(t, tt, "Send to tab")
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" }, "the tab")
	d.set(func() { d.status = hubapi.Status{HasVault: true, Locked: true} })
	note(t, a, tt, "locked", map[string]string{"reason": "manual"})
	hubNote(m, "term.data", map[string]any{"id": "t1", "data": []byte("$ ")})
	tabsUntil(t, tt, func() bool { t1, _ := a.tabs.Get("t1"); return t1.SawOutput }, "the output")
	if hd.dataFor("t1") != "" || decided(d) != "" {
		t.Fatalf("pasted %q, decided %q behind the lock", hd.dataFor("t1"), decided(d))
	}
	if len(a.Items()) != 1 || a.itemBusy["r1"] || a.itemErr["r1"] != "vault locked" {
		t.Fatalf("items %d busy %v err %q: the request must stay pending and usable", len(a.Items()), a.itemBusy["r1"], a.itemErr["r1"])
	}
}

func TestSendToTabWaitsForTheDelay(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}}
	a, tt, _, _ := newApp(t, d)
	a.Dial = (&dialRec{}).dial
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "echo sent")})
	_ = tt.Click("Send to tab")
	tt.flush()
	if len(a.tabs.Tabs) != 0 {
		t.Fatal("Send to tab worked before 500 ms")
	}
}
