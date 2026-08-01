package mcpserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lang315/ssh-mcp/internal/config"
)

func TestResolveCLIDefault(t *testing.T) {
	d := &Deps{CLI: &config.CLIConfig{Host: "h", Port: 22, User: "u", Password: "p", HasHost: true, TimeoutMs: 60000}}
	dc, err := d.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if dc.Host != "h" || dc.Password != "p" {
		t.Fatalf("got %+v", dc)
	}
}

func TestResolveLockedVault(t *testing.T) {
	f := &config.File{Version: 1, Servers: []config.Server{{Name: "p", Host: "h", Port: 22, User: "u", Auth: "password", EncPassword: "xxx"}}}
	d := &Deps{File: f, MasterKey: nil}
	if _, err := d.Resolve("p"); err == nil {
		t.Fatal("encrypted server without master key must error 'vault locked'")
	}
	if !d.IsLocked("p") {
		t.Fatal("IsLocked should be true")
	}
}

func TestResolveCLIKeyAuthReadsKeyFile(t *testing.T) {
	dir := t.TempDir()
	kp := filepath.Join(dir, "id")
	if err := os.WriteFile(kp, []byte("PRIVATE-KEY-DATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Deps{CLI: &config.CLIConfig{Host: "h", Port: 22, User: "u", Key: kp, HasHost: true, TimeoutMs: 60000}}
	dc, err := d.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if dc.Auth != "key" || dc.PrivateKey != "PRIVATE-KEY-DATA" {
		t.Fatalf("got auth=%q privkey=%q", dc.Auth, dc.PrivateKey)
	}
}

func TestResolveNamedKeyPassphraseLockedVault(t *testing.T) {
	f := &config.File{Version: 1, Servers: []config.Server{{Name: "k", Host: "h", Port: 22, User: "u", Auth: "key", KeyPath: "/nonexistent", EncKeyPassphrase: "xxx"}}}
	d := &Deps{File: f, MasterKey: nil}
	if _, err := d.Resolve("k"); err == nil {
		t.Fatal("encrypted key passphrase without master key must error 'vault locked'")
	}
}

func TestResolveNamedDecryptsPassword(t *testing.T) {
	k, mk, _ := config.NewKDF("pw")
	f := &config.File{Version: 1, KDF: &k, Servers: []config.Server{{Name: "p", Host: "h", Port: 22, User: "root", Auth: "password"}}}
	enc, _ := config.Encrypt(mk, "p/encPassword", config.AADFor(f, f.Servers[0], "encPassword"), "s3cret")
	f.Servers[0].EncPassword = enc
	d := &Deps{File: f, MasterKey: mk}
	dc, err := d.Resolve("p")
	if err != nil {
		t.Fatal(err)
	}
	if dc.Password != "s3cret" {
		t.Fatalf("password=%q", dc.Password)
	}
}
