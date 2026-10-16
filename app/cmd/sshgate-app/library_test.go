package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckLibrary(t *testing.T) {
	res := t.TempDir()
	lib := filepath.Join(res, "libghostty-vt.dylib")
	os.WriteFile(lib, []byte("lib"), 0o644)
	sum := sha256.Sum256([]byte("lib"))
	good := hex.EncodeToString(sum[:])
	if err := checkLibrary(lib, res, good); err != nil {
		t.Fatalf("good library refused: %v", err)
	}
	if checkLibrary(lib, res, "00"+good[2:]) == nil {
		t.Fatal("wrong hash accepted")
	}
	if checkLibrary(lib, res, "") == nil {
		t.Fatal("no embedded hash accepted")
	}
	other := filepath.Join(t.TempDir(), "libghostty-vt.dylib")
	os.WriteFile(other, []byte("lib"), 0o644)
	if checkLibrary(other, res, good) == nil {
		t.Fatal("library outside Resources accepted")
	}
	if checkLibrary(filepath.Join(res, "..", filepath.Base(filepath.Dir(other)), "x"), res, good) == nil {
		t.Fatal("path escaping Resources accepted")
	}
	t.Chdir(res) // "" would otherwise resolve to the cwd, which holds lib
	if checkLibrary(lib, "", good) == nil {
		t.Fatal("empty resources accepted")
	}
}
