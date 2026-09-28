package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ServerInput is a server as the app edits it. Secrets are write-only: nil
// keeps the stored value, "" clears it, anything else sets it. The host key
// is never an input.
type ServerInput struct {
	Name          string  `json:"name"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	User          string  `json:"user"`
	Auth          string  `json:"auth"`
	KeyPath       string  `json:"keyPath"`
	AIVisible     bool    `json:"aiVisible"`
	Password      *string `json:"password"`
	SuPassword    *string `json:"suPassword"`
	SudoPassword  *string `json:"sudoPassword"`
	KeyPassphrase *string `json:"keyPassphrase"`
}

var serverNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// plainToken: non-empty, no whitespace, no control or format runes.
func plainToken(s string) bool {
	_, _, bad := forbiddenRune(s)
	return s != "" && !bad && !strings.ContainsFunc(s, unicode.IsSpace)
}

// Validate names the first bad field.
func (in ServerInput) Validate() error {
	switch {
	case !serverNameRe.MatchString(in.Name):
		return errors.New("name: use 1-64 characters of A-Z a-z 0-9 . _ -")
	case !plainToken(in.Host):
		return errors.New("host: required, with no spaces or control characters")
	case in.Port < 1 || in.Port > 65535:
		return errors.New("port: must be 1-65535")
	case !plainToken(in.User):
		return errors.New("user: required, with no spaces or control characters")
	case in.Auth != "password" && in.Auth != "key" && in.Auth != "agent":
		return errors.New("auth: must be password, key, or agent")
	case in.Auth == "key" && in.KeyPath == "":
		return errors.New("keyPath: required for key auth")
	}
	return nil
}

// ApplyServer creates (original == "") or updates server original from in
// and returns it before and after. A kept secret is re-encrypted only when
// its AAD changes (name, user, auth). A new host or port drops the pin and
// every secret in does not re-supply: the AAD binds secrets to the endpoint,
// and carrying them over would hand them to whoever answers there. Tunnels are
// kept as they are; only tunnels.save and tunnels.delete change them.
// AutoAllow is never carried over: every save turns auto-allow off.
func ApplyServer(f *File, original string, in ServerInput, masterKey []byte) (before, after Server, err error) {
	idx := -1
	if original != "" {
		for i := range f.Servers {
			if f.Servers[i].Name == original {
				idx = i
			}
		}
		if idx < 0 {
			return before, after, fmt.Errorf("server %q not found", original)
		}
		before = f.Servers[idx]
	}
	if in.Name != original {
		if _, dup := f.FindServer(in.Name); dup {
			return before, after, fmt.Errorf("a server named %q already exists", in.Name)
		}
	}
	if in.Auth != "key" {
		in.KeyPath = ""
	}
	after = Server{Name: in.Name, Host: in.Host, Port: in.Port, User: in.User, Auth: in.Auth, KeyPath: in.KeyPath, AIVisible: in.AIVisible, Tunnels: before.Tunnels}
	moved := idx >= 0 && (before.Host != in.Host || before.Port != in.Port)
	if idx >= 0 && !moved {
		after.HostKey, after.HostKeyAlgo = before.HostKey, before.HostKeyAlgo
	}
	for _, s := range []struct {
		field string
		in    *string
		old   string
		dst   *string
	}{
		{"encPassword", in.Password, before.EncPassword, &after.EncPassword},
		{"encSuPassword", in.SuPassword, before.EncSuPassword, &after.EncSuPassword},
		{"encSudoPassword", in.SudoPassword, before.EncSudoPassword, &after.EncSudoPassword},
		{"encKeyPassphrase", in.KeyPassphrase, before.EncKeyPassphrase, &after.EncKeyPassphrase},
	} {
		switch {
		case s.in != nil && *s.in == "", s.in == nil && (s.old == "" || moved):
			// cleared, nothing stored, or the endpoint changed: stays empty
		case s.in == nil && aadFor(f, before, s.field) == aadFor(f, after, s.field):
			*s.dst = s.old
		default:
			if masterKey == nil {
				return before, after, errors.New("saving a password needs an unlocked vault")
			}
			pt := ""
			if s.in != nil {
				pt = *s.in
			} else if pt, err = Decrypt(masterKey, before.Name+"/"+s.field, aadFor(f, before, s.field), s.old); err != nil {
				return before, after, fmt.Errorf("re-encrypting %s: %w", s.field, err)
			}
			if *s.dst, err = Encrypt(masterKey, after.Name+"/"+s.field, aadFor(f, after, s.field), pt); err != nil {
				return before, after, err
			}
		}
	}
	if idx >= 0 {
		f.Servers[idx] = after
	} else {
		f.Servers = append(f.Servers, after)
	}
	return before, after, nil
}
