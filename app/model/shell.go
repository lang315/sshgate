package model

import (
	"strings"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

// HubState is the hub process as the app sees it.
type HubState struct {
	Kind    string // starting, running, restarting, failed
	Attempt int
	In      time.Duration
	Message string
	Stderr  string
}

// HubStateFrom maps the supervisor's state; a failed message carries the
// hub's last stderr lines after its first line.
func HubStateFrom(kind string, attempt int, in time.Duration, message string) HubState {
	msg, stderr, _ := strings.Cut(message, "\n")
	return HubState{Kind: kind, Attempt: attempt, In: in, Message: msg, Stderr: stderr}
}

type Screen string

const (
	ScreenHub         Screen = "hub"
	ScreenCreateVault Screen = "create-vault"
	ScreenLocked      Screen = "locked"
	ScreenReady       Screen = "ready"
)

func ScreenFor(h HubState, s *hubapi.Status) Screen {
	switch {
	case h.Kind != "running", s == nil:
		return ScreenHub
	case !s.HasVault:
		return ScreenCreateVault
	case s.Locked:
		return ScreenLocked
	}
	return ScreenReady
}

// LockKind: idle reads as inactivity; grantsEnded and softLockLimit only
// arrive during a soft lock and keep the reason stored for it.
func LockKind(reason, prev string) string {
	switch reason {
	case "idle":
		return "idle"
	case "grantsEnded", "softLockLimit":
		if prev != "" {
			return prev
		}
	}
	return "manual"
}

func LockTitle(liveAuto int) string {
	if liveAuto > 0 {
		return "Auto-allow keeps running while locked"
	}
	return ""
}

// AIButtonWaiting: amber only while the column is collapsed with requests waiting.
func AIButtonWaiting(open bool, n int) bool { return !open && n > 0 }
