package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMasterPasswordFileRejectsLoosePerms(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pw")
	os.WriteFile(p, []byte("secret\n"), 0o644)
	if _, err := ReadMasterPasswordFile(p); err == nil {
		t.Fatal("0644 must be rejected")
	}
	os.Chmod(p, 0o600)
	got, err := ReadMasterPasswordFile(p)
	if err != nil || got != "secret" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestRedactor(t *testing.T) {
	r := NewRedactor("hunter2", "", "root#pw")
	out := r.Redact("login hunter2 then root#pw done")
	if out != "login *** then *** done" {
		t.Fatalf("got %q", out)
	}
}
