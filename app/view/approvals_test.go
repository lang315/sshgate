package view

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
)

func colApp(t *testing.T) (*App, *tester, *clock, *fakeDoor) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}}
	a, tt, c, _ := newApp(t, d)
	tt.flush()
	return a, tt, c, d
}

func decided(d *fakeDoor) string { d.mu.Lock(); defer d.mu.Unlock(); return d.params["decide"] }

func TestColumnOpensItselfAndAllowWaits(t *testing.T) {
	a, tt, c, d := colApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "echo approved")})
	if !tt.HasText("echo approved") || !tt.HasText("Commands wait for you unless their host is on auto-allow. Enter in a request denies; Allow takes a mouse click.") {
		t.Fatal(tt.Texts())
	}
	_ = tt.Click("Allow")
	tt.flush()
	if decided(d) != "" {
		t.Fatal("Allow worked before 500 ms")
	}
	c.add(500 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Allow"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if decided(d) != `{"id":"r1","outcome":"allowed","reason":""}` {
		t.Fatal(decided(d))
	}
}

func TestEnterInReasonDenies(t *testing.T) {
	a, tt, _, d := colApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r2", "box", "rm -rf /")})
	if err := tt.Click("Reason (optional)"); err != nil {
		t.Fatal(err)
	}
	tt.Type("not today")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if decided(d) != `{"id":"r2","outcome":"denied","reason":"not today"}` {
		t.Fatal(decided(d))
	}
}

func TestArrivingRequestNeverTakesFocus(t *testing.T) {
	a, tt, _, _ := colApp(t)
	if err := tt.Click("Search hosts"); err != nil {
		t.Fatal(err)
	}
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "ls")})
	if !tt.Focused("Search hosts") {
		t.Fatal("the arriving request took the focus")
	}
}

func TestTabNeverReachesAllowAndSpaceOnlyPressesDeny(t *testing.T) {
	a, tt, c, d := colApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("k1", "box", "echo k1")})
	c.add(time.Second)
	tt.Frame()
	if err := tt.Click("Reason (optional)"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeySpace)
	tt.flush()
	if decided(d) != "" {
		t.Fatal("Space in Reason decided")
	}
	for i := 0; i < 30; i++ {
		tt.Key(0, ui.KeyTab)
		if tt.Focused("Allow") || tt.Focused("Send to tab") {
			t.Fatal("Tab reached a mouse-only button")
		}
	}
	if err := tt.Click("Reason (optional)"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Deny") {
		t.Fatal("Tab from Reason did not land on Deny")
	}
	tt.Key(0, ui.KeySpace)
	tt.flush()
	if !strings.Contains(decided(d), `"outcome":"denied"`) {
		t.Fatal(decided(d))
	}
}

func TestListChangeRestartsTheDelayAndDenyAllNeedsTwo(t *testing.T) {
	a, tt, c, _ := colApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r4", "box", "echo first")})
	note(t, a, tt, "pending", map[string]any{"request": reqN("r5", "box", "echo second")})
	c.add(time.Second)
	tt.Frame()
	note(t, a, tt, "decided", map[string]any{"request": reqN("r4", "box", "echo first"), "decision": map[string]string{"outcome": "allowed"}})
	// the remaining request's Allow must be disabled right after the list shrank
	d := a.Door.(*fakeDoor)
	_ = tt.Click("Allow")
	tt.flush()
	if decided(d) != "" {
		t.Fatal("Allow worked right after the list changed")
	}
	_ = tt.Click("Deny all")
	tt.flush()
	for _, m := range d.callList() {
		if m == "denyAll" {
			t.Fatal("Deny all worked with one request")
		}
	}
}

