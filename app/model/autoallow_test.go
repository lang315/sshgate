package model

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

func until(now time.Time, d time.Duration) *hubapi.AutoAllow {
	return &hubapi.AutoAllow{Until: now.Add(d).UTC().Format(time.RFC3339Nano)}
}

func TestChipLabel(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		a    *hubapi.AutoAllow
		want string
	}{
		{until(now, 14200*time.Millisecond*60), "Auto 15m"},
		{until(now, 6*time.Second), "Auto 1m"},
		{until(now, 125*time.Minute), "Auto 2h 5m"},
		{until(now, 120*time.Minute), "Auto 2h"},
		{until(now, -time.Minute), ""},
		{&hubapi.AutoAllow{Forever: true}, "Auto ∞"},
		{&hubapi.AutoAllow{Forever: true, Paused: true}, "Auto paused"},
		{nil, ""},
	} {
		if got := ChipLabel(tc.a, now); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.a, got, tc.want)
		}
	}
}

func TestHostSets(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	list := []hubapi.Server{
		{Name: "a", AutoAllow: until(now, 5*time.Minute)},
		{Name: "b", AutoAllow: &hubapi.AutoAllow{Forever: true}},
		{Name: "c", AutoAllow: &hubapi.AutoAllow{Forever: true, Paused: true}},
		{Name: "d", AutoAllow: until(now, -time.Minute)},
		{Name: "e"},
	}
	var active []bool
	for _, s := range list {
		active = append(active, IsActive(s.AutoAllow, now))
	}
	if !reflect.DeepEqual(active, []bool{true, true, false, false, false}) {
		t.Fatal(active)
	}
	if got := AutoHostNames(list, now); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatal(got)
	}
	if got := PausedHosts(list); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatal(got)
	}
	dropped := DropOnLock(list)
	if dropped[0].AutoAllow != nil || *dropped[1].AutoAllow != (hubapi.AutoAllow{Forever: true, Paused: true}) ||
		*dropped[2].AutoAllow != (hubapi.AutoAllow{Forever: true, Paused: true}) || dropped[3].AutoAllow != nil || dropped[4].AutoAllow != nil {
		t.Fatalf("%+v", dropped)
	}
	if list[0].AutoAllow == nil {
		t.Fatal("DropOnLock changed its input")
	}
	off := ApplyOff(list, "b")
	if off[1].AutoAllow != nil || off[0].AutoAllow == nil || list[1].AutoAllow == nil {
		t.Fatal("ApplyOff")
	}
}

func TestSoftLockLists(t *testing.T) {
	if got := DropLockHost([]string{"a", "b"}, "a"); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatal(got)
	}
	if got := DropLockHost([]string{"a"}, "x"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatal(got)
	}
	if got := LockHostsFrom(nil, map[string]bool{"a": true}); len(got) != 0 || got == nil {
		t.Fatal(got)
	}
	if got := LockHostsFrom([]string{"a", "b"}, map[string]bool{"a": true}); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatal(got)
	}
	if got := LockHostsFrom([]string{"a", "b"}, map[string]bool{}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatal(got)
	}
}

func TestFeed(t *testing.T) {
	var feed []hubapi.AutoRan
	for i := 0; i < 55; i++ {
		feed = PushFeed(feed, hubapi.AutoRan{Command: "c" + string(rune('0'+i%10)), Time: time.Unix(int64(i), 0).String()})
	}
	if len(feed) != FeedCap || feed[0].Time != time.Unix(54, 0).String() {
		t.Fatal(len(feed), feed[0])
	}
	if CommandLabel(hubapi.AutoRan{Command: "c1", Truncated: 42}) != "c1 … (truncated 42 bytes)" || CommandLabel(hubapi.AutoRan{Command: "c1"}) != "c1" {
		t.Fatal("CommandLabel")
	}
}

// Ports of the rest of desktop/test/autoallow.test.ts and the key rows of
// AutoAllowDialog.test.ts.

