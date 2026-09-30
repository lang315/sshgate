package hub

import "time"

const ProtocolVersion = 7

const defaultIdleLock = 15 * time.Minute

// touch records UI input; the idle auto-lock counts from the last one.
func (h *Hub) touch() {
	h.mu.Lock()
	h.lastActivity = time.Now()
	h.mu.Unlock()
}

// idleLoop sweeps expired auto-allow grants every tick, and locks the vault
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

// lockIfIdle locks the vault if nothing is pending, nothing is running, no
// timed auto-allow grant is before its deadline (auto runs themselves never
// count), and no UI input arrived within idle. Pending() is read before h.mu
// to keep the lock order (never hold h.mu while calling into the broker);
// quiet and running are re-checked under h.mu together with zeroing the key.
// A request submitted between the Pending() read and h.mu cannot be running
// yet (running++ happens only after approval), so the worst case is that it
// is approved against a locked vault and fails closed with ErrLocked.
func (h *Hub) lockIfIdle(idle time.Duration) {
	if len(h.broker.Pending()) > 0 {
		return
	}
	h.mu.Lock()
	if time.Since(h.lastActivity) < idle || h.running > 0 || h.timedGrantLocked(time.Now()) {
		h.mu.Unlock()
		return
	}
	sink := h.zeroKeyLocked()
	ended := h.endAllGrantsLocked()
	h.mu.Unlock()
	if sink != nil {
		sink("idle")
	}
	for _, n := range ended {
		h.grantEnded(n, "locked")
	}
}
