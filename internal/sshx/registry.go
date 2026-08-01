package sshx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

func ConfigHash(c DialConfig) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%s|%s|%s|%s|%s|%v", c.Host, c.Port, c.User, c.Auth,
		c.Password, c.PrivateKey, c.SuPassword, c.SudoPassword, c.Insecure)
	return hex.EncodeToString(h.Sum(nil))
}

type entry struct {
	hash string
	mgr  *Manager
}

type Registry struct {
	mu sync.Mutex
	m  map[string]entry
}

func NewRegistry() *Registry { return &Registry{m: map[string]entry{}} }

func (r *Registry) Get(name string, cfg DialConfig) *Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	hash := ConfigHash(cfg)
	if e, ok := r.m[name]; ok {
		if e.hash == hash {
			return e.mgr
		}
		e.mgr.Close()
	}
	mgr := NewManager(cfg)
	r.m[name] = entry{hash: hash, mgr: mgr}
	return mgr
}

func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.m {
		e.mgr.Close()
	}
	r.m = map[string]entry{}
}
