package config

import (
	"path/filepath"
	"testing"
)

func TestRecordHostKeyPinsWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "servers.json")
	k, mk, _ := NewKDF("masterpw1")
	f := &File{Version: 1, KDF: &k, Servers: []Server{{Name: "s", Host: "h", Port: 22, User: "u", Auth: "password"}}}
	if err := Save(p, f, mk); err != nil {
		t.Fatal(err)
	}
	if err := RecordHostKey(p, "s", "SHA256:abc", mk); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(p)
	s, _ := got.FindServer("s")
	if s.HostKey != "SHA256:abc" {
		t.Fatalf("hostKey = %q, want SHA256:abc", s.HostKey)
	}
}

func TestRecordHostKeyNoOpWhenAlreadyPinned(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "servers.json")
	k, mk, _ := NewKDF("masterpw1")
	f := &File{Version: 1, KDF: &k, Servers: []Server{{Name: "s", Host: "h", Port: 22, User: "u", Auth: "password", HostKey: "SHA256:original"}}}
	if err := Save(p, f, mk); err != nil {
		t.Fatal(err)
	}
	if err := RecordHostKey(p, "s", "SHA256:attacker", mk); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(p)
	s, _ := got.FindServer("s")
	if s.HostKey != "SHA256:original" {
		t.Fatal("must NOT overwrite an already-pinned host key")
	}
}
