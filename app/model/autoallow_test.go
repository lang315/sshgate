package model

import (
	"reflect"
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
