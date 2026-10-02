package hub

import "time"

const ProtocolVersion = 9

const defaultIdleLock = 15 * time.Minute

// touch records UI input; the idle auto-lock counts from the last one.
func (h *Hub) touch() {
	h.mu.Lock()
	h.lastActivity = time.Now()
	h.mu.Unlock()
}

// idleLoop sweeps expired auto-allow grants every tick, and locks the vault (softly, while a grant is live)
// after a quiet period with nothing pending or running (only while idle>0:
// idle<=0 disables auto-lock, but the sweep still runs, once a minute). It
// exits when h.done is closed (Close).
func (h *Hub) idleLoop(idle time.Duration) {
	tick := time.Minute
	if idle > 0 {
		tick = max(idle/15, time.Second)
		tick = min(tick, idle)
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-h.done:
			return
		case <-t.C:
			h.sweepGrants(time.Now())
			if idle > 0 {
				h.lockIfIdle(idle)
			}
		}
	}
}

// lockIfIdle locks the vault if nothing is pending, nothing approved is
// running, and no UI input arrived within idle. With a live auto-allow grant
// it locks softly: the UI door locks, the key and the grants stay (see
// softlock.go). No grant delays it, and auto runs never count as running.
// Pending() is read before h.mu to keep the lock order (never hold h.mu
// while calling into the broker); quiet and running are re-checked under
// h.mu together with the state change. A request submitted between the
// Pending() read and h.mu cannot be running yet (running++ happens only
// after approval), so the worst case is that it is approved against a locked
// vault and fails closed with ErrLocked.
func (h *Hub) lockIfIdle(idle time.Duration) {
	if len(h.broker.Pending()) > 0 {
		return
	}
	h.mu.Lock()
	switch {
	case h.running > 0, h.softLocked, h.deps.MasterKey == nil, time.Since(h.lastActivity) < idle:
		h.mu.Unlock()
	case len(h.grants) > 0:
		sink := h.enterSoftLocked(time.Now().Round(0))
		h.mu.Unlock()
		if sink != nil {
			sink("idle", true)
		}
	default:
		sink := h.zeroKeyLocked()
		h.mu.Unlock()
		if sink != nil {
			sink("idle", false)
		}
	}
}
