package config

import "testing"

func TestRedactor(t *testing.T) {
	r := NewRedactor("hunter2", "", "root#pw")
	out := r.Redact("login hunter2 then root#pw done")
	if out != "login *** then *** done" {
		t.Fatalf("got %q", out)
	}
}

func TestRedactorCount(t *testing.T) {
	r := NewRedactor("hunter2", "root#pw")
	out, n := r.RedactCount("hunter2 hunter2 root#pw none")
	if out != "*** *** *** none" || n != 3 {
		t.Fatalf("got %q %d", out, n)
	}
	if _, n := NewRedactor().RedactCount("hunter2"); n != 0 {
		t.Fatalf("empty redactor counted %d", n)
	}
}
