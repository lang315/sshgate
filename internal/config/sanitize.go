package config

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// forbiddenRune returns the first rune that must not appear in a command or
// description, with its byte position. Controls (including \t) can change
// what a shell or terminal does with the text; format runes (bidi
// overrides, zero-width characters) can make the approval UI display a
// different command than the one that runs.
func forbiddenRune(s string) (rune, int, bool) { return forbiddenRuneExcept(s, "") }

// forbiddenRuneExcept is forbiddenRune with the runes in allow exempt.
func forbiddenRuneExcept(s, allow string) (rune, int, bool) {
	for i, r := range s {
		if (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) && !strings.ContainsRune(allow, r) {
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

// ValidateDescription applies the command's rune rules plus a 500-byte cap.
func ValidateDescription(desc string) error {
	if r, pos, bad := forbiddenRune(desc); bad {
		return fmt.Errorf("description contains forbidden character U+%04X at position %d", r, pos)
	}
	if len(desc) > 500 {
		return fmt.Errorf("description too long (max 500)")
	}
	return nil
}

// AppendDescription is for --host mode only. The hub never runs the
// description: a trailing backslash or open quote would make it code.
func AppendDescription(cmd, desc string) (string, error) {
	if desc == "" {
		return cmd, nil
	}
	if err := ValidateDescription(desc); err != nil {
		return "", err
	}
	return cmd + " # " + strings.ReplaceAll(desc, "#", `\#`), nil
}

// MaxStdin bounds the stdin of one exec: it is shown in full to the
// approver and stored in full in the audit record.
const MaxStdin = 256 << 10

var (
	ErrStdinSudo = errors.New("stdin is not supported with sudo-exec")
	ErrStdinSu   = errors.New("stdin is not supported on this server")
)

// stdinAllowed are the controls and format runes that ordinary file
// content needs: line breaks and tabs, and the joiners and byte-order mark
// of emoji, Persian or Hindi text and BOM files. Bidi controls and the
// other zero-width runes stay forbidden.
const stdinAllowed = "\n\t\r\u200c\u200d\ufeff"

// ValidateStdin applies the command's rune rules to an exec's stdin, except
// for stdinAllowed: stdin is file content. It also refuses a "[REDACTED:"
// marker, which only appears in masked output: writing that back would
// corrupt the file.
func ValidateStdin(s string) error {
	if len(s) > MaxStdin {
		return fmt.Errorf("stdin too long (max %d bytes)", MaxStdin)
	}
	if r, pos, bad := forbiddenRuneExcept(s, stdinAllowed); bad {
		return fmt.Errorf("stdin contains forbidden character U+%04X at position %d", r, pos)
	}
	if strings.Contains(s, "[REDACTED:") {
		return errors.New("stdin contains a [REDACTED:…] marker: it is masked output, and writing it back would corrupt the file")
	}
	return nil
}
