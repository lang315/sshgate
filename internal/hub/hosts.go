package hub

import (
	"errors"
	"time"

	"github.com/lang315/ssh-mcp/internal/config"
)

// CreateVault gives a store with no master password (or no file yet) one.
// Kept servers lose aiVisible: a KDF-less file was never MAC'd, so its flags
// are unauthenticated. The key is derived once, here, and installed in the
// same h.mu section that saves and reloads, so nothing sees a vault that
// exists but is locked. If that reload fails, the key is dropped and the hub
// stays locked; the new password unlocks the vault. Lock order
// h.mu → config.Update is safe: no path takes h.mu from inside an Update.
func (h *Hub) CreateVault(pw string) error {
	if len(pw) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	// A fresh load, not deps.File. It must come first: Update checks the key
	// before fn runs, so an existing vault would only fail as "wrong master
	// key" (and a race still fails that way, closed).
	if f, err := config.Load(h.o.StorePath); err == nil && f.KDF != nil {
		return errors.New("a vault already exists")
	}
	k, mk, err := config.NewKDF(pw)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	err = config.Update(h.o.StorePath, mk, func(f *config.File) error {
		for i := range f.Servers {
			s := &f.Servers[i]
			if s.EncPassword != "" || s.EncSuPassword != "" || s.EncSudoPassword != "" || s.EncKeyPassphrase != "" {
				return errors.New("the store has encrypted fields but no master password; it is corrupt or was tampered with")
			}
			s.AIVisible = false
		}
		f.KDF = &k
		return nil
	})
	if err == nil {
		err = h.reloadLocked()
	}
	if err != nil {
		clear(mk)
		return err
	}
	clear(h.deps.MasterKey)
	h.deps.MasterKey = mk
	h.lastActivity = time.Now()
	return nil
}