func TestEnableAllowed(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	ms := func(n int) time.Time { return now.Add(time.Duration(n) * time.Millisecond) }
	for _, tc := range []struct {
		typeName       bool
		typed, refused string
		changed, at    time.Time
		want           bool
	}{
		{false, "", "", now, ms(600), true},
		{false, "", "", now, ms(499), false},
		{false, "", "", ms(200), ms(600), false},
		{true, "", "", now, ms(600), false},
		{true, "box", "", now, ms(600), true},
		{true, "Box", "", now, ms(600), false},
		{false, "", "root login", now, ms(600), false},
	} {
		if got := EnableAllowed(tc.typeName, tc.typed, "box", tc.refused, tc.changed, tc.at); got != tc.want {
			t.Errorf("%+v: got %v", tc, got)
		}
	}
}

func TestTimedNote(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	srv := func(a *hubapi.AutoAllow) *hubapi.Server { return &hubapi.Server{AutoAllow: a} }
	for _, tc := range []struct {
		s    *hubapi.Server
		want string
	}{
		{srv(until(now, 14200*time.Millisecond*60)), "On for 15 more min — saving ends it; pick a duration to keep auto-allow on"},
		{srv(until(now, 6*time.Second)), "On for 1 more min — saving ends it; pick a duration to keep auto-allow on"},
		{srv(until(now, -time.Minute)), ""},
		{srv(&hubapi.AutoAllow{Forever: true}), ""},
		{srv(nil), ""},
		{nil, ""},
	} {
		if got := TimedNote(tc.s, now); got != tc.want {
			t.Errorf("%+v: got %q", tc.s, got)
		}
	}
}

