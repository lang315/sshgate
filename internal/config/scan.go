package config

import (
	"errors"
	"io"
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
func ScanSecrets(r *Redactor, rd io.Reader) (map[string]int, error) {
	buf := make([]byte, scanOverlap+scanWindow)
	carry := 0
	for {
		n, err := io.ReadFull(rd, buf[carry:carry+scanWindow])
		if n > 0 {
			s, sec := r.RedactCount(string(buf[:carry+n]))
			_, c := RedactPatterns(s)
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