func TestReopeningRestartsTheDelay(t *testing.T) {
	a, tt, c, d := colApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("c1", "box", "echo c1")})
	c.add(time.Second)
	tt.Frame()
	if err := tt.Click("Close AI requests"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.AIOpen() || tt.HasText("echo c1") {
		t.Fatal("column still open")
	}
	if err := tt.Click("AI requests"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.Dial = (&dialRec{}).dial
	_ = tt.Click("Allow")
	_ = tt.Click("Send to tab")
	tt.flush()
	if decided(d) != "" || len(a.tabs.Tabs) != 0 {
		t.Fatal("Allow or Send to tab worked right after the column reopened")
	}
}

func TestScrollRestartsTheDelay(t *testing.T) {
	a, tt, c, d := colApp(t)
	tt.SetSize(800, 400) // the list overflows, and its first request is still in view on every OS
	for _, id := range []string{"s1", "s2", "s3", "s4", "s5"} {
		note(t, a, tt, "pending", map[string]any{"request": reqN(id, "box", "echo "+id)})
	}
	c.add(time.Second)
	tt.Frame()
	if a.col.scroll.MaxY <= 0 {
		t.Fatal("the request list does not scroll at 800x300")
	}
	r, _ := tt.Find("echo s1")
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Move(x, y)
	tt.Scroll(x, y, 0, 120)
	tt.Frame()
	if a.col.scroll.Y == 0 {
		t.Fatal("the wheel did not scroll the list")
	}
	_ = tt.Click("Allow")
	tt.flush()
	if decided(d) != "" {
		t.Fatal("Allow worked right after a scroll")
	}
	_ = tt.Click("Deny all")
	tt.flush()
	if d.params["denyAll"] != `{"reason":"denied all by user"}` {
		t.Fatal(d.params["denyAll"])
	}
}

func TestStdinRequestDisablesSendToTabOnly(t *testing.T) {
	a, tt, c, d := colApp(t)
	// The card with a stdin block is as tall as the 500 px window's scroll area
	// on macOS fonts and taller on others; a button scrolled out has no area.
	tt.SetSize(800, 800)
	r := reqN("st", "box", "cat")
	r.Stdin = "a\r\nb\n"
	note(t, a, tt, "pending", map[string]any{"request": r})
	if !tt.HasText("stdin · 5 bytes, 2 lines") || !tt.HasText("␍") {
		t.Fatal(tt.Texts())
	}
	c.add(time.Second)
	tt.Frame()
	a.Dial = (&dialRec{}).dial
	_ = tt.Click("Send to tab")
	tt.flush()
	if decided(d) != "" || len(a.tabs.Tabs) != 0 || tt.HasText("Send to tab is not available for a command with stdin") {
		t.Fatal("Send to tab must stay disabled for a command with stdin")
	}
	if err := tt.Click("Allow"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if !strings.Contains(decided(d), `"outcome":"allowed"`) {
		t.Fatal(decided(d))
	}
}

func TestItemEscapesServerTextAndMarksCommand(t *testing.T) {
	a, tt, _, _ := colApp(t)
	r := reqN("x1", "bo\u202ex", "curl g\u0456thub.com")
	r.Sudo = true
	r.Description = "fix\u200bit"
	r.Client = "claude-code"
	note(t, a, tt, "pending", map[string]any{"request": r})
	// A bidi override inside a command shows as its escape, not raw.
	note(t, a, tt, "pending", map[string]any{"request": reqN("x2", "box", "ls \u202etxt.exe")})
	for _, s := range []string{`bo\u202ex`, "SUDO", "test@127.0.0.1:22", "1 non-ASCII character highlighted (U+0456)", "AI's description · unverified", `fix\u200bit`, "timeout 60s · client claude-code (unverified)", `ls \u202etxt.exe`, "1 non-ASCII character highlighted (U+202E)"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	for _, s := range tt.Texts() {
		if strings.ContainsRune(s, '\u202e') || strings.ContainsRune(s, '\u200b') {
			t.Fatalf("raw hidden character shown: %q", s)
		}
	}
}

func TestDecidedRequestDropsItsState(t *testing.T) {
	a, tt, _, d := colApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("p1", "box", "ls")})
	d.set(func() { d.errs = map[string]error{"decide": errors.New("request not found")} })
	if err := tt.Click("Reason (optional)"); err != nil {
		t.Fatal(err)
	}
	tt.Type("no")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if !tt.HasText("request not found") {
		t.Fatal(tt.Texts())
	}
	note(t, a, tt, "decided", map[string]any{"request": reqN("p1", "box", "ls"), "decision": map[string]string{"outcome": "denied"}})
	if len(a.col.reasons) != 0 || len(a.itemErr) != 0 || len(a.itemBusy) != 0 {
		t.Fatalf("reasons %v errs %v busy %v kept after the decision", a.col.reasons, a.itemErr, a.itemBusy)
	}
}

func TestAutoFeedAndStopAll(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box", AutoAllow: &hubapi.AutoAllow{Forever: true}}}}
	a, tt, _, _ := newApp(t, d)
	tt.flush()
	a.Update(func() { a.aiOpen = true })
	tt.flush()
	if !tt.HasText("Nothing ran on auto-allow.") {
		t.Fatal(tt.Texts())
	}
	code := 0
	note(t, a, tt, "autoAllow.ran", map[string]any{"server": "box", "command": "echo auto-2", "description": "", "exitCode": code, "time": "2026-10-15T12:00:00Z", "sudo": true, "stdinBytes": 8})
	for _, s := range []string{"Auto-allowed", "echo auto-2", "sudo", "stdin 8 B", "exit 0"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if err := tt.Click("Stop all auto-allow"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if d.params["servers.setAutoAllow"] != `{"mode":"off","server":"box"}` {
		t.Fatal(d.params["servers.setAutoAllow"])
	}
}

func TestLockAndUnlockRestartTheDelay(t *testing.T) {
	a, tt, c, d := colApp(t)
	r := reqN("l1", "box", "echo l1")
	d.set(func() { d.pending = []hubapi.Request{r} })
	note(t, a, tt, "pending", map[string]any{"request": r})
	c.add(time.Second)
	tt.Frame()
	d.set(func() { d.status.Locked = true })
	note(t, a, tt, "locked", map[string]string{"reason": "manual"})
	if !tt.HasText("Master password") {
		t.Fatal("not locked:", tt.Texts())
	}
	if a.col.mounted {
		t.Fatal("the column is still mounted on the locked screen")
	}
	d.set(func() { d.status.Locked = false })
	a.Update(func() { a.refresh() })
	tt.flush()
	if !tt.HasText("echo l1") {
		t.Fatal("request not shown after unlock:", tt.Texts())
	}
	_ = tt.Click("Allow")
	tt.flush()
	if decided(d) != "" {
		t.Fatal("Allow worked right after unlock")
	}
	c.add(600 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Allow"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if decided(d) != `{"id":"l1","outcome":"allowed","reason":""}` {
		t.Fatal(decided(d))
	}
}

// TestFullFeedLeavesRequestReachable: 50 auto runs must not push a pending
// request out of the column (Electron caps .autofeed at 45% and scrolls it).
func TestFullFeedLeavesRequestReachable(t *testing.T) {
	a, tt, c, d := colApp(t)
	tt.SetSize(1000, 640)
	a.Update(func() {
		for i := 0; i < 50; i++ {
			b, _ := json.Marshal(map[string]any{"server": "box", "command": fmt.Sprintf("echo auto-%d", i), "description": "", "exitCode": 0, "time": "2026-10-15T12:00:00Z"})
			a.Note("autoAllow.ran", b)
		}
	})
	tt.flush()
	note(t, a, tt, "pending", map[string]any{"request": reqN("f1", "box", "echo f1")})
	c.add(time.Second)
	tt.Frame()
	allow, ok := tt.Find("Allow")
	if !ok || allow.H <= 0 || allow.Y < 0 || allow.Y+allow.H > 640 {
		t.Fatalf("Allow not reachable: %+v %v", allow, ok)
	}
	if a.col.feed.MaxY <= 0 {
		t.Fatal("the feed does not scroll")
	}
	r, ok := tt.Find("echo auto-49")
	if !ok {
		t.Fatal("newest feed entry not shown")
	}
	tt.Move(r.X, r.Y)
	tt.Scroll(r.X, r.Y, 0, 300)
	tt.Frame()
	if a.col.feed.Y == 0 || a.col.scroll.Y != 0 {
		t.Fatalf("feed Y %v request list Y %v: the wheel must scroll the feed only", a.col.feed.Y, a.col.scroll.Y)
	}
	if moved, _ := tt.Find("Allow"); moved != allow {
		t.Fatalf("scrolling the feed moved Allow: %+v -> %+v", allow, moved)
	}
	if err := tt.Click("Allow"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if decided(d) != `{"id":"f1","outcome":"allowed","reason":""}` {
		t.Fatal(decided(d))
	}
}

// TestBannersNeverMoveAllow: the alert and store-error banners sit beside
// the column, so one appearing cannot shift Allow under the cursor without
// restarting the delay.
func TestBannersNeverMoveAllow(t *testing.T) {
	a, tt, c, d := colApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("b1", "box", "echo b1")})
	c.add(time.Second)
	tt.Frame()
	before, _ := tt.Find("Allow")
	a.Update(func() {
		a.alert = "could not open box"
		a.status.StoreError = "the vault file could not be read"
	})
	tt.flushOnce()
	if !tt.HasText("could not open box") || !tt.HasText("The vault file could not be read") {
		t.Fatal(tt.Texts())
	}
	if after, _ := tt.Find("Allow"); after != before {
		t.Fatalf("a banner moved Allow: %+v -> %+v", before, after)
	}
	if err := tt.Click("Allow"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if decided(d) != `{"id":"b1","outcome":"allowed","reason":""}` {
		t.Fatal(decided(d))
	}
}

func TestTransferRequestShowsPathsAndDisablesSendToTab(t *testing.T) {
	a, tt, c, d := colApp(t)
	r := reqN("tr", "box", "upload 2 path(s) to /srv/app")
	r.Transfer = &hubapi.Transfer{Op: "upload", Root: "/Users/me/proj", Local: []string{"dist", "README.md"}, Remote: []string{"/srv/app"}, Overwrite: true}
	note(t, a, tt, "pending", map[string]any{"request": r})
	for _, s := range []string{"UPLOAD", "from /Users/me/proj", "dist", "README.md", "to /srv/app", "overwrites existing files"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	c.add(time.Second)
	tt.Frame()
	a.Dial = (&dialRec{}).dial
	_ = tt.Click("Send to tab")
	tt.flush()
	if decided(d) != "" || len(a.tabs.Tabs) != 0 {
		t.Fatal("Send to tab must stay disabled for a transfer")
	}
}

// allowNodes are the Allow buttons of the last frame, top to bottom.
func allowNodes(tt *tester) []ui.TestNode {
	var out []ui.TestNode
	for _, n := range tt.Nodes() {
		if n.Label == "Allow" {
			out = append(out, n)
		}
	}
	return out
}

// adv moves the app's clock (the Allow delay) and the Tester's (the fades) together.
func adv(tt *tester, c *clock, d time.Duration) { c.add(d); tt.Advance(d) }

func pendingList(d *fakeDoor, rs ...hubapi.Request) { d.set(func() { d.pending = rs }) }

func TestArrivingRequestFadesWithoutMovingOthers(t *testing.T) {
	a, tt, c, d := colApp(t)
	tt.SetSize(800, 1200) // both cards on screen
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "echo one")})
	adv(tt, c, time.Second)
	tt.Frame()
	first := allowNodes(tt)
	if len(first) != 1 || first[0].Disabled {
		t.Fatalf("r1 Allow %+v", first)
	}
	r1 := first[0].Bounds
	pendingList(d, reqN("r1", "box", "echo one"), reqN("r2", "box", "echo two"))
	note(t, a, tt, "pending", map[string]any{"request": reqN("r2", "box", "echo two")})
	adv(tt, c, 40*time.Millisecond)
	now := allowNodes(tt)
	if len(now) != 2 || now[0].Bounds != r1 || !now[1].Disabled || !tt.FramePending() {
		t.Fatalf("at +40 ms: %+v pending %v", now, tt.FramePending())
	}
	cmd, ok := tt.Find("echo two")
	if !ok {
		t.Fatal(tt.Texts())
	}
	card := grow(cmd, 20) // Allow-free: only the fade can change it
	if _, fading := a.col.fading["r2"]; !fading {
		t.Fatal("r2 is not in fading at +40 ms")
	}
	mid := shot(tt)
	adv(tt, c, 459*time.Millisecond) // +499 ms
	tt.Frame()
	if n := allowNodes(tt); !n[1].Disabled {
		t.Fatal("r2 Allow enabled at +499 ms")
	}
	adv(tt, c, 2*time.Millisecond) // +501 ms
	tt.Frame()
	if n := allowNodes(tt); n[1].Disabled {
		t.Fatal("r2 Allow still disabled at +501 ms")
	}
	adv(tt, c, time.Second)
	tt.Frame()
	if sameRegion(mid, shot(tt), card) {
		t.Fatal("r2's command block looks the same mid-fade and at rest")
	}
	if len(a.col.fading) != 0 || tt.FramePending() {
		t.Fatalf("fading %v pending %v", a.col.fading, tt.FramePending())
	}
}

func TestRequestsPresentAtOpenDoNotFade(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, pending: []hubapi.Request{reqN("r1", "box", "ls"), reqN("r2", "box", "pwd")}}
	a, tt, _, _ := newApp(t, d)
	tt.flush()
	_, ok1 := tt.Find("ls")
	_, ok2 := tt.Find("pwd")
	if !a.aiOpen || !ok1 || !ok2 || len(a.Items()) != 2 || len(a.col.fading) != 0 {
		t.Fatalf("open %v ls %v pwd %v items %d fading %v", a.aiOpen, ok1, ok2, len(a.Items()), a.col.fading)
	}
}

