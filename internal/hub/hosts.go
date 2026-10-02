package hub

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshx"
)

// errNoVault: every write from the app needs a vault, the only thing that
// gives a key to encrypt with and to MAC under.
var errNoVault = errors.New("create a vault first")

// newKDF is config.NewKDF; a test seam that counts key derivations.
var newKDF = config.NewKDF

// CreateVault gives a store with no master password (or no file yet) one.
// Kept servers lose aiVisible, autoAllow (and its two opt-ins) and their pins: a KDF-less file was never
// MAC'd, so its flags and pins are unauthenticated, and a pin must come
// through the fingerprint prompt. The key is derived once, here, and installed in the
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
	// The in-memory vault counts too: its file may have gone missing.
	if f, err := config.Load(h.o.StorePath); h.hasVault() || err == nil && f.KDF != nil {
		return errors.New("a vault already exists")
	}
	k, mk, err := newKDF(pw)
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
			s.AutoAllow = false
			s.AutoAllowRoot, s.AutoAllowSudo = false, false
			s.HostKey, s.HostKeyAlgo = "", ""
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
	h.lockGen.Add(1)
	h.unlocked.Store(true)
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
		{"autoAllow", a.AutoAllow, b.AutoAllow}, {"autoAllowRoot", a.AutoAllowRoot, b.AutoAllowRoot},
		{"autoAllowSudo", a.AutoAllowSudo, b.AutoAllowSudo}, {"hostKey", a.HostKey, b.HostKey},
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

// writeKeyLocked is writeKey with h.mu held: SaveServer, DeleteServer and
// ForgetHostKey call it directly since their own write, reload and grant-end
// all run in one h.mu section (writeKey's own Lock would deadlock there).
func (h *Hub) writeKeyLocked() ([]byte, error) {
	switch {
	case h.deps.MasterKey != nil:
		return bytes.Clone(h.deps.MasterKey), nil
	case h.deps.File != nil && h.deps.File.KDF != nil:
		return nil, ErrLocked
	}
	return nil, errNoVault
}

// writeKey returns a copy of the master key for a store write.
func (h *Hub) writeKey() ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.writeKeyLocked()
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

// dialChanged: every field but AIVisible, AutoAllow, the two opt-ins and
// Tunnels feeds the dial config or the name the connection is registered
// under.
func dialChanged(a, b config.Server) bool {
	a.AIVisible, a.AutoAllow, a.AutoAllowRoot, a.AutoAllowSudo, a.Tunnels = b.AIVisible, b.AutoAllow, b.AutoAllowRoot, b.AutoAllowSudo, nil
	b.Tunnels = nil
	return !reflect.DeepEqual(a, b)
}

// errSavedButPrefix marks a SaveServerWithAutoAllow error whose write stood
// but whose requested auto-allow mode could not be armed: the app shows
// "Saved, but auto-allow was not turned on: <reason>".
const errSavedButPrefix = "saved, but auto-allow was not turned on: "

// SaveServer creates (original == "") or updates a server, with auto-allow
// left off (Amendment 2026-09-29: servers.save's default when no mode is
// given).
func (h *Hub) SaveServer(original string, in config.ServerInput) error {
	return h.SaveServerWithAutoAllow(original, in, "off")
}

