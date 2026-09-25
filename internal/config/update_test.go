package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func vaultAt(t *testing.T) (string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "servers.json")
	k, mk, err := NewKDF("pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, &File{Version: 1, KDF: &k, Servers: []Server{}}, mk); err != nil {
		t.Fatal(err)
	}
	return path, mk
}

func TestUpdateSerializesConcurrentWriters(t *testing.T) {
	path, mk := vaultAt(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := Update(path, mk, func(f *File) error {
				f.Servers = append(f.Servers, Server{Name: fmt.Sprintf("s%d", i), Host: "h", Port: 22, User: "u", Auth: "agent"})
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Servers) != 20 || f.Revision != 21 {
		t.Fatalf("lost writes: %d servers, revision %d", len(f.Servers), f.Revision)
	}
	if err := f.VerifyMAC(mk); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRefusesEncryptedStoreWithoutItsKey(t *testing.T) {
	path, _ := vaultAt(t)
	_, wrong, _ := NewKDF("other")
	before, _ := os.ReadFile(path)
	for _, key := range [][]byte{nil, wrong} {
		called := false
		err := Update(path, key, func(*File) error { called = true; return nil })
		if err == nil || called {
			t.Fatalf("key given=%v: err %v, fn called %v", key != nil, err, called)
		}
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("a refused update changed the file")
	}
}

func TestUpdateVerifiesMACBeforeWriting(t *testing.T) {
	path, mk := vaultAt(t)
	raw, _ := os.ReadFile(path)
	tampered := bytes.Replace(raw, []byte(`"servers": []`),
		[]byte(`"servers": [{"name":"evil","host":"x","port":22,"user":"u","auth":"agent","aiVisible":true}]`), 1)
	if bytes.Equal(raw, tampered) {
		t.Fatal("tamper did not apply")
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	err := Update(path, mk, func(*File) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "MAC") {
		t.Fatalf("want a MAC error, got %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, tampered) {
		t.Fatal("a tampered store was re-signed")
	}
}

func TestUpdateStartsEmptyAndWritesNothingOnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "servers.json")
	if err := Update(path, nil, func(*File) error { return errors.New("no") }); err == nil {
		t.Fatal("fn error not returned")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a failed update created the store: %v", err)
	}
	err := Update(path, nil, func(f *File) error {
		f.Servers = append(f.Servers, Server{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil || f.Version != 1 || f.Revision != 1 || len(f.Servers) != 1 {
		t.Fatalf("%v %+v", err, f)
	}
}

// A store stripped of its KDF (and so of its MAC check) must not be re-signed
// by a caller that holds the key; one that adds the KDF in fn still saves.
func TestUpdateRefusesStoreThatLostItsKDF(t *testing.T) {
	path, mk := vaultAt(t)
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f.KDF, f.MAC = nil, ""
	f.Servers = append(f.Servers, Server{Name: "evil", Host: "x", Port: 22, User: "u", Auth: "agent", AIVisible: true})
	f.Revision++
	raw, _ := json.Marshal(f)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, mk, func(*File) error { return nil }); err == nil {
		t.Fatal("a KDF-stripped store was re-signed")
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, raw) {
		t.Fatal("a refused update changed the file")
	}
	k, mk2, err := NewKDF("pw2")
	if err != nil {
		t.Fatal(err)
	}
	if err := Update(path, mk2, func(f *File) error { f.KDF = &k; return nil }); err != nil {
		t.Fatalf("creating a vault from a KDF-less file: %v", err)
	}
}
