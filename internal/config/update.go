package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// updateMu serializes writers in this process; withFlock serializes
// processes (a no-op off unix, where this mutex is all there is).
var updateMu sync.Mutex

// Update is the only writer of the store. It reloads the file (a missing one
// starts empty), checks the key and MAC of an encrypted store, applies fn,
// and saves. Nothing is written if any step fails.
func Update(path string, masterKey []byte, fn func(*File) error) error {
	updateMu.Lock()
	defer updateMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return withFlock(path, func() error {
		f, err := Load(path)
		if os.IsNotExist(err) {
			f, err = &File{Version: 1, Servers: []Server{}}, nil
		}
		if err != nil {
			return err
		}
		if f.KDF != nil {
			if masterKey == nil {
				return errors.New("store is encrypted; unlock it before saving")
			}
			if !f.KDF.Verify(masterKey) {
				return errors.New("wrong master key for this store")
			}
			if err := f.VerifyMAC(masterKey); err != nil {
				return err
			}
		}
		if err := fn(f); err != nil {
			return err
		}
		// A key with no KDF would MAC a file nothing authenticated: an
		// encrypted store stripped of its KDF (and so of its MAC check).
		// Creating a vault sets the KDF in fn, so it still passes.
		if masterKey != nil && f.KDF == nil {
			return errors.New("store has no master password but a key was given; it was tampered with")
		}
		return Save(path, f, masterKey)
	})
}