func TestRequestGoneMidFadeLeavesNoFade(t *testing.T) {
	a, tt, c, d := colApp(t)
	tt.SetSize(800, 1200) // both cards on screen
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "echo one")})
	adv(tt, c, time.Second)
	pendingList(d, reqN("r1", "box", "echo one"), reqN("r2", "box", "echo two"))
	note(t, a, tt, "pending", map[string]any{"request": reqN("r2", "box", "echo two")})
	adv(tt, c, 40*time.Millisecond)
	if _, ok := a.col.fading["r2"]; !ok {
		t.Fatal("r2 is not fading")
	}
	pendingList(d, reqN("r1", "box", "echo one"))
	note(t, a, tt, "decided", map[string]any{"request": reqN("r2", "box", "echo two"), "decision": map[string]string{"outcome": "denied"}})
	tt.Frame()
	if _, ok := a.col.fading["r2"]; ok {
		t.Fatal("r2 still fading after it was withdrawn")
	}
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("frames still pending")
	}
}

func TestReduceMotionNoArrivalFade(t *testing.T) {
	a, tt, c, _ := colApp(t)
	tt.SetSize(800, 1200) // both cards on screen
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "echo one")})
	adv(tt, c, time.Second)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r2", "box", "echo two")})
	n := allowNodes(tt)
	if len(a.col.fading) != 0 || len(n) != 2 {
		t.Fatalf("fading %v allows %d", a.col.fading, len(n))
	}
	cmd, ok := tt.Find("echo two")
	if !ok {
		t.Fatal(tt.Texts())
	}
	card := grow(cmd, 20) // the command block: r1's Allow turns green meanwhile, so not the whole card
	first := shot(tt)
	adv(tt, c, time.Second)
	tt.Frame()
	if !sameRegion(first, shot(tt), card) {
		t.Fatal("r2's card changed after its first frame")
	}
}

