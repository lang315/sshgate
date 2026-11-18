package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Key is a Secure Enclave key kept in the vault. EncBlob is the key's
// dataRepresentation, encrypted under the master key and bound to Name and
// PublicKey; it never leaves the hub except to the signing helper's stdin.
type Key struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	PublicKey string `json:"publicKey"`
	Created   string `json:"created"`
	EncBlob   string `json:"encBlob"`
}

var (
	ErrKeyExists = errors.New("a key with this name already exists")
	ErrKeyInUse  = errors.New("key is in use")
	ErrNoKey     = errors.New("no such key")
)

var keyNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func ValidKeyName(name string) error {
	if !keyNameRe.MatchString(name) {
		return errors.New("key name: use 1-64 characters of A-Z a-z 0-9 . _ -")
	}
	return nil
}

func keyBlobAAD(f *File, name, publicKey string) string {
	return strconv.Itoa(f.Version) + "|keychain|" + name + "|" + publicKey
}

func (f *File) findKey(name string) *Key {
	for i := range f.Keys {
		if f.Keys[i].Name == name {
			return &f.Keys[i]
		}
	}
	return nil
}

// AddKey encrypts blob and appends the key. It does not save the file.
func AddKey(f *File, masterKey []byte, name, publicKey string, blob []byte, now time.Time) error {
	if err := ValidKeyName(name); err != nil {
		return err
	}
	if f.findKey(name) != nil {
		return ErrKeyExists
	}
	enc, err := Encrypt(masterKey, "keychain/"+name+"/blob", keyBlobAAD(f, name, publicKey), base64.StdEncoding.EncodeToString(blob))
	if err != nil {
		return err
	}
	f.Keys = append(f.Keys, Key{Name: name, Type: "se-p256", PublicKey: publicKey, Created: now.UTC().Format(time.RFC3339), EncBlob: enc})
	return nil
}

// KeyUsers lists the servers that use key name.
func (f *File) KeyUsers(name string) []string {
	var out []string
	for _, s := range f.Servers {
		if s.Auth == "keychain" && s.KeyName == name {
			out = append(out, s.Name)
		}
	}
	return out
}

// DeleteKey removes the key unless a server uses it.
func DeleteKey(f *File, name string) error {
	for i := range f.Keys {
		if f.Keys[i].Name != name {
			continue
		}
		if users := f.KeyUsers(name); len(users) > 0 {
			return fmt.Errorf("%w: server %q uses it", ErrKeyInUse, users[0])
		}
		f.Keys = append(f.Keys[:i], f.Keys[i+1:]...)
		return nil
	}
	return ErrNoKey
}

// KeyBlob decrypts the key's blob and returns it with the public key.
func KeyBlob(f *File, masterKey []byte, name string) (blob []byte, publicKey string, err error) {
	k := f.findKey(name)
	if k == nil {
		return nil, "", ErrNoKey
	}
	pt, err := Decrypt(masterKey, "keychain/"+name+"/blob", keyBlobAAD(f, name, k.PublicKey), k.EncBlob)
	if err != nil {
		return nil, "", errors.New("key blob does not decrypt")
	}
	blob, err = base64.StdEncoding.DecodeString(pt)
	if err != nil {
		return nil, "", errors.New("key blob does not decrypt")
	}
	return blob, k.PublicKey, nil
}
