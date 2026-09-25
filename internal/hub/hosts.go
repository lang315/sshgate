package hub

import (
	"bytes"
	"errors"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
)

// errNoVault: every write from the app needs a vault, the only thing that
// gives a key to encrypt with and to MAC under.
var errNoVault = errors.New("create a vault first")

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

// writeKey returns a copy of the master key for a store write.
func (h *Hub) writeKey() ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.deps.MasterKey != nil:
		return bytes.Clone(h.deps.MasterKey), nil
	case h.deps.File != nil && h.deps.File.KDF != nil:
		return nil, ErrLocked
	}
	return nil, errNoVault
}

// denyPending denies name's pending AI requests: each was for the server as
// it was when submitted.
func (h *Hub) denyPending(name string) {
	for _, r := range h.broker.Pending() {
		if r.Server == name {
			_ = h.broker.Decide(r.ID, broker.Decision{Outcome: broker.Denied, Reason: "server changed"})
		}
	}
}

// dialChanged: every field but AIVisible feeds the dial config or the name
// the connection is registered under.
func dialChanged(a, b config.Server) bool {
	a.AIVisible = b.AIVisible
	return a != b
}

// SaveServer creates (original == "") or updates a server. Its connection is
// closed only if something that feeds the dial changed, never for an
// aiVisible toggle; its pending AI requests are denied either way.
func (h *Hub) SaveServer(original string, in config.ServerInput) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	var before, after config.Server
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		var err error
		before, after, err = config.ApplyServer(f, original, in, key)
		return err
	})
	if err != nil {
		return err
	}
	_ = h.Reload()
	name := original
	if name == "" {
		name = in.Name
	}
	if dialChanged(before, after) {
		h.reg.Close(name)
	}
	h.denyPending(name)
	return nil
}

func (h *Hub) DeleteServer(name string) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		for i, s := range f.Servers {
			if s.Name == name {
				f.Servers = append(f.Servers[:i], f.Servers[i+1:]...)
				return nil
			}
		}
		return serverNotFound(name)
	})
	if err != nil {
		return err
	}
	_ = h.Reload()
	h.reg.Close(name)
	h.denyPending(name)
	return nil
}

// ForgetHostKey clears a server's pin and closes its connection, so the next
// open asks the user again. An unpinned server is a no-op success.
func (h *Hub) ForgetHostKey(name string) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == name {
				f.Servers[i].HostKey, f.Servers[i].HostKeyAlgo = "", ""
				return nil
			}
		}
		return serverNotFound(name)
	})
	if err != nil {
		return err
	}
	_ = h.Reload()
	h.reg.Close(name)
	return nil
}
