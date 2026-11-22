package hub

import (
	"errors"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
)

// ErrRevealBusy refuses a reveal while another one derives, so calls in
// parallel cannot guess the master password in parallel.
var ErrRevealBusy = errors.New("another reveal is running; try again")

var errRevealNoServer = errors.New("no such server")

// revealFields maps servers.reveal's wire names to the stored field names,
// which are also the encryption labels' and AADs' field part.
var revealFields = map[string]string{
	"password":      "encPassword",
	"suPassword":    "encSuPassword",
	"sudoPassword":  "encSudoPassword",
	"keyPassphrase": "encKeyPassphrase",
}

// encField is s's ciphertext field called stored (a revealFields value).
func encField(s *config.Server, stored string) *string {
	switch stored {
	case "encPassword":
		return &s.EncPassword
	case "encSuPassword":
		return &s.EncSuPassword
	case "encSudoPassword":
		return &s.EncSudoPassword
	}
	return &s.EncKeyPassphrase
}

// revealTargetLocked finds server's ciphertext for stored. h.mu is held.
func (h *Hub) revealTargetLocked(server, field, stored string) (config.Server, string, error) {
	for _, s := range h.deps.File.Servers {
		if s.Name == server {
			if blob := *encField(&s, stored); blob != "" {
				return s, blob, nil
			}
			return config.Server{}, "", errors.New("no saved " + field)
		}
	}
	return config.Server{}, "", errRevealNoServer
}

// RevealSecret returns one saved secret of server (UI door only; spec
// 2026-11-22-reveal-secret-design.md). password is the master password,
// checked on every call the way Unlock checks it; there is no grace window.
// The vault must be unlocked before and after the derive.
func (h *Hub) RevealSecret(server, field, password string) (string, error) {
	stored, ok := revealFields[field]
	if !ok {
		return "", errBadParams
	}
	if !h.revealing.CompareAndSwap(false, true) {
		return "", ErrRevealBusy
	}
	defer h.revealing.Store(false)

	h.mu.Lock()
	mk, err := h.writeKeyLocked()
	clear(mk)
	if err == nil {
		_, _, err = h.revealTargetLocked(server, field, stored)
	}
	if err != nil {
		h.mu.Unlock()
		return "", err
	}
	kdf := *h.deps.File.KDF
	h.mu.Unlock()

	dk, err := deriveKey(&kdf, password)
	if err != nil {
		return "", err
	}
	defer clear(dk)

	h.mu.Lock()
	defer h.mu.Unlock()
	f := h.deps.File
	if f == nil || f.KDF == nil || *f.KDF != kdf {
		return "", errors.New("the vault changed during unlock; try again")
	}
	if !f.KDF.Verify(dk) {
		h.auditConfig(broker.ConfigRecord{Action: "secretRevealDenied", Server: server, Field: field})
		return "", errors.New("wrong master password")
	}
	mk, err = h.writeKeyLocked() // the hub may have locked during the derive
	if err != nil {
		return "", err
	}
	defer clear(mk)
	s, blob, err := h.revealTargetLocked(server, field, stored) // the server may have changed too
	if err != nil {
		return "", err
	}
	v, err := config.Decrypt(mk, s.Name+"/"+stored, config.AADFor(f, s, stored), blob)
	if err != nil {
		return "", errors.New("could not decrypt the saved " + field)
	}
	h.auditConfig(broker.ConfigRecord{Action: "secretReveal", Server: server, Field: field})
	return v, nil
}
