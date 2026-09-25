package sshx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

func ConfigHash(c DialConfig) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%s|%s|%s|%s|%s|%s|%s|%s|%v", c.Host, c.Port, c.User, c.Auth,
		c.Password, c.PrivateKey, c.Passphrase, c.SuPassword, c.SudoPassword, c.HostKey, c.HostKeyAlgo, c.Insecure)
	return hex.EncodeToString(h.Sum(nil))
}

type Registry struct {
	mu sync.Mutex
	m  map[string]*Manager
}

func NewRegistry() *Registry { return &Registry{m: map[string]*Manager{}} }

// Get compares against the manager's current config, pin included, so a
// manager that just learned the pin cfg now carries is kept. An empty pin in
// a non-strict cfg means the caller has none to offer (--host mode): the
// manager's learned pin still governs its redials. A strict (hub) caller's
// empty pin means "unpinned" and is never filled from the cache.
func (r *Registry) Get(name string, cfg DialConfig) *Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	if mgr, ok := r.m[name]; ok {
		cur := mgr.currentConfig()
		want := cfg
		if want.HostKey == "" && !want.StrictHostKey {
			want.HostKey = cur.HostKey
		}
		if ConfigHash(cur) == ConfigHash(want) {
			return mgr
		}
		mgr.Close()
	}
	mgr := NewManager(cfg)
	r.m[name] = mgr
	return mgr
}

// Close closes and forgets name's manager; its terminals see EOF.
func (r *Registry) Close(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if mgr, ok := r.m[name]; ok {
		mgr.Close()
		delete(r.m, name)
	}
}

func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, mgr := range r.m {
		mgr.Close()
	}
	r.m = map[string]*Manager{}
}
