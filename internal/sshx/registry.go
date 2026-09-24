package sshx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

func ConfigHash(c DialConfig) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%s|%s|%s|%s|%s|%s|%s|%v", c.Host, c.Port, c.User, c.Auth,
		c.Password, c.PrivateKey, c.Passphrase, c.SuPassword, c.SudoPassword, c.HostKey, c.Insecure)
	return hex.EncodeToString(h.Sum(nil))
}

type Registry struct {
	mu sync.Mutex
	m  map[string]*Manager
}

func NewRegistry() *Registry { return &Registry{m: map[string]*Manager{}} }

// Get compares against the manager's current config, pin included, so a
// manager that just learned the pin cfg now carries is kept.
func (r *Registry) Get(name string, cfg DialConfig) *Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	if mgr, ok := r.m[name]; ok {
		if ConfigHash(mgr.currentConfig()) == ConfigHash(cfg) {
			return mgr
		}
		mgr.Close()
	}
	mgr := NewManager(cfg)
	r.m[name] = mgr
	return mgr
}

func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, mgr := range r.m {
		mgr.Close()
	}
	r.m = map[string]*Manager{}
}
