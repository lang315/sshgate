package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Server struct {
	Name             string `json:"name"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	User             string `json:"user"`
	Auth             string `json:"auth"`
	KeyPath          string `json:"keyPath,omitempty"`
	HostKey          string `json:"hostKey,omitempty"`
	EncPassword      string `json:"encPassword,omitempty"`
	EncSuPassword    string `json:"encSuPassword,omitempty"`
	EncSudoPassword  string `json:"encSudoPassword,omitempty"`
	EncKeyPassphrase string `json:"encKeyPassphrase,omitempty"`
}

type File struct {
	Version  int      `json:"version"`
	Revision int      `json:"revision"`
	KDF      *KDF     `json:"kdf,omitempty"`
	MAC      string   `json:"mac,omitempty"`
	Servers  []Server `json:"servers"`
}

func aadFor(f *File, s Server, field string) string {
	return strconv.Itoa(f.Version) + "|" + s.Name + "|" + field + "|" + s.Host + "|" +
		strconv.Itoa(s.Port) + "|" + s.User + "|" + s.Auth
}

// AADFor exposes aadFor for later tasks that need to compute the AAD outside this package's tests.
func AADFor(f *File, s Server, field string) string { return aadFor(f, s, field) }

func computeMAC(masterKey []byte, f File) string {
	f.MAC = ""
	canon, _ := json.Marshal(f)
	mac := hmac.New(sha256.New, subKey(masterKey, "file-mac"))
	mac.Write(canon)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (f *File) VerifyMAC(masterKey []byte) error {
	if f.MAC == "" {
		return nil // key/agent-only vault; perms are the protection
	}
	want := computeMAC(masterKey, *f)
	if !hmac.Equal([]byte(want), []byte(f.MAC)) {
		return fmt.Errorf("config MAC mismatch — file tampered or wrong master password")
	}
	return nil
}

func (f *File) FindServer(name string) (Server, bool) {
	for _, s := range f.Servers {
		if s.Name == name {
			return s, true
		}
	}
	return Server{}, false
}

func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func Save(path string, f *File, masterKey []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f.Revision++
	if masterKey != nil {
		f.MAC = computeMAC(masterKey, *f)
	} else {
		f.MAC = ""
	}
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".servers-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
