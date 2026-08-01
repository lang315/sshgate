package sshx

import "testing"

func TestRegistryReusesSameConfig(t *testing.T) {
	r := NewRegistry()
	cfg := DialConfig{Host: "h", Port: 22, User: "u", Auth: "password", Password: "p"}
	m1 := r.Get("a", cfg)
	m2 := r.Get("a", cfg)
	if m1 != m2 {
		t.Fatal("same name+config should reuse manager")
	}
}

func TestRegistryReplacesOnConfigChange(t *testing.T) {
	r := NewRegistry()
	m1 := r.Get("a", DialConfig{Host: "h", Port: 22, User: "u", Auth: "password", Password: "p"})
	m2 := r.Get("a", DialConfig{Host: "h2", Port: 22, User: "u", Auth: "password", Password: "p"})
	if m1 == m2 {
		t.Fatal("config change should create a new manager")
	}
}
