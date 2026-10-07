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

func TestValidateStdin(t *testing.T) {
	ok := []string{"", "a\nb\n", "col1\tcol2\n", "crlf\r\n", "xin chào\n", strings.Repeat("x", MaxStdin),
		"dev \U0001F468\u200d\U0001F4BB\n", "a\u200cb\n", "\ufeffpackage main\n"} // ZWJ, ZWNJ, BOM: ordinary file content
	for _, s := range ok {
		if err := ValidateStdin(s); err != nil {
			t.Errorf("%q: %v", s[:min(len(s), 20)], err)
		}
	}
	bad := []struct{ name, in, want string }{
		{"esc", "a\x1b[2Jb", "forbidden character U+001B"},
		{"nul", "a\x00b", "forbidden character U+0000"},
		{"bidi override", "echo ‮gnp.txt", "forbidden character U+202E"},
		{"zero width space", "rm​ -rf", "forbidden character U+200B"},
		{"c1 control", "a\u0085b", "forbidden character U+0085"},
		{"too long", strings.Repeat("x", MaxStdin+1), "stdin too long"},
		{"masked output", "PASS=[REDACTED:password]\n", "[REDACTED:"},
	}
	for _, c := range bad {
		if err := ValidateStdin(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", c.name, err, c.want)
		}
	}
}
