package config

import (
	"fmt"
	"strings"
)

func hasControl(s string) bool {
	return strings.ContainsAny(s, "\n\r\x00")
}

func SanitizeCommand(cmd string, maxChars int) (string, error) {
	t := strings.TrimSpace(cmd)
	if t == "" {
		return "", fmt.Errorf("Command cannot be empty")
	}
	if hasControl(t) {
		return "", fmt.Errorf("Command must not contain newline or NUL")
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
	if hasControl(desc) {
		return "", fmt.Errorf("description must not contain newline or NUL")
	}
	if len(desc) > 500 {
		return "", fmt.Errorf("description too long (max 500)")
	}
	return cmd + " # " + strings.ReplaceAll(desc, "#", `\#`), nil
}