func TestAllowTurnsGreenWhenEnabled(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	a, tt, c, _ := colApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "echo one")})
	tt.Advance(time.Second) // past the fade, not the delay (the app's clock has not moved)
	tt.Frame()
	n := allowNodes(tt)
	if len(n) != 1 || !n[0].Disabled {
		t.Fatalf("allow %+v", n)
	}
	box, ok := grow(n[0].Bounds, 1), palette(false).Ok
	if got := countNear(shot(tt), box, ok, 20); got != 0 {
		t.Fatalf("%d green pixels while disabled", got)
	}
	adv(tt, c, time.Second)
	tt.Frame()
	if got := countNear(shot(tt), box, ok, 20); got <= 10 {
		t.Fatalf("%d green pixels when enabled", got)
	}
}

// A disabled Allow looks like a disabled Send to tab: the same box, border
// and corners (their texts differ, so only the left edge strip is compared).
func TestDisabledAllowLooksLikeMouseButton(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	a, tt, _, _ := colApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "echo one")})
	tt.Advance(time.Second) // the app's clock has not moved: both stay disabled
	tt.Frame()
	var al, st ui.Rect
	for _, n := range tt.Nodes() {
		switch n.Label {
		case "Allow":
			al = n.Bounds
		case "Send to tab":
			st = n.Bounds
		}
	}
	if al.H == 0 || al.H != st.H {
		t.Fatalf("allow %+v send %+v", al, st)
	}
	img := shot(tt)
	strip := func(r ui.Rect) ui.Rect { return ui.Rect{X: r.X - 1, Y: r.Y - 1, W: 8, H: r.H + 2} } // region rounds to pixels
	if string(region(img, strip(al))) != string(region(img, strip(st))) {
		t.Fatal("a disabled Allow's edge differs from a disabled Send to tab's")
	}
}

