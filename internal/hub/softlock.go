package hub

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/sshx"
)

// Soft lock (spec 2026-10-02-soft-lock-design.md): when the idle lock fires
// while a grant is live, the UI door locks but the master key is kept for auto
// runs on granted hosts. The key moves out of deps.MasterKey into h.autoKey,
// which only the auto path (resolveAutoLocked) and the vault MAC check
// (vaultKeyLocked) read, so every other reader sees a locked vault with no
// check of its own. Invariant, whenever h.mu is released: autoKey != nil
// implies deps.MasterKey == nil and at least one grant is live.

// grantNamesLocked lists the hosts with a live grant, sorted; h.mu is held.
func (h *Hub) grantNamesLocked() []string {
	return slices.Sorted(maps.Keys(h.grants))
}

// lockNote tells the lock sink about one lock-state change; the generation it
// carries is the one that change made. Call it outside h.mu.
type lockNote func(reason string)

// lockNoteLocked is the note for the state change just made, with h.mu held:
// nil when no lock sink is installed.
func (h *Hub) lockNoteLocked() lockNote {
	sink, gen := h.lockSink, h.lockGen.Load()
	if sink == nil {
		return nil
	}
	return func(reason string) { sink(reason, gen) }
}

// enterSoftLocked locks the UI door and moves the key to autoKey; h.mu is held
// and at least one grant is live. The audit record is written after h.unlocked
// is cleared, so it is not pushed to the UI. It returns the lock note to send
// outside h.mu.
func (h *Hub) enterSoftLocked(now time.Time) lockNote {
	h.autoKey, h.deps.MasterKey, h.softLockedAt = h.deps.MasterKey, nil, now
	h.lockGen.Add(1)
	h.unlocked.Store(false)
	h.auditConfig(broker.ConfigRecord{Action: "softLock", Servers: h.grantNamesLocked()})
	return h.lockNoteLocked()
}

// resolveAutoLocked resolves name for the auto path, with h.mu held. Under
// soft lock the key is in h.autoKey, which nothing else reads, so the resolve
// runs on a copy of deps that carries it.
func (h *Hub) resolveAutoLocked(name string) (sshx.DialConfig, error) {
	if h.autoKey == nil {
		return h.deps.Resolve(name)
	}
	d := *h.deps
	d.MasterKey = h.autoKey
	return d.Resolve(name)
}

// vaultKeyLocked is the key the vault file is checked against: the master
// key, or under soft lock the one kept for the auto path. h.mu is held.
func (h *Hub) vaultKeyLocked() []byte {
	if h.deps.MasterKey != nil {
		return h.deps.MasterKey
	}
	return h.autoKey
}

// hardenLocked runs after every removal from h.grants, with h.mu held: the
// key never outlives the last grant. The sink is called on its own goroutine
// because the caller still holds h.mu; it only writes to the UI door. Its
// delivery order against other lock notes is unspecified, so the note carries
// the generation of this change and the door drops it once the hub has moved on.
func (h *Hub) hardenLocked() {
	if h.autoKey == nil || len(h.grants) > 0 {
		return
	}
	if note := h.zeroKeyLocked(); note != nil {
		go note("grantsEnded")
	}
}

// lockStatus is status's lock state, read in one section so the two parts
// cannot disagree: autoHosts is set only while soft-locked.
func (h *Hub) lockStatus() (locked bool, autoHosts []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.autoKey != nil {
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
	dc, err := h.resolveAutoLocked(name)
	return err != nil || snapOf(dc) != g.snap
}

// aiLockedLocked is the grant rule, with h.mu held and a vault present: the
// server is locked to the AI when both keys are absent, or under soft lock
// when it has no live grant or the soft lock has reached its ceiling
// (otherwise enforced only on the idle tick, so an exec right after a wake
// from sleep could run first).
func (h *Hub) aiLockedLocked(name string) bool {
	if h.deps.MasterKey != nil {
		return false
	}
	if h.autoKey == nil {
		return true
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