func TestSaveConfirm(t *testing.T) {
	srv := func(a *hubapi.AutoAllow, root, sudo bool) *hubapi.Server {
		return &hubapi.Server{AutoAllow: a, AutoAllowRoot: root, AutoAllowSudo: sudo}
	}
	with := func(s *hubapi.Server, edit func(*HostDraft)) HostDraft {
		d := DraftFrom(s)
		edit(&d)
		return d
	}
	none := srv(nil, false, false)
	armed := srv(&hubapi.AutoAllow{Forever: true}, false, false)
	for _, tc := range []struct {
		name string
		s    *hubapi.Server
		d    HostDraft
		want Confirm
	}{
		{"off", none, with(none, func(d *HostDraft) { d.AutoAllow = "off" }), Confirm{}},
		{"timed", none, with(none, func(d *HostDraft) { d.AutoAllow = "15m" }), Confirm{Needed: true}},
		{"forever newly chosen", none, with(none, func(d *HostDraft) { d.AutoAllow = "forever" }), Confirm{Needed: true, TypeName: true}},
		{"forever armed, same options", srv(&hubapi.AutoAllow{Forever: true}, true, false), DraftFrom(srv(&hubapi.AutoAllow{Forever: true}, true, false)), Confirm{}},
		{"armed, root newly ticked", armed, with(armed, func(d *HostDraft) { d.AutoAllowRoot = true }), Confirm{Needed: true, RootNew: true}},
		{"armed, sudo newly ticked", armed, with(armed, func(d *HostDraft) { d.AutoAllowSudo = true }), Confirm{Needed: true, SudoNew: true}},
		{"paused forever", srv(&hubapi.AutoAllow{Forever: true, Paused: true}, false, false), DraftFrom(srv(&hubapi.AutoAllow{Forever: true, Paused: true}, false, false)), Confirm{Needed: true}},
	} {
		if got := SaveConfirm(tc.s, tc.d); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestDraftRefusal(t *testing.T) {
	pinned := func(refused string) *hubapi.Server {
		return &hubapi.Server{HostKey: "SHA256:x", AutoAllowRefused: refused}
	}
	visible := func(s *hubapi.Server, root bool) HostDraft {
		d := DraftFrom(s)
		d.AIVisible, d.AutoAllowRoot = true, root
		return d
	}
	if got := DraftRefusal(nil, DraftFrom(nil)); got != "no pinned host key" {
		t.Errorf("new host: %q", got)
	}
	if got := DraftRefusal(&hubapi.Server{}, visible(&hubapi.Server{}, false)); got != "no pinned host key" {
		t.Errorf("unpinned: %q", got)
	}
	if got := DraftRefusal(pinned(""), DraftFrom(pinned(""))); got != "not visible to AI" {
		t.Errorf("hidden: %q", got)
	}
	for _, r := range []string{"root login", "has an su password", "has a sudo password"} {
		if got := DraftRefusal(pinned(r), visible(pinned(r), false)); got != r {
			t.Errorf("%s: %q", r, got)
		}
		if got := DraftRefusal(pinned(r), visible(pinned(r), true)); got != "" {
			t.Errorf("%s with Allow on root hosts: %q", r, got)
		}
	}
	if got := DraftRefusal(pinned(""), visible(pinned(""), false)); got != "" {
		t.Errorf("pinned and visible: %q", got)
	}
}

func TestRanLocked(t *testing.T) {
	if got := RanLockedText(nil); got != "" {
		t.Errorf("none: %q", got)
	}
	if got := RanLockedText([]hubapi.RanLocked{{Server: "a", Count: 1}}); got != "While the app was locked the AI ran 1 command on a. See the Audit tab." {
		t.Errorf("one: %q", got)
	}
	if got := RanLockedText([]hubapi.RanLocked{{Server: "a", Count: 2}, {Server: "b", Count: 3}}); got != "While the app was locked the AI ran 5 commands on a, b. See the Audit tab." {
		t.Errorf("five: %q", got)
	}
	ran := []hubapi.RanLocked{{Server: "box", Count: 2}}
	if !reflect.DeepEqual(KeepRan(nil, ran), ran) || !reflect.DeepEqual(KeepRan(ran, []hubapi.RanLocked{}), ran) || !reflect.DeepEqual(KeepRan(ran, nil), ran) {
		t.Error("KeepRan dropped the runs")
	}
}

// The Enable delay's key: what moves the buttons changes it, typing does not.
func TestDialogChangeKey(t *testing.T) {
	pending := DialogChangeKey("forever", false, false, false, nil, false, "")
	root := DialogChangeKey("forever", false, false, false, &hubapi.AutoCheck{UID: 0}, false, "")
	failed := DialogChangeKey("forever", false, false, false, nil, true, "")
	if root == pending || failed == pending || root == failed {
		t.Fatal("the check's state is not in the key")
	}
	for i, k := range []string{
		DialogChangeKey("forever", true, false, false, nil, false, ""),
		DialogChangeKey("forever", false, true, false, nil, false, ""),
		DialogChangeKey("forever", false, false, true, nil, false, ""),
		DialogChangeKey("forever", false, false, false, nil, false, "boom"),
		DialogChangeKey("15m", false, false, false, nil, false, ""),
	} {
		if k == pending {
			t.Errorf("change %d kept the key", i)
		}
	}
	if DialogChangeKey("forever", false, false, false, nil, false, "") != pending {
		t.Error("the same inputs changed the key")
	}
	// The check resolving at T=1000 restarts the delay at once.
	t0 := time.UnixMilli(0)
	lc := NewListChanges(pending, t0)
	lc.SetKey(pending, time.UnixMilli(1000))
	if !lc.At.Equal(t0) {
		t.Fatal("an unchanged key restarted the delay")
	}
	lc.SetKey(root, time.UnixMilli(1000))
	for _, tc := range []struct {
		at   int64
		want bool
	}{{1000, false}, {1499, false}, {1500, true}} {
		if got := EnableAllowed(false, "", "h", "", lc.At, time.UnixMilli(tc.at)); got != tc.want {
			t.Errorf("at %d: %v", tc.at, got)
		}
	}
}

func TestAutoModes(t *testing.T) {
	var labels []string
	for _, m := range AutoModes {
		labels = append(labels, m.Mode+"="+m.Label)
	}
	if got := strings.Join(labels, ","); got != "15m=15 min,30m=30 min,60m=60 min,2h=2 h,4h=4 h,forever=Until turned off" {
		t.Fatal(got)
	}
	if m, ok := AutoModeOf("2h"); !ok || m.Dur != 2*time.Hour {
		t.Fatal(m, ok)
	}
	if _, ok := AutoModeOf("off"); ok {
		t.Fatal("off is a mode")
	}
}
