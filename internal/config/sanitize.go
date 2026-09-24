package config

import (
	"fmt"
	"strings"
	"unicode"
)

// forbiddenRune returns the first rune that must not appear in a command or
// description, with its byte position. Controls (including \t) can change
// what a shell or terminal does with the text; format runes (bidi
// overrides, zero-width characters) can make the approval UI display a
// different command than the one that runs.
func forbiddenRune(s string) (rune, int, bool) {
	for i, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return r, i, true
		}
	}
	return 0, 0, false
}

func SanitizeCommand(cmd string, maxChars int) (string, error) {
	t := strings.TrimSpace(cmd)
	if t == "" {
		return "", fmt.Errorf("Command cannot be empty")
	}
	if r, pos, bad := forbiddenRune(t); bad {
		return "", fmt.Errorf("command contains forbidden character U+%04X at position %d", r, pos)
	}
	if maxChars >= 0 && len(t) > maxChars {
		return "", fmt.Errorf("Command is too long (max %d characters)", maxChars)
	}
	return t, nil
}

func EscapeShellSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

func AppendDescription(cmd, desc string) (string, error) {
	if desc == "" {
		return cmd, nil
	}
	if r, pos, bad := forbiddenRune(desc); bad {
		return "", fmt.Errorf("description contains forbidden character U+%04X at position %d", r, pos)
	}
	if len(desc) > 500 {
		return "", fmt.Errorf("description too long (max 500)")
	}
	return cmd + " # " + strings.ReplaceAll(desc, "#", `\#`), nil
}
