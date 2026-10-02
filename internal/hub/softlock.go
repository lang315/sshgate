package hub

import (
	"slices"
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