// SaveServerWithAutoAllow is SaveServer plus an optional auto-allow mode
// (Amendment 2026-09-29), off or "" behaving exactly like SaveServer did
// before this. Its connection is closed only if something that feeds the
// dial changed, never for an aiVisible toggle; its pending AI requests are
// denied either way.
//
// The write, the reload that follows, ending the server's auto-allow grant,
// and — for a mode other than off — arming a new one all run in one h.mu
// section — the CreateVault/SetAutoAllow pattern (lock order h.mu →
// config.Update is safe: no config.Update fn takes h.mu). That serializes
// this call with servers.setAutoAllow, so any grant present when this
// section runs provably predates this save: it is ended unconditionally, no
// revision bookkeeping needed. A rename ends the grant under both the old
// name and, if different, the new one, so it can't leave one behind or
// inherit a leftover one; arming (armLocked) runs under the new name.
func (h *Hub) SaveServerWithAutoAllow(original string, in config.ServerInput, mode string) error {
	h.mu.Lock()
	key, err := h.writeKeyLocked()
	if err != nil {
		h.mu.Unlock()
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
		h.mu.Unlock()
		return err
	}
	name := original
	if name == "" {
		name = in.Name
	}
	reloadErr := h.reloadLocked()
	endedOld := h.endGrantLocked(name) || before.AutoAllow
	if endedOld {
		h.auditGrantEnded(name, "saved")
	}
	// A rename also ends a grant sitting under the target name (a stray
	// left by an earlier bug, or of a since-deleted server that reused the
	// name): recorded and notified under that name, not the old one, since
	// it isn't the flag or grant this save's own server just had.
	var endedNew bool
	if after.Name != name {
		endedNew = h.endGrantLocked(after.Name)
		if endedNew {
			h.auditGrantEnded(after.Name, "saved")
		}
	}
	var armNotes []endedNote
	var armErr error
	if mode != "" && mode != "off" && reloadErr == nil {
		armNotes, armErr = h.armLocked(after.Name, mode)
	}
	h.mu.Unlock()

	// Notified before the dialChanged/denyPending cleanup below, not after:
	// denyPending's broker.Decide fires a "decided" event for the pending
	// request it just denied, which the UI door forwards to the app as its
	// own outgoing notification (and o.OnEvent, a test/embedding hook) —
	// production delivery never calls back into the hub, so this ordering
	// is about what the app sees when, not a reentrancy race. Moving the
	// notify here means the app learns the grant ended before it learns the
	// request it was covering got denied, and before this save's own RPC
	// reply lands. (A test event sink can reenter SetAutoAllow synchronously
	// from its own "decided" callback, to reproduce specific interleavings
	// deterministically; that is a test technique, not something production
	// event delivery does.)
	if endedOld {
		h.notifyGrantEnded(name, "saved")
	}
	if endedNew {
		h.notifyGrantEnded(after.Name, "saved")
	}
	for _, n := range armNotes {
		h.notifyGrantEnded(n.name, n.reason)
	}

	if dialChanged(before, after) {
		h.endServerTunnels(name, "server changed")
		h.files.endServer(name, "server changed")
		h.reg.Close(name)
	}
	h.denyPending(name)
	h.auditConfig(broker.ConfigRecord{Action: "save", Server: after.Name, Changed: changes(before, after, in)})

	switch {
	case reloadErr != nil:
		if mode != "" && mode != "off" {
			return fmt.Errorf("%s%w", errSavedButPrefix, reloadErr)
		}
		return reloadErr
	case armErr != nil:
		return fmt.Errorf("%s%w", errSavedButPrefix, armErr)
	}
	return nil
}

// DeleteServer removes name, in the same one-h.mu-section pattern as
// SaveServer.
func (h *Hub) DeleteServer(name string) error {
	h.mu.Lock()
	key, err := h.writeKeyLocked()
	if err != nil {
		h.mu.Unlock()
		return err
	}
	defer clear(key)
	var hadFlag bool
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		for i, s := range f.Servers {
			if s.Name == name {
				hadFlag = s.AutoAllow
				f.Servers = append(f.Servers[:i], f.Servers[i+1:]...)
				return nil
			}
		}
		return serverNotFound(name)
	})
	if err != nil {
		h.mu.Unlock()
		return err
	}
	reloadErr := h.reloadLocked()
	had := h.endGrantLocked(name)
	ended := had || hadFlag
	if ended {
		h.auditGrantEnded(name, "deleted")
	}
	h.mu.Unlock()

	if ended {
		h.notifyGrantEnded(name, "deleted")
	}
	h.endServerTunnels(name, "server changed")
	h.files.endServer(name, "server changed")
	h.reg.Close(name)
	h.denyPending(name)
	h.auditConfig(broker.ConfigRecord{Action: "delete", Server: name})
	return reloadErr // the write itself succeeded
}

// ForgetHostKey clears a server's pin and closes its connection, so the next
// open asks the user again. An unpinned server is a no-op success. Same
// one-h.mu-section pattern as SaveServer.
func (h *Hub) ForgetHostKey(name string) error {
	h.mu.Lock()
	key, err := h.writeKeyLocked()
	if err != nil {
		h.mu.Unlock()
		return err
	}
	defer clear(key)
	var old string
	var hadFlag bool
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == name {
				old = f.Servers[i].HostKey
				f.Servers[i].HostKey, f.Servers[i].HostKeyAlgo = "", ""
				hadFlag = f.Servers[i].AutoAllow
				f.Servers[i].AutoAllow = false
				return nil
			}
		}
		return serverNotFound(name)
	})
	if err != nil {
		h.mu.Unlock()
		return err
	}
	reloadErr := h.reloadLocked()
	had := h.endGrantLocked(name)
	ended := had || hadFlag
	if ended {
		h.auditGrantEnded(name, "server changed")
	}
	h.mu.Unlock()

	if ended {
		h.notifyGrantEnded(name, "server changed")
	}
	h.endServerTunnels(name, "server changed")
	h.files.endServer(name, "server changed")
	h.reg.Close(name)
	h.auditConfig(broker.ConfigRecord{Action: "forgetHostKey", Server: name, OldFingerprint: old})
	return reloadErr // the write itself succeeded
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
	// Unlike servers.*, a failed reload does not fail the call: the pin is
	// written and the key verified, so the open proceeds. Reload remembers
	// the failure as storeError.
	_ = h.Reload()
	h.auditConfig(broker.ConfigRecord{Action: "trust", Server: name, Host: dc.Host, Port: dc.Port, Fingerprint: dc.HostKey, Algo: dc.HostKeyAlgo})
	return nil
}
