package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	k, mk, _ := NewKDF("pw")
	f := &File{Version: 1, KDF: &k, Servers: []Server{{Name: "p", Host: "h", Port: 22, User: "root", Auth: "password"}}}
	enc, _ := Encrypt(mk, "p/encPassword", aadFor(f, f.Servers[0], "encPassword"), "topsecret")
	f.Servers[0].EncPassword = enc
	if err := Save(path, f, mk); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.VerifyMAC(mk); err != nil {
		t.Fatalf("mac: %v", err)
	}
	if loaded.Revision != 1 {
		t.Fatalf("revision = %d", loaded.Revision)
	}
	s, _ := loaded.FindServer("p")
	got, err := Decrypt(mk, "p/encPassword", aadFor(loaded, s, "encPassword"), s.EncPassword)
	if err != nil || got != "topsecret" {
		t.Fatalf("decrypt got %q err %v", got, err)
	}
}

func TestMACTamperDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	k, mk, _ := NewKDF("pw")
	f := &File{Version: 1, KDF: &k, Servers: []Server{{Name: "p", Host: "h", Port: 22, User: "root", Auth: "password"}}}
	Save(path, f, mk)
	raw, _ := os.ReadFile(path)
	tampered := []byte(string(raw))
	// flip host h -> x by editing bytes
	for i := range tampered {
		if tampered[i] == 'h' {
			tampered[i] = 'x'
			break
		}
	}
	os.WriteFile(path, tampered, 0o600)
	loaded, _ := Load(path)
	if err := loaded.VerifyMAC(mk); err == nil {
		t.Fatal("tamper must be detected by MAC")
	}
}

func TestAIVisibleRoundtripsAndDefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	f := &File{Version: 1, Servers: []Server{
		{Name: "a", Host: "h", Port: 22, User: "u", Auth: "key", AIVisible: true},
		{Name: "b", Host: "h", Port: 22, User: "u", Auth: "key"},
	}}
	if err := Save(path, f, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Servers[0].AIVisible || loaded.Servers[1].AIVisible {
		t.Fatalf("aiVisible not preserved: %+v", loaded.Servers)
	}
}

func TestSaveKeylessClearsMAC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	k, mk, _ := NewKDF("pw")
	f := &File{Version: 1, KDF: &k, Servers: []Server{{Name: "p", Host: "h", Port: 22, User: "root", Auth: "password"}}}
	if err := Save(path, f, mk); err != nil {
		t.Fatal(err)
	}
	if f.MAC == "" {
		t.Fatal("keyed save should set a MAC")
	}
	// now save keyless — stale MAC must be cleared, and the reloaded file must verify
	if err := Save(path, f, nil); err != nil {
		t.Fatal(err)
	}
	if f.MAC != "" {
		t.Fatal("keyless save must clear the MAC")
	}
	loaded, _ := Load(path)
	if err := loaded.VerifyMAC(nil); err != nil {
		t.Fatalf("keyless file must verify with nil key: %v", err)
	}
}
