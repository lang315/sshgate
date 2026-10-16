package main

import (
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo"
)

// The rule of desktop/e2e/background.spec.ts, bar the real window and Dock,
// which only a Mac and a person can check (the plan's hand checks).
func TestHidesOnClose(t *testing.T) {
	for _, tc := range []struct {
		goos     string
		quitting bool
		want     bool
	}{
		{"darwin", false, true}, {"darwin", true, false},
		{"linux", false, false}, {"windows", false, false}, {"linux", true, false},
	} {
		if got := hidesOnClose(tc.goos, tc.quitting); got != tc.want {
			t.Errorf("hidesOnClose(%s, %v) = %v", tc.goos, tc.quitting, got)
		}
	}
}

func TestCloseHookHidesInsteadOfClosingUnlessQuitting(t *testing.T) {
	var quitting atomic.Bool
	hid := 0
	hook := closeHook("darwin", &quitting, func() { hid++ })

	e := &mygo.CloseEvent{}
	hook(e)
	if !e.DefaultPrevented() || hid != 1 {
		t.Fatalf("a close on macOS: prevented %v, hid %d", e.DefaultPrevented(), hid)
	}

	quitting.Store(true) // Quit closes every window: it must get through
	e = &mygo.CloseEvent{}
	hook(e)
	if e.DefaultPrevented() || hid != 1 {
		t.Fatalf("a close while quitting: prevented %v, hid %d", e.DefaultPrevented(), hid)
	}

	quitting.Store(false)
	e = &mygo.CloseEvent{}
	closeHook("linux", &quitting, func() { hid++ })(e)
	if e.DefaultPrevented() || hid != 1 {
		t.Fatalf("a close on Linux: prevented %v, hid %d", e.DefaultPrevented(), hid)
	}
}
