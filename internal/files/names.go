// Package files plans and runs SFTP file operations for the hub: listings,
// mkdir, rename, and recursive upload, download, and delete. Remote names are
// untrusted; local I/O goes through os.Root.
package files

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

const (
	MaxList        = 10000  // entries one listing returns
	MaxPlanEntries = 100000 // entries one plan walks
	MaxDepth       = 64     // folder levels one plan walks
	MaxErrors      = 20     // errors a job keeps (it counts the rest)
)

var errBadName = errors.New("bad file name from the server")

// checkRemoteName: valid UTF-8 and exactly one path element.
func checkRemoteName(name string) error {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) || strings.ContainsAny(name, "/\\\x00") {
		return errBadName
	}
	return nil
}

// checkLocalName adds the local OS's rules (reserved names, ':' and
// trailing dots or spaces on Windows).
func checkLocalName(name string) error {
	if err := checkRemoteName(name); err != nil {
		return err
	}
	if l, err := filepath.Localize(name); err != nil || l != name || !filepath.IsLocal(name) {
		return errBadName
	}
	// Windows-only check for trailing dots/spaces and ':'.
	if runtime.GOOS == "windows" && (strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || strings.Contains(name, ":")) {
		return errBadName
	}
	return nil
}

// CheckAbs: an absolute, already-clean remote path.
func CheckAbs(p string) error {
	if p == "" || !path.IsAbs(p) || path.Clean(p) != p || strings.HasPrefix(p, "//") {
		return errors.New("path must be absolute and clean")
	}
	return nil
}

// checkMutable: CheckAbs, and never "/" or the login user's home itself.
func checkMutable(p, home string) error {
	if err := CheckAbs(p); err != nil {
		return err
	}
	if p == "/" || p == home {
		return errors.New("refusing to change / or the home directory itself")
	}
	return nil
}

// partName is a fresh hidden part-file name beside name.
func partName(name string) string {
	b := make([]byte, 8)
	rand.Read(b)
	return "." + name + "." + hex.EncodeToString(b) + ".sshgate-part"
}
