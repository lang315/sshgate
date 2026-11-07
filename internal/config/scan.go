package config

import (
	"bytes"
	"errors"
	"io"
	"strings"
)

const (
	scanWindow  = 1 << 20  // bytes read per window
	scanOverlap = 64 << 10 // carried into the next window, so a block across the edge matches
)

// ScanSecrets reports the redaction hits in rd: vault secrets (kind
// "secret") and RedactPatterns kinds. It reads windows of scanWindow bytes,
// each prefixed by the previous window's last scanOverlap bytes, and stops at
// the first window with a hit; the counts are that window's, a lower bound.
// nil: nothing matched.
//
// A window with a NUL byte is binary: the key-value, header and URL heuristics
// read format and UI strings in executables, locale packs and compressed data
// as secrets, so there only vault secrets, PEM blocks and prefixed tokens
// count.
//
// shortcut: NUL padding hides a "password=..." line from the heuristics; the
// human approves every transfer, so upgrade only if that gap is used.
func ScanSecrets(r *Redactor, rd io.Reader) (map[string]int, error) {
	buf := make([]byte, scanOverlap+scanWindow)
	carry := 0
	for {
		n, err := io.ReadFull(rd, buf[carry:carry+scanWindow])
		if n > 0 {
			s, sec := r.RedactCount(string(buf[:carry+n]))
			var c map[string]int
			if bytes.IndexByte(buf[:carry+n], 0) >= 0 {
				c = binaryPatterns(s)
			} else {
				_, c = RedactPatterns(s)
			}
			if sec > 0 {
				if c == nil {
					c = map[string]int{}
				}
				c["secret"] += sec
			}
			if len(c) > 0 {
				return c, nil
			}
			end := carry + n
			carry = min(scanOverlap, end)
			copy(buf, buf[end-carry:end])
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// binaryPatterns counts the high-precision RedactPatterns kinds only.
func binaryPatterns(s string) map[string]int {
	var c map[string]int
	add := func(kind string, n int) {
		if n > 0 {
			if c == nil {
				c = map[string]int{}
			}
			c[kind] += n
		}
	}
	add("private_key", len(pemRe.FindAllStringIndex(s, -1)))
	for _, t := range tokenRe.FindAllString(s, -1) {
		if strings.ContainsAny(t, "0123456789") {
			add("token", 1)
		}
	}
	return c
}