// The safety cue for sudo: a 4 px red left edge and a SUDO chip on DangerTint,
// neither on a plain card.
func TestSudoCardHasRedEdgeAndChip(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	for _, dark := range []bool{false, true} {
		a, tt, c, _ := colApp(t)
		tt.SetDark(dark)
		tt.SetSize(1000, 900)
		sudo := reqN("s1", "sudobox", "echo sudo")
		sudo.Sudo = true
		note(t, a, tt, "pending", map[string]any{"request": sudo})
		note(t, a, tt, "pending", map[string]any{"request": reqN("p1", "plainbox", "echo plain")})
		adv(tt, c, time.Second)
		tt.Frame()
		img, p := shot(tt), palette(dark)
		edge := func(server string) int {
			r, ok := tt.Find(server)
			if !ok {
				t.Fatal(tt.Texts())
			}
			// the card's left edge is its padding (12) and a border (4 on sudo, 1 on plain) to the left of the name
			return countNear(img, ui.Rect{X: r.X - 16, Y: r.Y, W: 4, H: r.H}, p.Danger, 30)
		}
		if n := edge("sudobox"); n < 3*10 {
			t.Fatalf("dark=%v: sudo card's left strip has %d Danger pixels", dark, n)
		}
		if n := edge("plainbox"); n != 0 {
			t.Fatalf("dark=%v: plain card's left strip has %d Danger pixels", dark, n)
		}
		chip, ok := tt.Find("SUDO")
		if !ok {
			t.Fatal(tt.Texts())
		}
		if n := countNear(img, grow(chip, 4), p.DangerTint, 3); n == 0 {
			t.Fatalf("dark=%v: no DangerTint pixels around the SUDO chip", dark)
		}
	}
}

// Deny sets its own background, so it paints its own hover colour.
func TestDenyHoverColour(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	a, tt, c, _ := colApp(t)
	tt.SetSize(1000, 900)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "echo one")})
	adv(tt, c, time.Second)
	tt.Frame()
	var r ui.Rect
	for _, n := range tt.Nodes() {
		if n.Label == "Deny" && n.Role == ui.RoleButton {
			r = n.Bounds
		}
	}
	if r.W == 0 {
		t.Fatal("no Deny button")
	}
	patch := ui.Rect{X: r.X + 5, Y: r.Y + r.H/2 - 1, W: 3, H: 3} // inside the button, left of its label
	p := palette(false)
	if n := countNear(shot(tt), patch, p.DangerFill, 3); n != 9 {
		t.Fatalf("at rest: %d of 9 pixels on DangerFill", n)
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(time.Second)
	tt.Frame()
	if n := countNear(shot(tt), patch, p.DangerFillHover, 3); n != 9 {
		t.Fatalf("hovered: %d of 9 pixels on DangerFillHover", n)
	}
}
