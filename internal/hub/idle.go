package hub

import "time"

const ProtocolVersion = 1

const defaultIdleLock = 15 * time.Minute

// touch records UI or AI activity; the idle auto-lock counts from the last one.
func (h *Hub) touch() {
	h.mu.Lock()
	h.lastActivity = time.Now()
	h.mu.Unlock()
}

// idleLoop locks the vault after a quiet period with nothing pending or
// running. It exits when h.done is closed (Close).
func (h *Hub) idleLoop(idle time.Duration) {
	tick := idle / 15
	if tick < time.Second {
		tick = time.Second
	}
	if idle < tick {
		tick = idle
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-h.done:
			return
		case now := <-t.C:
			h.mu.Lock()
			quiet := now.Sub(h.lastActivity) >= idle && h.running == 0
			h.mu.Unlock()
			if quiet && len(h.broker.Pending()) == 0 {
				h.lockWithReason("idle")
			}
		}
	}
}
