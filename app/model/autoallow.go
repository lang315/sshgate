package model

import (
	"fmt"
	"math"
	"strings"
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

// AutoMode is an Auto-allow choice other than Off: the hub's value, the
// editor's label, and the grant's length (0 for forever).
type AutoMode struct {
	Mode, Label string
	Dur         time.Duration
}

// AutoModes are the editor's choices after Off, in Electron's order.
var AutoModes = []AutoMode{
	{"15m", "15 min", 15 * time.Minute}, {"30m", "30 min", 30 * time.Minute}, {"60m", "60 min", time.Hour},
	{"2h", "2 h", 2 * time.Hour}, {"4h", "4 h", 4 * time.Hour}, {"forever", "Until turned off", 0},
}

// AutoModeOf finds mode among AutoModes; off and anything unknown are not.
func AutoModeOf(mode string) (AutoMode, bool) {
	for _, m := range AutoModes {
		if m.Mode == mode {
			return m, true
		}
	}
	return AutoMode{}, false
}

// KeepRan keeps the runs an unlock reply listed: the hub hands them to the
// first reply only, so a later empty one keeps the list.
func KeepRan(cur, next []hubapi.RanLocked) []hubapi.RanLocked {
	if len(next) > 0 {
		return next
	}
	return cur
}

// RanLockedText says what ran while the app was locked; "" when nothing did.
func RanLockedText(ran []hubapi.RanLocked) string {
	n := 0
	names := make([]string, len(ran))
	for i, r := range ran {
		n += r.Count
		names[i] = r.Server
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("While the app was locked the AI ran %d command%s on %s. See the Audit tab.", n, plural(n), strings.Join(names, ", "))
}

// EnableAllowed is the Auto-allow dialog's Enable: never on a refused host,
// and with typeName only once the host name is typed exactly; then
// AllowDelay after the dialog opened or its content last changed.
func EnableAllowed(typeName bool, typed, host, refused string, changedAt, now time.Time) bool {
	if refused != "" || typeName && typed != host {
		return false
	}
	return TrustEnabled(changedAt, now)
}

// DialogChangeKey is what restarts Enable's delay when it changes: the
// mode, the warnings, the sudo sentence, the root check's state (pending,
// failed or its answer) and an error line. The typed name is not in it.
func DialogChangeKey(mode string, rootNew, sudoNew, sudo bool, check *hubapi.AutoCheck, checkFailed bool, err string) string {
	state := "pending"
	switch {
	case checkFailed:
		state = "error"
	case check != nil:
		state = fmt.Sprintf("uid %d sudo %t", check.UID, check.PasswordlessSudo)
	}
	return fmt.Sprintf("%s|%t|%t|%t|%s|%q", mode, rootNew, sudoNew, sudo, state, err)
}

// TimedNote warns, under the editor's select, that saving ends a running
// timed grant; "" when none runs.
func TimedNote(s *hubapi.Server, now time.Time) string {
	if s == nil {
		return ""
	}
	ms := left(s.AutoAllow, now)
	if ms <= 0 {
		return ""
	}
	return fmt.Sprintf("On for %d more min — saving ends it; pick a duration to keep auto-allow on", int(math.Ceil(float64(ms)/float64(time.Minute))))
}

// Confirm is what Save must ask before it sends the draft's auto-allow.
type Confirm struct {
	Needed, TypeName, RootNew, SudoNew bool
}

// SaveConfirm: any mode but Off asks, unless the host already runs a live
// forever grant with the same opt-ins; a newly chosen forever needs the
// name typed; an opt-in newly ticked gets its warning.
func SaveConfirm(s *hubapi.Server, d HostDraft) Confirm {
	var a hubapi.AutoAllow
	var root, sudo bool
	if s != nil {
		root, sudo = s.AutoAllowRoot, s.AutoAllowSudo
		if s.AutoAllow != nil {
			a = *s.AutoAllow
		}
	}
	armed := d.AutoAllow == "forever" && a.Forever && !a.Paused && d.AutoAllowRoot == root && d.AutoAllowSudo == sudo
	return Confirm{
		Needed:   d.AutoAllow != "off" && !armed,
		TypeName: d.AutoAllow == "forever" && !a.Forever,
		RootNew:  d.AutoAllowRoot && !root,
		SudoNew:  d.AutoAllowSudo && !sudo,
	}
}

// DraftRefusal is why arming the draft would fail, shown before Save; the
// hub has the final say on the draft's other edits.
func DraftRefusal(s *hubapi.Server, d HostDraft) string {
	switch {
	case s == nil || s.HostKey == "":
		return "no pinned host key"
	case !d.AIVisible:
		return "not visible to AI"
	}
	switch s.AutoAllowRefused {
	case "root login", "has an su password", "has a sudo password":
		if !d.AutoAllowRoot {
			return s.AutoAllowRefused
		}
	}
	return ""
}
