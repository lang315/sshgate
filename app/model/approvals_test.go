package model

import (
	"reflect"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

var t0 = time.Unix(0, 0)

func ms(n int) time.Time { return t0.Add(time.Duration(n) * time.Millisecond) }

func req(id string) hubapi.Request { return hubapi.Request{ID: id} }

func ids(items []PendingItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Request.ID)
	}
	return out
}

func TestAddPendingOnceRemoveOnDecided(t *testing.T) {
	var items []PendingItem
	items = AddPending(items, req("a"), ms(0))
	items = AddPending(items, req("a"), ms(5))
	items = AddPending(items, req("b"), ms(6))
	if got := ids(items); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatal(got)
	}
	if items[0].ShownAt != ms(0) {
		t.Fatal("a duplicate pending moved shownAt")
	}
	items = RemoveDecided(RemoveDecided(items, "a"), "b")
	if len(items) != 0 {
		t.Fatal(ids(items))
	}
}

func TestAllowEnabled(t *testing.T) {
	it := PendingItem{ShownAt: ms(1000)}
	if AllowEnabled(it, ms(1499), time.Time{}) || !AllowEnabled(it, ms(1500), time.Time{}) {
		t.Fatal("first 500 ms")
	}
	old := PendingItem{ShownAt: ms(0)}
	if AllowEnabled(old, ms(5000), ms(4800)) || !AllowEnabled(old, ms(5300), ms(4800)) {
		t.Fatal("list change restarts the delay")
	}
	if AllowEnabled(PendingItem{ShownAt: ms(1000)}, ms(1400), ms(900)) {
		t.Fatal("shownAt wins over an older change")
	}
}

func TestDelayRemaining(t *testing.T) {
	for _, c := range []struct {
		opened, changed, now time.Time
		want                 time.Duration
	}{
		{ms(1000), time.Time{}, ms(1000), 500 * time.Millisecond},
		{ms(1000), time.Time{}, ms(1300), 200 * time.Millisecond},
		{ms(1000), time.Time{}, ms(1500), 0},
		{ms(1000), time.Time{}, ms(9000), 0},
		{ms(0), ms(4800), ms(5000), 300 * time.Millisecond},
		{ms(1000), ms(900), ms(1400), 100 * time.Millisecond},
	} {
		if got := DelayRemaining(c.opened, c.changed, c.now); got != c.want {
			t.Errorf("DelayRemaining(%v, %v, %v) = %v, want %v", c.opened, c.changed, c.now, got, c.want)
		}
		if DelayElapsed(c.opened, c.changed, c.now) != (c.want == 0) {
			t.Errorf("DelayElapsed disagrees at %v", c.now)
		}
	}
}

func TestClickAllowed(t *testing.T) {
	young := PendingItem{ShownAt: ms(4900)}
	old := PendingItem{ShownAt: ms(0)}
	if ClickAllowed(young, ms(5000), time.Time{}, false) {
		t.Error("young")
	}
	if ClickAllowed(old, ms(5000), time.Time{}, true) {
		t.Error("busy")
	}
	if !ClickAllowed(old, ms(5000), time.Time{}, false) {
		t.Error("old, idle, no change")
	}
	if ClickAllowed(old, ms(5000), ms(4800), false) {
		t.Error("right after a list change")
	}
}

func TestListChanges(t *testing.T) {
	lc := NewListChanges("a", ms(1000))
	if lc.SetHeight(100, ms(1010)) {
		t.Fatal("first measurement is not a change")
	}
	if lc.SetHeight(100, ms(1500)) || lc.At != ms(1000) {
		t.Fatal("same height changed At")
	}
	if !lc.SetHeight(130, ms(2000)) || lc.At != ms(2000) {
		t.Fatal("height change not recorded")
	}
	it := PendingItem{ShownAt: ms(0)}
	if AllowEnabled(it, ms(2499), lc.At) || !AllowEnabled(it, ms(2500), lc.At) {
		t.Fatal("height change restarts the delay")
	}
	lc.SetKey("a", ms(2600))
	if lc.At != ms(2000) {
		t.Fatal("same ids moved At")
	}
	lc.SetKey("a\nb", ms(2700))
	if lc.At != ms(2700) {
		t.Fatal("new ids did not move At")
	}
	lc.Touch(ms(3000))
	if lc.At != ms(3000) {
		t.Fatal("touch")
	}
}

func TestMergeSeed(t *testing.T) {
	none := map[string]bool{}
	// a request decided while the snapshot was in flight stays gone
	if got := MergeSeed(nil, []hubapi.Request{req("a")}, map[string]bool{"a": true}, none, ms(0)); len(got) != 0 {
		t.Fatal("ghost:", ids(got))
	}
	// one that became pending after the snapshot is kept
	cur := []PendingItem{{Request: req("x"), ShownAt: ms(1)}}
	if got := MergeSeed(cur, nil, none, map[string]bool{"x": true}, ms(2)); !reflect.DeepEqual(ids(got), []string{"x"}) {
		t.Fatal("hidden:", ids(got))
	}
	// a current item the snapshot no longer has is dropped unless pending since
	cur = []PendingItem{{Request: req("stale")}, {Request: req("new")}}
	if got := MergeSeed(cur, nil, none, map[string]bool{"new": true}, ms(2)); !reflect.DeepEqual(ids(got), []string{"new"}) {
		t.Fatal(ids(got))
	}
	// union, keeping a known shownAt
	cur = []PendingItem{{Request: req("a"), ShownAt: ms(10)}}
	got := MergeSeed(cur, []hubapi.Request{req("a"), req("b")}, none, none, ms(999))
	if !reflect.DeepEqual(ids(got), []string{"a", "b"}) || got[0].ShownAt != ms(10) || got[1].ShownAt != ms(999) {
		t.Fatalf("%+v", got)
	}
}

func TestHighlightNonASCII(t *testing.T) {
	if got := HighlightNonASCII("rm -rf /tmp/а", ""); !reflect.DeepEqual(got, []Segment{{"rm -rf /tmp/", false}, {"а", true}}) {
		t.Fatal(got)
	}
	if got := HighlightNonASCII("ls", ""); !reflect.DeepEqual(got, []Segment{{"ls", false}}) {
		t.Fatal(got)
	}
	if got := HighlightNonASCII("", ""); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestNonASCIISummary(t *testing.T) {
	for in, want := range map[string]string{
		"ls -la":                               "",
		"curl gіthub.com":                      "1 non-ASCII character highlighted (U+0456)",
		"іі":                                   "2 non-ASCII characters highlighted (U+0456)",
		"\u00e0\u00e1\u00e2\u00e3\u00e4\u00e5": "6 non-ASCII characters highlighted (U+00E0, U+00E1, U+00E2, U+00E3, U+00E4, +1 more)",
	} {
		if got := NonASCIISummary(in, ""); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestStdin(t *testing.T) {
	if got := StdinMeta("a\r\nb\n"); got != "5 bytes, 2 lines" {
		t.Error(got)
	}
	if got := StdinMeta("é"); got != "2 bytes, 1 line" {
		t.Error(got)
	}
	if got := StdinMeta(""); got != "0 bytes, 0 lines" {
		t.Error(got)
	}
	if got := StdinView("a\tb\r\n"); !reflect.DeepEqual(got, []Segment{{"a\tb", false}, {"␍", true}, {"\n", false}}) {
		t.Error(got)
	}
}
