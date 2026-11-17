package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/config"
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

// The file branch serves only the hub: strict, no learner, algorithm pinned.
func TestResolveNamedIsStrictWithNoLearner(t *testing.T) {
	f := &config.File{Version: 1, Servers: []config.Server{{Name: "p", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:x", HostKeyAlgo: "ssh-ed25519"}}}
	dc, err := (&Deps{File: f}).Resolve("p")
	if err != nil {
		t.Fatal(err)
	}
	if !dc.StrictHostKey || dc.OnLearnHostKey != nil || dc.HostKeyAlgo != "ssh-ed25519" {
		t.Fatalf("got %+v", dc)
	}
	cli, err := (&Deps{CLI: &config.CLIConfig{Host: "h", Port: 22, User: "u", HasHost: true}}).Resolve("")
	if err != nil || cli.StrictHostKey {
		t.Fatalf("--host mode must keep TOFU: %v %+v", err, cli)
	}
}

func TestResolveKeychain(t *testing.T) {
	k, mk, _ := config.NewKDF("pw")
	f := &config.File{Version: 1, KDF: &k, Servers: []config.Server{{Name: "kc", Host: "h", Port: 22, User: "u", Auth: "keychain", KeyName: "k1"}}}
	if err := config.AddKey(f, mk, "k1", "ecdsa-sha2-nistp256 AAAA", []byte("opaque-blob"), time.Now()); err != nil {
		t.Fatal(err)
	}
	d := &Deps{File: f, MasterKey: mk, KeyHelper: "/h/sshgate-touchid"}
	dc, err := d.Resolve("kc")
	if err != nil {
		t.Fatal(err)
	}
	if string(dc.SEKeyBlob) != "opaque-blob" || dc.PublicKey != "ecdsa-sha2-nistp256 AAAA" || dc.KeyHelper != "/h/sshgate-touchid" {
		t.Fatalf("got blob=%q pub=%q helper=%q", dc.SEKeyBlob, dc.PublicKey, dc.KeyHelper)
	}
	d.MasterKey = nil
	if _, err := d.Resolve("kc"); err == nil || !strings.Contains(err.Error(), "vault locked") {
		t.Fatalf("locked resolve: %v", err)
	}
	if !d.IsLocked("kc") {
		t.Fatal("IsLocked must be true for a keychain server without the master key, though no Enc* field is set")
	}
}
