package hub

import (
	"slices"
	"strings"
	"time"

	"github.com/lang315/sshgate/internal/broker"
)

// Soft lock (spec 2026-10-02-soft-lock-design.md): when the idle lock fires
// while a grant is live, the UI door locks but the master key stays, so auto
// runs on granted hosts continue. Invariant, whenever h.mu is released:
// softLocked implies the key is present and at least one grant is live.

// grantNamesLocked lists the hosts with a live grant, sorted; h.mu is held.
func (h *Hub) grantNamesLocked() []string {
	names := make([]string, 0, len(h.grants))
	for n := range h.grants {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// enterSoftLocked locks the UI door and keeps the key; h.mu is held and at
// least one grant is live. The audit record is written after h.unlocked is
// cleared, so it is not pushed to the UI. It returns the lock sink to call
// outside h.mu.
func (h *Hub) enterSoftLocked(now time.Time) func(string, bool) {
	h.softLocked, h.softLockedAt = true, now
	h.unlocked.Store(false)
	h.auditConfig(broker.ConfigRecord{Action: "softLock", Servers: h.grantNamesLocked()})
	return h.lockSink
}

// hardenLocked runs after every removal from h.grants, with h.mu held: the
// key never outlives the last grant. The sink is called on its own goroutine
// because the caller still holds h.mu; it only writes to the UI door.
func (h *Hub) hardenLocked() {
	if !h.softLocked || len(h.grants) > 0 {
		return
	}
	if sink := h.zeroKeyLocked(); sink != nil {
		go sink("grantsEnded", false)
	}
}

// lockStatus is status's lock state, read in one section so the two parts
// cannot disagree: autoHosts is set only while soft-locked.
func (h *Hub) lockStatus() (locked bool, autoHosts []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.softLocked {
		autoHosts = h.grantNamesLocked()
	}
	return h.lockedLocked(), autoHosts
}

// maxSoftLock bounds how long a key and a grant outlive the human's last
// input. Before soft lock that bound was the idle period. A constant: no
// flag, no setting.
const maxSoftLock = 24 * time.Hour

// grantStaleLocked reports, with h.mu held, that g no longer matches name's
// server: hidden, removed, unpinned, refused, or changed since the grant's
// snapshot. sweepGrants uses it under soft lock; autoStart makes the same
// checks on every run, but only for a server that still passes the first
// resolve.
func (h *Hub) grantStaleLocked(name string, g *grant) bool {
	if h.noVaultLocked() {
		return true
	}
	s, ok := h.deps.File.FindServer(name)
	if !ok || autoRefusal(s) != "" || g.until.IsZero() && !s.AutoAllow {
		return true
	}
	dc, err := h.resolveLocked(name)
	return err != nil || snapOf(dc) != g.snap
}

// aiLockedLocked is the grant rule, with h.mu held and a vault present: the
// server is locked to the AI when the key is absent, or under soft lock when
// it has no live grant or the soft lock has reached its ceiling (otherwise
// enforced only on the idle tick, so an exec right after a wake from sleep
// could run first).
func (h *Hub) aiLockedLocked(name string) bool {
	if h.deps.MasterKey == nil {
		return true
	}
	if !h.softLocked {
		return false
	}
	return h.grants[name] == nil || time.Now().Round(0).Sub(h.softLockedAt) >= maxSoftLock
}

// checkGrantLocked is checkServerLocked under the grant rule. It has exactly
// two callers, resolveForAuto and autoStart; every other path, the
// post-approval resolve included, stays on checkLocked.
func (h *Hub) checkGrantLocked(name string) error {
	return h.checkServerLocked(name, h.aiLockedLocked(name))
}

// RanLocked is how many auto runs finished on a server while the UI was locked.
type RanLocked struct {
	Server string `json:"server"`
	Count  int    `json:"count"`
}

// TakeRanLocked returns and clears those counts, sorted by server. The unlock
// request calls it, so the human sees what ran while they were away.
func (h *Hub) TakeRanLocked() []RanLocked {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]RanLocked, 0, len(h.ranLocked))
	for s, n := range h.ranLocked {
		out = append(out, RanLocked{s, n})
	}
	slices.SortFunc(out, func(a, b RanLocked) int { return strings.Compare(a.Server, b.Server) })
	clear(h.ranLocked)
	return out
}
