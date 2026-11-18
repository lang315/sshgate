package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var keysTestMK = []byte("0123456789abcdef0123456789abcdef")

func newKeysFile() *File { return &File{Version: 1} }

func TestAddKeyEncryptsBlob(t *testing.T) {
	f := newKeysFile()
	blob := []byte("secret-data-representation")
	if err := AddKey(f, keysTestMK, "k1", "pub1", blob, time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}
	k := f.Keys[0]
	if k.Type != "se-p256" || k.Name != "k1" || k.PublicKey != "pub1" || k.Created == "" {
		t.Fatalf("bad key %+v", k)
	}
	if strings.Contains(k.EncBlob, base64.StdEncoding.EncodeToString(blob)) || strings.Contains(k.EncBlob, string(blob)) {
		t.Fatal("EncBlob holds the blob in the clear")
	}
	got, pub, err := KeyBlob(f, keysTestMK, "k1")
	if err != nil || string(got) != string(blob) || pub != "pub1" {
		t.Fatalf("KeyBlob = %q %q %v", got, pub, err)
	}
	if _, _, err := KeyBlob(f, keysTestMK, "nope"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("missing key: %v", err)
	}
}

func TestKeyBlobBoundToNameAndPublicKey(t *testing.T) {
	f := newKeysFile()
	now := time.Now()
	if err := AddKey(f, keysTestMK, "a", "pubA", []byte("blobA"), now); err != nil {
		t.Fatal(err)
	}
	if err := AddKey(f, keysTestMK, "b", "pubB", []byte("blobB"), now); err != nil {
		t.Fatal(err)
	}
	f.Keys[0].EncBlob, f.Keys[1].EncBlob = f.Keys[1].EncBlob, f.Keys[0].EncBlob
	if _, _, err := KeyBlob(f, keysTestMK, "a"); err == nil {
		t.Fatal("swapped blob decrypted")
	}
	f.Keys[0].EncBlob, f.Keys[1].EncBlob = f.Keys[1].EncBlob, f.Keys[0].EncBlob
	f.Keys[0].PublicKey = "pubX"
	if _, _, err := KeyBlob(f, keysTestMK, "a"); err == nil {
		t.Fatal("changed public key decrypted")
	}
}

func TestAddKeyRefusesDuplicateAndBadNames(t *testing.T) {
	f := newKeysFile()
	if err := AddKey(f, keysTestMK, "k1", "p", []byte("b"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := AddKey(f, keysTestMK, "k1", "p", []byte("b"), time.Now()); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("dup: %v", err)
	}
	for _, n := range []string{"", strings.Repeat("a", 65), "a b", "ä"} {
		if err := AddKey(f, keysTestMK, n, "p", []byte("b"), time.Now()); err == nil {
			t.Fatalf("name %q accepted", n)
		}
	}
	if len(f.Keys) != 1 {
		t.Fatalf("keys = %d", len(f.Keys))
	}
}

func TestDeleteKeyInUse(t *testing.T) {
	f := newKeysFile()
	if err := AddKey(f, keysTestMK, "k1", "p", []byte("b"), time.Now()); err != nil {
		t.Fatal(err)
	}
	f.Servers = []Server{{Name: "s1", Auth: "keychain", KeyName: "k1"}}
	err := DeleteKey(f, "k1")
	if !errors.Is(err, ErrKeyInUse) || !strings.Contains(err.Error(), "s1") {
		t.Fatalf("in use: %v", err)
	}
	f.Servers[0].Auth, f.Servers[0].KeyName = "password", ""
	if err := DeleteKey(f, "k1"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteKey(f, "k1"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("gone: %v", err)
	}
}

func TestApplyServerKeychain(t *testing.T) {
	f := newKeysFile()
	if err := AddKey(f, keysTestMK, "k1", "p", []byte("b"), time.Now()); err != nil {
		t.Fatal(err)
	}
	in := ServerInput{Name: "s", Host: "h", Port: 22, User: "u", Auth: "keychain", KeyName: "nope"}
	if _, _, err := ApplyServer(f, "", in, keysTestMK); !errors.Is(err, ErrNoKey) {
		t.Fatalf("unknown: %v", err)
	}
	in.KeyName = "k1"
	if _, after, err := ApplyServer(f, "", in, keysTestMK); err != nil || after.KeyName != "k1" {
		t.Fatalf("known: %+v %v", after, err)
	}
	in.Name = "s2"
	if _, after, err := ApplyServer(f, "s", in, keysTestMK); err != nil || after.KeyName != "k1" || after.Name != "s2" {
		t.Fatalf("rename: %+v %v", after, err)
	}
	in.Auth = "password"
	if _, after, err := ApplyServer(f, "s2", in, keysTestMK); err != nil || after.KeyName != "" {
		t.Fatalf("password: %+v %v", after, err)
	}
	if err := (ServerInput{Name: "s", Host: "h", Port: 22, User: "u", Auth: "keychain"}).Validate(); err == nil {
		t.Fatal("keychain without keyName validated")
	}
	if err := (ServerInput{Name: "s", Host: "h", Port: 22, User: "u", Auth: "keychain", KeyName: "k1"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFileWithoutKeysUnchanged(t *testing.T) {
	b, _ := json.Marshal(File{Version: 1, Servers: []Server{{Name: "s", Auth: "password"}}})
	if strings.Contains(string(b), `"keys"`) || strings.Contains(string(b), `"keyName"`) {
		t.Fatalf("keyless JSON changed: %s", b)
	}
}
