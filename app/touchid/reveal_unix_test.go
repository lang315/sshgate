//go:build unix

package touchid

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestRevealReturnsValue(t *testing.T) {
	r := newRig(t)
	r.enable(t)
	n := len(r.hub.unlocked()) // Enable checked the password once
	v, err := r.m.Reveal(context.Background(), "enc", "password")
	if err != nil || v != "enc/password" {
		t.Fatalf("Reveal = %q, %v", v, err)
	}
	if got := r.hub.unlocked()[n:]; len(got) != 1 || got[0] != "pw" {
		t.Errorf("the hub got %q, want the enrolled password", got)
	}
	if b, _ := os.ReadFile(r.mode + ".verb"); string(b) != "reveal" {
		t.Errorf("helper verb = %q, want reveal", b)
	}
}

func TestRevealOutcomes(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		want      error
		keepsFile bool
	}{
		{"cancel", ErrCancelled, true},
		{"invalid", ErrReset, false},
		{"unavailable", ErrUnavailable, true},
		{"fail", ErrFailed, true},
	} {
		r := newRig(t)
		r.enable(t)
		r.setMode(t, tc.mode)
		n := len(r.hub.unlocked())
		if v, err := r.m.Reveal(context.Background(), "enc", "password"); !errors.Is(err, tc.want) || v != "" || r.fileExists() != tc.keepsFile {
			t.Errorf("%s: %q, %v, file %v", tc.mode, v, err, r.fileExists())
		}
		if len(r.hub.unlocked()) != n {
			t.Errorf("%s: the hub was called", tc.mode)
		}
	}
}

// The master password changed since Touch ID was turned on: the sealed one
// is now wrong, so the file goes, as at unlock.
func TestRevealStaleDisables(t *testing.T) {
	r := newRig(t)
	r.enable(t)
	r.hub.password = "new"
	if _, err := r.m.Reveal(context.Background(), "enc", "password"); !errors.Is(err, ErrReset) {
		t.Fatalf("err = %v, want ErrReset", err)
	}
	if r.fileExists() {
		t.Error("touchid.json is still there")
	}
}

func TestRevealBusy(t *testing.T) {
	r := newRig(t)
	r.enable(t)
	r.setMode(t, "slow")
	done := make(chan error, 1)
	go func() { _, err := r.m.Reveal(context.Background(), "enc", "password"); done <- err }()
	until(t, "the helper to start", func() bool { b, _ := os.ReadFile(r.mode + ".pid"); return len(b) > 0 })
	if _, err := r.m.Reveal(context.Background(), "enc", "password"); !errors.Is(err, ErrBusy) {
		t.Errorf("a second reveal: %v", err)
	}
	r.m.Cancel(nil)
	<-done
}
