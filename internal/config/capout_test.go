package config

import (
	"strings"
	"testing"
)

func TestCapOutputPassThrough(t *testing.T) {
	if got := CapOutput("hello", 10); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestCapOutputKeepsHeadAndTail(t *testing.T) {
	s := strings.Repeat("a", 50) + strings.Repeat("b", 50) + strings.Repeat("c", 50)
	got := CapOutput(s, 40)
	if !strings.HasPrefix(got, strings.Repeat("a", 20)) {
		t.Fatalf("head missing: %q", got)
	}
	if !strings.HasSuffix(got, strings.Repeat("c", 20)) {
		t.Fatalf("tail missing: %q", got)
	}
	if !strings.Contains(got, "[truncated 110 bytes]") {
		t.Fatalf("marker wrong: %q", got)
	}
}

func TestDefaultOutputCapIs64K(t *testing.T) {
	if DefaultOutputCap != 65536 {
		t.Fatal("spec says 64 KB")
	}
}
