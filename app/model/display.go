// Package model holds the desktop app's rules that need no window: the
// Electron renderer's pure functions, ported with their tests.
package model

import (
	"fmt"
	"strings"
)

// DisplayText shows server-sourced text safely: controls, bidi overrides
// and isolates, zero-width characters and line separators become a
// visible \uXXXX escape, so one name cannot pose as another
// (desktop/src/shared/display.ts).
func DisplayText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if hidden(r) {
			fmt.Fprintf(&b, `\u%04x`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hidden(r rune) bool {
	return r <= 0x1f || (r >= 0x7f && r <= 0x9f) ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) ||
		(r >= 0x200b && r <= 0x200f) || (r >= 0x2060 && r <= 0x2064) || r == 0xfeff ||
		r == 0x2028 || r == 0x2029
}
