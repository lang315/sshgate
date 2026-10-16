package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ghosttySHA is the SHA-256 of the libghostty-vt the packaging job built
// and shipped beside this binary (-X main.ghosttySHA=...). The build is
// not reproducible, so it binds the app to that exact copy.
var ghosttySHA string

// checkLibrary refuses a terminal library that is not the shipped one:
// outside resources, or with another SHA-256 than want.
func checkLibrary(path, resources, want string) error {
	if want == "" {
		return errors.New("no libghostty-vt hash was embedded at build time")
	}
	if resources == "" {
		return errors.New("no resources directory")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	res, err := filepath.Abs(resources)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(res, abs); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("libghostty-vt %s is not inside %s", abs, res)
	}
	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("libghostty-vt %s has SHA-256 %s, want %s", abs, got, want)
	}
	return nil
}
