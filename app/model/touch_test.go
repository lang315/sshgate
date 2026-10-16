package model

import "testing"

func TestTouchChange(t *testing.T) {
	for _, tc := range []struct {
		enabled, on bool
		want        string
	}{{false, false, "same"}, {true, true, "same"}, {false, true, "on"}, {true, false, "off"}} {
		if got := TouchChange(tc.enabled, tc.on); got != tc.want {
			t.Errorf("TouchChange(%v, %v) = %q, want %q", tc.enabled, tc.on, got, tc.want)
		}
	}
}

// AutoTouch asks at start with the focus and on a focus while locked, once
// per locked episode, never at the moment of locking (test/shell.test.ts).
func TestAutoTouch(t *testing.T) {
	on := &TouchStatus{Supported: true, Available: true, Enabled: true}
	for _, tc := range []struct {
		name                      string
		trigger                   string
		locked                    bool
		touch                     *TouchStatus
		asked, everReady, focused bool
		want                      bool
	}{
		{"start with the focus", "start", true, on, false, false, true, true},
		{"a focus while locked", "focus", true, on, false, false, false, true},
		{"a focus after the ready screen was shown", "focus", true, on, false, true, false, true},
		{"never at start once the ready screen was shown", "start", true, on, false, true, true, false},
		{"not at start without the focus", "start", true, on, false, false, false, false},
		{"not when asked already", "focus", true, on, true, false, true, false},
		{"not when unlocked", "focus", false, on, false, false, true, false},
		{"not when off", "focus", true, &TouchStatus{Supported: true, Available: true}, false, false, true, false},
		{"not when unavailable", "focus", true, &TouchStatus{Supported: true, Enabled: true}, false, false, true, false},
		{"not before the status is known", "focus", true, nil, false, false, true, false},
		{"no trigger asks at the moment of locking", "lock", true, on, false, true, true, false},
	} {
		if got := AutoTouch(tc.trigger, tc.locked, tc.touch, tc.asked, tc.everReady, tc.focused); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}
