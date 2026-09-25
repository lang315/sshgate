package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecordHostKeyPinsWhenEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "servers.json")
	k, mk, _ := NewKDF("masterpw1")
	f := &File{Version: 1, KDF: &k, Servers: []Server{{Name: "s", Host: "h", Port: 22, User: "u", Auth: "password"}}}
	if err := Save(p, f, mk); err != nil {
		t.Fatal(err)
	}
	if err := RecordHostKey(p, "s", "h", 22, "SHA256:abc", "ssh-ed25519", mk); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(p)
	s, _ := got.FindServer("s")
	if s.HostKey != "SHA256:abc" || s.HostKeyAlgo != "ssh-ed25519" {
		t.Fatalf("got %q %q", s.HostKey, s.HostKeyAlgo)
	}
	if err := got.VerifyMAC(mk); err != nil {
		t.Fatal(err)
	}
}

// A pinned server is never re-pinned, and a key dialled at one endpoint is
// never recorded for a server that now points somewhere else.
func TestRecordHostKeySkipsPinnedMovedOrMissingServer(t *testing.T) {
	p := filepath.Join(t.TempDir(), "servers.json")
	k, mk, _ := NewKDF("masterpw1")
	f := &File{Version: 1, KDF: &k, Servers: []Server{
		{Name: "pinned", Host: "h", Port: 22, User: "u", Auth: "password", HostKey: "SHA256:original"},
		{Name: "open", Host: "h", Port: 22, User: "u", Auth: "password"},
	}}
	if err := Save(p, f, mk); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	for _, c := range []struct {
		name, host string
		port       int
	}{{"pinned", "h", 22}, {"open", "h2", 22}, {"open", "h", 2222}, {"gone", "h", 22}} {
		if err := RecordHostKey(p, c.name, c.host, c.port, "SHA256:attacker", "", mk); !errors.Is(err, ErrPinSkipped) {
			t.Fatalf("%+v: want ErrPinSkipped, got %v", c, err)
		}
	}
	if after, _ := os.ReadFile(p); !bytes.Equal(before, after) {
		t.Fatal("a skipped record changed the file")
	}
}

// Recording the key a server is already pinned to, at the same endpoint, is
// success without a write: two trusted opens of one key may race. A
// different key type is still a skip.
func TestRecordHostKeySamePinIsANoOpSuccess(t *testing.T) {
	p := filepath.Join(t.TempDir(), "servers.json")
	k, mk, _ := NewKDF("masterpw1")
	f := &File{Version: 1, KDF: &k, Servers: []Server{
		{Name: "s", Host: "h", Port: 22, User: "u", Auth: "password", HostKey: "SHA256:abc", HostKeyAlgo: "ssh-ed25519"},
	}}
	if err := Save(p, f, mk); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	if err := RecordHostKey(p, "s", "h", 22, "SHA256:abc", "ssh-ed25519", mk); err != nil {
		t.Fatalf("same pin: %v", err)
	}
	for _, algo := range []string{"ssh-rsa", ""} {
		if err := RecordHostKey(p, "s", "h", 22, "SHA256:abc", algo, mk); !errors.Is(err, ErrPinSkipped) {
			t.Fatalf("algo %q: want ErrPinSkipped, got %v", algo, err)
		}
	}
	if err := RecordHostKey(p, "s", "h", 2222, "SHA256:abc", "ssh-ed25519", mk); !errors.Is(err, ErrPinSkipped) {
		t.Fatalf("moved: want ErrPinSkipped, got %v", err)
	}
	if after, _ := os.ReadFile(p); !bytes.Equal(before, after) {
		t.Fatal("the file changed")
	}
}
