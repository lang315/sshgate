package config

import (
	"bytes"
	"errors"
	"io"
	"regexp"
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
// A window whose newly read bytes hold a NUL is binary: the key-value, header
// and URL heuristics read format and UI strings in executables, locale packs
// and compressed data as secrets, so there only vault secrets, PEM blocks with
// a body and an END line, and prefixed tokens count. NUL padding can therefore
// hide a "password=..." line, on auto-allow runs too; accepted, since a grant
// already lets exec print any remote file in an encoding redaction misses.
func ScanSecrets(r *Redactor, rd io.Reader) (map[string]int, error) {
	buf := make([]byte, scanOverlap+scanWindow)
	carry := 0
	for {
		n, err := io.ReadFull(rd, buf[carry:carry+scanWindow])
		if n > 0 {
			s, sec := r.RedactCount(string(buf[:carry+n]))
			var c map[string]int
			if bytes.IndexByte(buf[carry:carry+n], 0) >= 0 {
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

// binPemRe is a PEM private key with a base64 body and an END line; the bare
// header text that OpenSSH and TLS tools carry as a string does not match.
var binPemRe = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY(?: BLOCK)?-----[A-Za-z0-9+/=:,\s-]{16,}?-----END [A-Z ]*PRIVATE KEY(?: BLOCK)?-----`)

// binaryPatterns counts the high-precision RedactPatterns kinds only.
func binaryPatterns(s string) map[string]int {
	var c map[string]int
	add := func(kind string) {
		if c == nil {
			c = map[string]int{}
		}
		c[kind]++
	}
	for range binPemRe.FindAllStringIndex(s, -1) {
		add("private_key")
	}
	for _, t := range tokenRe.FindAllString(s, -1) {
		// sk- also starts OpenSSH key type names (sk-ssh-ed25519-cert-v01@openssh.com)
		if realToken(t) && !strings.HasPrefix(t, "sk-") {
			add("token")
		}
	}
	return c
}
