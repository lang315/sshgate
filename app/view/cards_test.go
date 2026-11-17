package view

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
)

// The timed chip, the AI button's count and the card's Stop follow the
// app's clock; the redraw asks the hub nothing.
func TestChipTicksFromTheAppClock(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Until: time.Date(2026, 10, 15, 12, 14, 12, 0, time.UTC).Format(time.RFC3339)}
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{s}}
	_, tt, c, _ := newApp(t, d)
	tt.flush()
	if _, ok := tt.Find("Stop auto-allow box"); !ok || !tt.HasText("Auto 15m") || !tt.HasText("auto 1") {
		t.Fatalf("texts %q", tt.Texts())
	}
	d.resetCalls()
	c.add(10 * time.Minute)
	tt.Frame()
	if !tt.HasText("Auto 5m") {
		t.Fatalf("the chip did not tick: %q", tt.Texts())
	}
	c.add(5 * time.Minute)
	tt.Frame()
	if _, ok := tt.Find("Stop auto-allow box"); ok || tt.HasText("Auto 0m") || tt.HasText("auto 1") {
		t.Fatalf("an ended grant still shows: %q", tt.Texts())
	}
	if got := d.callList(); len(got) != 0 {
		t.Fatalf("the clock called %v", got)
	}
}

func TestCardStopEndsTheGrant(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true, Paused: true}
	_, tt, d := readyApp(t, []hubapi.Server{s})
	if !tt.HasText("Auto paused") {
		t.Fatal(tt.Texts())
	}
	d.resetCalls()
	clickT(t, tt, "Stop auto-allow box")
	tt.flush()
	if params(d, "servers.setAutoAllow") != `{"mode":"off","server":"box"}` || !hasCall(d, "servers") {
		t.Fatalf("calls %v %s", d.callList(), params(d, "servers.setAutoAllow"))
	}
	d.set(func() { d.errs = map[string]error{"servers.setAutoAllow": errors.New("boom")} })
	clickT(t, tt, "Stop auto-allow box")
	tt.flush()
	if !tt.HasText("Could not stop auto-allow on box: boom") {
		t.Fatal(tt.Texts())
	}
}

// A stop whose reply lands behind the lock screen changes nothing there.
func TestStopReplyBehindTheLockChangesNothing(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true}
	a, tt, d := readyApp(t, []hubapi.Server{s})
	gate := make(chan struct{})
	d.set(func() {
		d.gates = map[string]chan struct{}{"servers.setAutoAllow": gate}
		d.errs = map[string]error{"servers.setAutoAllow": errors.New("Vault is locked; unlock it in the app")}
	})
	clickT(t, tt, "Stop auto-allow box")
	lockNow(t, a, tt, d)
	d.resetCalls()
	close(gate)
	tt.flush()
	if a.alert != "" || hasCall(d, "servers") {
		t.Fatalf("alert %q, calls %v after a reply behind the lock", a.alert, d.callList())
	}
}

func TestTabDotFollowsTheGrant(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true}
	a, tt, _ := readyApp(t, []hubapi.Server{s})
	a.Update(func() { a.tabs.Open("box") })
	tt.flush()
	if _, ok := tt.Find("auto-allow on"); !ok {
		t.Fatal("no auto-allow dot on box's tab")
	}
	note(t, a, tt, "autoAllow.off", map[string]string{"server": "box", "reason": "turned off"})
	if _, ok := tt.Find("auto-allow on"); ok {
		t.Fatal("the dot outlived the grant")
	}
}

// TestCardActionsStayInTheCard: with Stop shown, the five actions still end
// inside the card, not past its right edge; a card without chips keeps its
// neighbour's top and bottom, its actions on the same line.
func TestCardActionsStayInTheCard(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true}
	plain := boxServer
	plain.Name = "plain"
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{s, plain}}
	_, tt, _, _ := newApp(t, d)
	tt.flush()
	open, ok1 := tt.Find("Open box")
	del, ok2 := tt.Find("Delete box")
	if !ok1 || !ok2 {
		t.Fatalf("texts %q", tt.Texts())
	}
	if del.X+del.W > open.X+open.W+0.5 {
		t.Fatalf("Delete ends at %v, past the card's content edge %v", del.X+del.W, open.X+open.W)
	}
	if other, ok := tt.Find("Open plain"); !ok || other.Y != open.Y {
		t.Fatalf("the chipless card's top is at %v, its neighbour's at %v", other.Y, open.Y)
	}
	// Stop, the tallest action, sets the bottom of its card's actions.
	stop, _ := tt.Find("Stop auto-allow box")
	if other, ok := tt.Find("Delete plain"); !ok || math.Abs(float64(other.Y+other.H-stop.Y-stop.H)) > 0.5 {
		t.Fatalf("the chipless card's actions end at %v, its neighbour's at %v", other.Y+other.H, stop.Y+stop.H)
	}
}

// The dot is decoration with a label: it shows while a terminal is open.
func TestCardConnectedTile(t *testing.T) {
	needLib(t)
	a, tt, _, _, _ := tabApp(t)
	if _, ok := tt.Find("connected"); ok {
		t.Fatal("connected dot with no terminal open")
	}
	clickT(t, tt, "Open box")
	tabsUntil(t, tt, func() bool { return tabState(a, "t1") == "open" }, "t1 open")
	a.Update(func() { a.tabs.ShowHome() })
	tt.flush()
	if _, ok := tt.Find("connected"); !ok {
		t.Fatalf("no connected dot: %q", tt.Texts())
	}
	a.Update(func() { a.closeTab("t1") })
	tt.flush()
	if _, ok := tt.Find("connected"); ok {
		t.Fatal("connected dot after the tab closed")
	}
}

