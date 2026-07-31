package config

import "strings"
import "testing"

func TestSanitizeCommand(t *testing.T) {
	if _, err := SanitizeCommand("   ", 1000); err == nil {
		t.Fatal("empty should error")
	}
	if _, err := SanitizeCommand("ls\nrm -rf /", 1000); err == nil {
		t.Fatal("newline should error")
	}
	got, err := SanitizeCommand("  ls -la  ", 1000)
	if err != nil || got != "ls -la" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := SanitizeCommand("abcdef", 3); err == nil {
		t.Fatal("too long should error")
	}
	if _, err := SanitizeCommand(strings.Repeat("x", 5000), -1); err != nil {
		t.Fatal("unlimited should pass")
	}
}

func TestEscapeShellSingleQuote(t *testing.T) {
	if EscapeShellSingleQuote("a'b") != `a'\''b` {
		t.Fatalf("got %q", EscapeShellSingleQuote("a'b"))
	}
}

func TestAppendDescription(t *testing.T) {
	got, err := AppendDescription("ls", "list # files")
	if err != nil {
		t.Fatal(err)
	}
	if got != `ls # list \# files` {
		t.Fatalf("got %q", got)
	}
	if _, err := AppendDescription("ls", "bad\ndesc"); err == nil {
		t.Fatal("newline in desc should error")
	}
}
