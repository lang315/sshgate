package hub

import (
	"context"
	"slices"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sekey"
	"github.com/lang315/sshgate/internal/sshx"
)

// uiKey is a Keychain key as the UI door lists it: never the blob.
type uiKey struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	PublicKey string   `json:"publicKey"`
	Created   string   `json:"created"`
	UsedBy    []string `json:"usedBy"`
}

// createdKey is the keys.create reply: a new key is used by nothing, so no usedBy.
type createdKey struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	PublicKey string `json:"publicKey"`
	Created   string `json:"created"`
}

// ListKeys needs an unlocked vault.
func (h *Hub) ListKeys() ([]uiKey, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k, err := h.writeKeyLocked()
	if err != nil {
		return nil, err
	}
	clear(k)
	out := []uiKey{}
	for _, k := range h.deps.File.Keys {
		users := h.deps.File.KeyUsers(k.Name)
		if users == nil {
			users = []string{}
		}
		out = append(out, uiKey{k.Name, k.Type, k.PublicKey, k.Created, users})
	}
	return out, nil
}

// CreateKey makes a Secure Enclave key through the helper and stores its
// blob in the vault. The lock state and the name are checked before the
// helper runs, and h.mu is not held across it.
func (h *Hub) CreateKey(ctx context.Context, name string) (createdKey, error) {
	h.mu.Lock()
	k, err := h.writeKeyLocked() // lock state first, then the name, then uniqueness
	if err == nil {
		clear(k)
		err = config.ValidKeyName(name)
	}
	if err == nil {
		if slices.ContainsFunc(h.deps.File.Keys, func(k config.Key) bool { return k.Name == name }) {
			err = config.ErrKeyExists
		}
	}
	h.mu.Unlock()
	if err != nil {
		return createdKey{}, err
	}
	if h.o.KeyHelper == "" {
		return createdKey{}, sshx.ErrSEKeyNoHelper
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	blob, pub, err := sekey.Create(cctx, h.o.KeyHelper)
	if err != nil {
		return createdKey{}, err
	}
	defer clear(blob)
	pubLine := sekey.AuthorizedKey(pub, name)

	h.mu.Lock()
	defer h.mu.Unlock()
	mk, err := h.writeKeyLocked() // the lock state may have changed meanwhile
	if err != nil {
		return createdKey{}, err
	}
	defer clear(mk)
	now := time.Now()
	// A lost race returns ErrKeyExists; the Secure Enclave key made above is dropped: it cannot be used without its blob.
	err = config.Update(h.o.StorePath, mk, func(f *config.File) error {
		return config.AddKey(f, mk, name, pubLine, blob, now)
	})
	if err != nil {
		return createdKey{}, err
	}
	reloadErr := h.reloadLocked()
	h.auditConfig(broker.ConfigRecord{Action: "keyCreate", Key: name})
	return createdKey{Name: name, Type: "se-p256", PublicKey: pubLine, Created: now.UTC().Format(time.RFC3339)}, reloadErr
}

// DeleteKey refuses a key a server still uses.
func (h *Hub) DeleteKey(name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	mk, err := h.writeKeyLocked()
	if err != nil {
		return err
	}
	defer clear(mk)
	if err := config.Update(h.o.StorePath, mk, func(f *config.File) error { return config.DeleteKey(f, name) }); err != nil {
		return err
	}
	reloadErr := h.reloadLocked()
	h.auditConfig(broker.ConfigRecord{Action: "keyDelete", Key: name})
	return reloadErr
}
