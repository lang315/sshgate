package config

import (
	"bytes"
	"runtime"
	"testing"

	"golang.org/x/crypto/argon2"
)

// The hub keeps running after an unlock, so Argon2's 64 MiB work area must go
// back to the OS instead of sitting in the Go heap as idle, unreleased memory.
func TestArgon2KeyReleasesWorkArea(t *testing.T) {
	if testing.Short() {
		t.Skip("20 Argon2id derives at 64 MiB")
	}
	pw, salt := []byte("master password"), []byte("0123456789abcdef")
	want := argon2.IDKey(pw, salt, 3, 65536, 4, 32)
	procs := runtime.GOMAXPROCS(0)
	for i := 0; i < 20; i++ {
		got := argon2Key(pw, salt, 3, 65536, 4, 32)
		if !bytes.Equal(got, want) {
			t.Fatalf("run %d: key differs from argon2.IDKey", i)
		}
		if got := runtime.GOMAXPROCS(0); got != procs {
			t.Fatalf("run %d: GOMAXPROCS %d, was %d", i, got, procs)
		}
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		if held := m.HeapIdle - m.HeapReleased; held >= 8<<20 {
			t.Fatalf("run %d: %d MiB idle heap not returned to the OS", i, held>>20)
		}
	}
}
