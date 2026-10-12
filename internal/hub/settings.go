package hub

import (
	"fmt"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
)

// SetSoftLockLimit stores the soft-lock limit (24h, 3d, 7d or none) in the
// vault, under its MAC. It needs an unlocked vault, like every store write,
// so the limit cannot change during a soft lock. Setting the value already
// stored writes nothing. A failed reload after the write is returned; the
// write stands.
func (h *Hub) SetSoftLockLimit(limit string) error {
	stored, ok := softLockStored(limit)
	if !ok {
		return fmt.Errorf("unknown soft-lock limit %q", limit)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	key, err := h.writeKeyLocked()
	if err != nil {
		return err
	}
	defer clear(key)
	if h.deps.File.SoftLockLimit == stored {
		return nil
	}
	if err := config.Update(h.o.StorePath, key, func(f *config.File) error {
		f.SoftLockLimit = stored
		return nil
	}); err != nil {
		return err
	}
	h.auditConfig(broker.ConfigRecord{Action: "softLockLimit", Limit: limit})
	return h.reloadLocked()
}

// softLockLimitForUI is the limit in force as a UI-door value, only while
// unlocked: while hard-locked the file's MAC is unchecked.
func (h *Hub) softLockLimitForUI() (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.deps.MasterKey == nil || h.deps.File == nil {
		return "", false
	}
	return softLockWire(h.deps.File.SoftLockLimit), true
}
