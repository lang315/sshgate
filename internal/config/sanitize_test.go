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

func TestSanitizeCommandRejectsControlAndFormatRunes(t *testing.T) {
	cases := []struct {
		name, in string
	}{
		{"esc", "ls\x1b[2Jrm -rf /"},
		{"tab", "ls\t-la"},
		{"bidi override", "echo ‮gnp.txt"},
		{"zero width space", "rm​ -rf /"},
		{"c1 control", "ls\u0085rm"},
		{"nul", "ls\x00rm"},
	}
	for _, c := range cases {
		if _, err := SanitizeCommand(c.in, 1000); err == nil {
			t.Errorf("%s: expected error for %q", c.name, c.in)
		} else if !strings.Contains(err.Error(), "forbidden character U+") {
			t.Errorf("%s: error should name the rune, got %v", c.name, err)
		}
	}
	// Ordinary non-ASCII text is allowed.
	if _, err := SanitizeCommand("echo 'xin chào'", 1000); err != nil {
		t.Fatalf("vietnamese should pass: %v", err)
	}
}

func TestAppendDescriptionRejectsControlRunes(t *testing.T) {
	if _, err := AppendDescription("ls", "safe\x1bdesc"); err == nil {
		t.Fatal("ESC in description should error")
	}
	if _, err := AppendDescription("ls", "tab\tdesc"); err == nil {
		t.Fatal("tab in description should error")
	}
}
