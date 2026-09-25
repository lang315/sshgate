package hub

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx"
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
	var kept []string
	err = config.Update(h.o.StorePath, mk, func(f *config.File) error {
		for i := range f.Servers {
			s := &f.Servers[i]
			if s.EncPassword != "" || s.EncSuPassword != "" || s.EncSudoPassword != "" || s.EncKeyPassphrase != "" {
				return errors.New("the store has encrypted fields but no master password; it is corrupt or was tampered with")
			}
			s.AIVisible = false
			kept = append(kept, s.Name)
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
	h.auditConfig(broker.ConfigRecord{Action: "vaultCreate", KeptServers: kept})
	return nil
}

// auditConfig never takes h.mu, so CreateVault may call it holding h.mu.
func (h *Hub) auditConfig(r broker.ConfigRecord) {
	if h.audit != nil {
		r.Time = time.Now()
		_ = h.audit.WriteConfig(r)
	}
}

// changes lists what a save changed: before→after for plain fields and the
// pin, and only the name of a secret that was re-supplied or dropped.
func changes(a, b config.Server, in config.ServerInput) []string {
	var out []string
	for _, f := range []struct {
		name string
		x, y any
	}{
		{"name", a.Name, b.Name}, {"host", a.Host, b.Host}, {"port", a.Port, b.Port}, {"user", a.User, b.User},
		{"auth", a.Auth, b.Auth}, {"keyPath", a.KeyPath, b.KeyPath}, {"aiVisible", a.AIVisible, b.AIVisible},
		{"hostKey", a.HostKey, b.HostKey},
	} {
		if f.x != f.y {
			out = append(out, fmt.Sprintf("%s: %v → %v", f.name, f.x, f.y))
		}
	}
	for _, s := range []struct {
		name string
		in   *string
		x, y string
	}{
		{"password", in.Password, a.EncPassword, b.EncPassword},
		{"suPassword", in.SuPassword, a.EncSuPassword, b.EncSuPassword},
		{"sudoPassword", in.SudoPassword, a.EncSudoPassword, b.EncSudoPassword},
		{"keyPassphrase", in.KeyPassphrase, a.EncKeyPassphrase, b.EncKeyPassphrase},
	} {
		if s.in != nil || (s.x != "" && s.y == "") {
			out = append(out, s.name)
		}
	}
	return out
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
	h.auditConfig(broker.ConfigRecord{Action: "save", Server: after.Name, Changed: changes(before, after, in)})
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
	h.auditConfig(broker.ConfigRecord{Action: "delete", Server: name})
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
	var old string
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == name {
				old = f.Servers[i].HostKey
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
	h.auditConfig(broker.ConfigRecord{Action: "forgetHostKey", Server: name, OldFingerprint: old})
	return nil
}

// recordHostKey is a test seam.
var recordHostKey = config.RecordHostKey

// recordTrust pins the key a trusted open just verified. The write is
// skipped, and the open fails, if the server got a pin or moved to another
// host or port while the dial ran.
func (h *Hub) recordTrust(name string, dc sshx.DialConfig) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	if err := recordHostKey(h.o.StorePath, name, dc.Host, dc.Port, dc.HostKey, dc.HostKeyAlgo, key); err != nil {
		return err
	}
	_ = h.Reload()
	h.auditConfig(broker.ConfigRecord{Action: "trust", Server: name, Host: dc.Host, Port: dc.Port, Fingerprint: dc.HostKey, Algo: dc.HostKeyAlgo})
	return nil
}
