package model

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// Ports of desktop/test/settings.test.ts (Touch ID rows: slice 7).

func TestLimitChange(t *testing.T) {
	for _, tc := range [][3]string{
		{"24h", "24h", "same"}, {"24h", "3d", "higher"}, {"3d", "7d", "higher"}, {"7d", "3d", "lower"},
		{"none", "24h", "lower"}, {"24h", "none", "none"}, {"none", "none", "same"},
	} {
		if got := LimitChange(tc[0], tc[1]); got != tc[2] {
			t.Errorf("LimitChange(%s, %s) = %s, want %s", tc[0], tc[1], got, tc[2])
		}
	}
}

func TestLimitCopy(t *testing.T) {
	want := []Limit{{"24h", "24 hours (default)"}, {"3d", "3 days"}, {"7d", "7 days"}, {"none", "No limit"}}
	if !slices.Equal(Limits, want) {
		t.Fatalf("Limits %v", Limits)
	}
	for _, l := range []string{"24h", "3d", "7d"} {
		if got := LimitHelp(l); got != "Then the app locks fully and auto-allow stops. Forever grants wait for Resume." {
			t.Errorf("LimitHelp(%s) = %q", l, got)
		}
	}
	if got := LimitHelp("none"); got != "Auto-allow stops only with Stop auto-allow and lock, a restart or a crash." {
		t.Errorf("LimitHelp(none) = %q", got)
	}
	if got := LimitNotice("7d"); got != "While the app is locked, the AI keeps running on hosts with auto-allow for up to 7 days. Any program running as your user can use that access too." {
		t.Errorf("LimitNotice(7d) = %q", got)
	}
	if got := LimitNotice("none"); got != "While the app is locked, the AI keeps running on hosts with auto-allow for as long as the app runs. Any program running as your user can use that access too." {
		t.Errorf("LimitNotice(none) = %q", got)
	}
}

// SaveAllowed takes the later of open and change as changedAt (ListChanges.At).
func TestSaveAllowed(t *testing.T) {
	at := func(ms int) time.Time { return time.UnixMilli(int64(ms)) }
	for _, tc := range []struct {
		change, touch, password string
		busy                    bool
		changed, now            int
		want                    bool
	}{
		{"higher", "same", "", false, 1000, 1499, false}, {"higher", "same", "", false, 1000, 1500, true},
		{"higher", "same", "", false, 2000, 2499, false}, {"higher", "same", "", false, 2000, 2500, true},
		{"same", "same", "", false, 1000, 9999, false}, {"higher", "same", "", true, 1000, 9999, false}, {"lower", "same", "", false, 1000, 9999, true},
		// Touch ID alone enables Save; turning it on also needs the master password.
		{"same", "on", "", false, 1000, 9999, false}, {"same", "on", "pw", false, 1000, 1499, false},
		{"same", "on", "pw", false, 1000, 1500, true}, {"same", "off", "", false, 1000, 1500, true},
		{"higher", "on", "", false, 1000, 9999, false}, {"higher", "off", "", true, 1000, 9999, false},
	} {
		if got := SaveAllowed(tc.change, tc.touch, tc.password, tc.busy, at(tc.changed), at(tc.now)); got != tc.want {
			t.Errorf("%+v: got %v", tc, got)
		}
	}
}

func TestSettingsChangeKey(t *testing.T) {
	k := SettingsChangeKey("none", []string{"box"}, "", false)
	for _, other := range []string{
		SettingsChangeKey("7d", []string{"box"}, "", false),
		SettingsChangeKey("none", []string{"box", "web"}, "", false),
		SettingsChangeKey("none", []string{"box"}, "Vault is locked", false),
		SettingsChangeKey("none", []string{"box"}, "", true),
	} {
		if other == k {
			t.Errorf("%q did not change the key", other)
		}
	}
	if SettingsChangeKey("none", []string{"box"}, "", false) != k {
		t.Error("the same inputs changed the key")
	}
}

func TestLimitText(t *testing.T) {
	day := LimitText("24h")
	for _, s := range []string{"the AI keeps running on this host for up to 24 hours.", "After a restart, the 24-hour limit, or Stop auto-allow and lock, it waits for you to click Resume."} {
		if !strings.Contains(day, s) {
			t.Errorf("24h: missing %q in %q", s, day)
		}
	}
	if LimitText("") != day {
		t.Error("an absent limit is not the 24-hour copy")
	}
	for l, s := range map[string]string{"3d": "for up to 3 days.", "7d": "the 7-day limit"} {
		if !strings.Contains(LimitText(l), s) {
			t.Errorf("%s: missing %q", l, s)
		}
	}
	if !strings.Contains(LimitText("3d"), "the 3-day limit") {
		t.Error("3d: missing the 3-day limit")
	}
	none := LimitText("none")
	for _, s := range []string{"the AI keeps running on this host while the app is locked, with no time limit.", "After a restart or Stop auto-allow and lock, it waits for you to click Resume."} {
		if !strings.Contains(none, s) {
			t.Errorf("none: missing %q in %q", s, none)
		}
	}
	for _, s := range []string{"24", "hour", "day"} {
		if strings.Contains(none, s) {
			t.Errorf("none mentions %q: %q", s, none)
		}
	}
}
