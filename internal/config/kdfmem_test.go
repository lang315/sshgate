package config

import (
	"bytes"
	"runtime"
	"testing"
	"time"

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

// Overlapping derives with different parallelism must not leave a stale
// GOMAXPROCS behind (A lowers 8 to 4, B lowers 4 to 2, A restores 8, B
// restores 4).
func TestArgon2KeyConcurrentRestoresGOMAXPROCS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the GOMAXPROCS cap in argon2Key is darwin-only")
	}
	// Small CI runners have 3-4 CPUs; 8 makes the cap engage for both derives.
	const before = 8
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(before))
	pw, salt := []byte("master password"), []byte("0123456789abcdef")
	done := make(chan struct{}, 2)
	go func() { argon2Key(pw, salt, 3, 32768, 4, 32); done <- struct{}{} }()
	time.Sleep(5 * time.Millisecond)
	go func() { argon2Key(pw, salt, 3, 65536, 2, 32); done <- struct{}{} }()
	<-done
	<-done
	if after := runtime.GOMAXPROCS(0); after != before {
		t.Fatalf("GOMAXPROCS %d after overlapping derives, was %d", after, before)
	}
}
