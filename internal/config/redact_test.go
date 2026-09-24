package config

import "testing"

func TestRedactor(t *testing.T) {
	r := NewRedactor("hunter2", "", "root#pw")
	out := r.Redact("login hunter2 then root#pw done")
	if out != "login *** then *** done" {
		t.Fatalf("got %q", out)
	}
}
