package model

import (
	"fmt"
	"math"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

func left(a *hubapi.AutoAllow, now time.Time) time.Duration {
	if a == nil || a.Until == "" {
		return 0
	}
	u, err := time.Parse(time.RFC3339Nano, a.Until)
	if err != nil {
		return 0
	}
	return u.Sub(now)
}

func ChipLabel(a *hubapi.AutoAllow, now time.Time) string {
	if a == nil {
		return ""
	}
	if a.Forever {
		if a.Paused {
			return "Auto paused"
		}
		return "Auto ∞"
	}
	ms := left(a, now)
	if ms <= 0 {
		return ""
	}
	min := int(math.Ceil(float64(ms) / float64(time.Minute)))
	if min < 60 {
		return fmt.Sprintf("Auto %dm", min)
	}
	h, m := min/60, min%60
	if m != 0 {
		return fmt.Sprintf("Auto %dh %dm", h, m)
	}
	return fmt.Sprintf("Auto %dh", h)
}

func IsActive(a *hubapi.AutoAllow, now time.Time) bool {
	if a == nil {
		return false
	}
	if a.Forever {
		return !a.Paused
	}
	return left(a, now) > 0
}

func AutoHostNames(servers []hubapi.Server, now time.Time) []string {
	var out []string
	for _, s := range servers {
		if IsActive(s.AutoAllow, now) || (s.AutoAllow != nil && s.AutoAllow.Paused) {
			out = append(out, s.Name)
		}
	}
	return out
}

func PausedHosts(servers []hubapi.Server) []string {
	var out []string
	for _, s := range servers {
		if s.AutoAllow != nil && s.AutoAllow.Paused {
			out = append(out, s.Name)
		}
	}
	return out
}

// DropOnLock is the locked view: timed grants vanish, forever ones pause.
func DropOnLock(servers []hubapi.Server) []hubapi.Server {
	out := append([]hubapi.Server(nil), servers...)
	for i := range out {
		if out[i].AutoAllow != nil && out[i].AutoAllow.Forever {
			out[i].AutoAllow = &hubapi.AutoAllow{Forever: true, Paused: true}
		} else {
			out[i].AutoAllow = nil
		}
	}
	return out
}

func ApplyOff(servers []hubapi.Server, name string) []hubapi.Server {
	out := append([]hubapi.Server(nil), servers...)
	for i := range out {
		if out[i].Name == name {
			out[i].AutoAllow = nil
		}
	}
	return out
}

// LockHostsFrom drops hosts an autoAllow.off removed while the status call
// that listed them was in flight.
func LockHostsFrom(auto []string, dropped map[string]bool) []string {
	out := []string{}
	for _, h := range auto {
		if !dropped[h] {
			out = append(out, h)
		}
	}
	return out
}

func DropLockHost(hosts []string, server string) []string {
	out := []string{}
	for _, h := range hosts {
		if h != server {
			out = append(out, h)
		}
	}
	return out
}

const FeedCap = 50

func PushFeed(feed []hubapi.AutoRan, r hubapi.AutoRan) []hubapi.AutoRan {
	out := append([]hubapi.AutoRan{r}, feed...)
	if len(out) > FeedCap {
		out = out[:FeedCap]
	}
	return out
}

func CommandLabel(r hubapi.AutoRan) string {
	if r.Truncated > 0 {
		return fmt.Sprintf("%s … (truncated %d bytes)", r.Command, r.Truncated)
	}
	return r.Command
}