// At the window minimum the AI column leaves ~320 DIP for the cards.
func TestCardFitsBesideAIColumn(t *testing.T) {
	var servers []hubapi.Server
	for _, n := range []string{"host1", "host2", "host3"} {
		servers = append(servers, hubapi.Server{Name: n, User: "u", Host: n + ".example", Port: 22})
	}
	a, tt, _ := readyApp(t, servers)
	tt.SetSize(900, 500)
	a.Update(func() { a.aiOpen = true })
	tt.flush()
	r, ok := tt.Find("Open host1")
	if !ok || r.X+r.W > 900-380 {
		t.Fatalf("card at %+v (%v), AI column starts at %d", r, ok, 900-380)
	}
}

func TestStopAutoAllowStillOnCard(t *testing.T) {
	s := autoBox()
	s.AutoAllow = &hubapi.AutoAllow{Forever: true, Paused: true}
	_, tt, _ := readyApp(t, []hubapi.Server{s})
	tt.SetSize(900, 500)
	tt.flush()
	if _, ok := tt.Find("Stop auto-allow box"); !ok {
		t.Fatal(tt.Texts())
	}
}

// Every hover fades the shadow in and out, not only the first.
func TestHoverCardFadesEveryTime(t *testing.T) {
	servers := []hubapi.Server{{Name: "hostA", User: "u", Host: "a.example", Port: 22}, {Name: "hostB", User: "u", Host: "b.example", Port: 22}}
	a, tt, _ := readyApp(t, servers)
	tt.SetSize(900, 500)
	tt.flush()
	over := func(name string) {
		r, ok := tt.Find("Open " + name)
		if !ok {
			t.Fatalf("no card %s", name)
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
	}
	over("hostA")
	tt.Advance(200 * time.Millisecond)
	if got := a.lift["hostA"]; got != 1 {
		t.Fatalf("after 200 ms hover lift %v", got)
	}
	over("hostB")
	tt.Advance(200 * time.Millisecond)
	if _, still := a.lift["hostA"]; still {
		t.Fatalf("A kept animating after it faded out: %v", a.lift)
	}
	over("hostA")
	tt.Advance(40 * time.Millisecond)
	if got := a.lift["hostA"]; got <= 0 || got >= 1 {
		t.Fatalf("second hover jumped: lift %v", got)
	}
	if !tt.FramePending() {
		t.Fatal("no frame pending during the fade")
	}
}

// A focused Open button shows the Accent ring and an unfocused one none.
func TestOpenButtonShowsFocusRing(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	_, tt, _ := readyApp(t, []hubapi.Server{{Name: "build", User: "u", Host: "h", Port: 22}})
	tt.Advance(time.Second)
	tt.Frame()
	var id uint64
	var box ui.Rect
	for _, n := range tt.Nodes() {
		if n.Label == "Open build" {
			id, box = n.ID, grow(n.Bounds, 4)
		}
	}
	if id == 0 {
		t.Fatal("no Open build node")
	}
	acc := palette(false).Accent
	// the tile inside the button is Accent: count the band outside the button only
	band := func() int {
		img := shot(tt)
		return countNear(img, box, acc, 30) - countNear(img, grow(box, -4), acc, 30)
	}
	if n := band(); n != 0 {
		t.Fatalf("%d accent pixels before focus", n)
	}
	tt.FocusNode(id)
	tt.Frame()
	focused := false
	for _, n := range tt.Nodes() {
		focused = focused || n.ID == id && n.Focused
	}
	if !focused {
		t.Fatal("the node is not focused")
	}
	if band() == 0 {
		t.Fatal("no focus ring")
	}
}

// A host that leaves the list stops animating: its lift entry goes with it.
func TestLiftForgetsRemovedHost(t *testing.T) {
	servers := []hubapi.Server{{Name: "hostA", User: "u", Host: "a.example", Port: 22}, {Name: "hostB", User: "u", Host: "b.example", Port: 22}}
	a, tt, _ := readyApp(t, servers)
	tt.SetSize(900, 500)
	tt.flush()
	r, ok := tt.Find("Open hostA")
	if !ok {
		t.Fatal("no card hostA")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(40 * time.Millisecond)
	if a.lift["hostA"] <= 0 {
		t.Fatalf("lift %v", a.lift)
	}
	a.Update(func() { a.servers = a.servers[1:] })
	tt.flushOnce()
	tt.Frame()
	if _, still := a.lift["hostA"]; still {
		t.Fatalf("lift kept the removed host: %v", a.lift)
	}
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("a frame is still pending")
	}
}

// The address in the card header is cut with an ellipsis only when it does
// not fit: root@80.78.27.250:22 fits a card beside no AI column.
func TestCardAddressNotTruncated(t *testing.T) {
	a, tt, _ := readyApp(t, []hubapi.Server{{Name: "fviainboxes-server", User: "root", Host: "80.78.27.250", Port: 22}})
	tt.SetSize(1000, 640)
	a.Update(func() { a.aiOpen = false })
	tt.flush()
	addr, ok := tt.Find("root@80.78.27.250:22")
	if !ok {
		t.Fatalf("address text missing or cut: %q", tt.Texts())
	}
	t.Logf("addr %+v", addr)
	open, _ := tt.Find("Open fviainboxes-server")
	t.Logf("open %+v", open)
	if addr.X+addr.W > open.X+open.W+0.5 {
		t.Fatalf("address ends at %v, past the header at %v", addr.X+addr.W, open.X+open.W)
	}
}
