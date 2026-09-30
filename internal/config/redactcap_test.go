package config

import (
	"fmt"
	"strings"
	"testing"
)

const capMax = 64 << 10

func filler(n int) string { return strings.Repeat("lorem ipsum dolor sit amet\n", n/27+1)[:n] }

func TestRedactCapBigOutput(t *testing.T) {
	const pw, bearer, body = "Wm4tQz8vLp2Rk7Xs", "9f8e7d6c5b4a39281706f5e4d3c2b1a0", "MIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun"
	head := "DB_PASSWORD=" + pw + "\n"
	mid := "Authorization: Bearer " + bearer + "\n"
	tail := "-----BEGIN RSA PRIVATE KEY-----\n" + body + "\n-----END RSA PRIVATE KEY-----\n"
	in := head + filler(1<<20) + mid + filler(1<<20) + tail
	out, counts := RedactCap(NewRedactor(), in, capMax)
	if !strings.Contains(out, "DB_PASSWORD=[REDACTED:password]") || !strings.Contains(out, "[REDACTED:private_key]") {
		t.Errorf("head or tail secret not masked in kept parts")
	}
	for _, s := range []string{pw, bearer, body} {
		if strings.Contains(out, s) {
			t.Errorf("%q leaked", s)
		}
	}
	if counts["password"] != 1 || counts["private_key"] != 1 || counts["auth_header"] != 0 {
		t.Errorf("counts = %v", counts)
	}
	marker := fmt.Sprintf("\n… [truncated %d bytes] …\n", len(in)-capMax)
	if !strings.Contains(out, marker) {
		t.Errorf("marker %q missing", marker)
	}
	if len(out) != capMax+len(marker) {
		t.Errorf("len = %d, want %d", len(out), capMax+len(marker))
	}
}

func TestRedactCapShortIsRedactPatterns(t *testing.T) {
	in := "a\nDB_PASSWORD=Wm4tQz8vLp2Rk7Xs\n"
	want, wc := RedactPatterns(in)
	got, gc := RedactCap(NewRedactor(), in, capMax)
	if got != want || fmt.Sprint(gc) != fmt.Sprint(wc) {
		t.Errorf("got %q %v, want %q %v", got, gc, want, wc)
	}
}

// BEGIN before the tail window, END inside it: the window starts mid-body and
// no body line may survive in the kept tail.
func TestRedactCapPEMStraddlesTailWindow(t *testing.T) {
	line := "MIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun\n"
	end := "-----END RSA PRIVATE KEY-----\n"
	w := capMax/2 + redactMargin
	// 20 body lines, the window starting ~10 lines before END.
	in := filler(3<<20) + "-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat(line, 10) +
		strings.Repeat(line, 10) + end + filler(w-len(end)-10*len(line)-13)
	out, _ := RedactCap(NewRedactor(), in, capMax)
	if strings.Contains(out, "MIIEowIBAAKCAQEAu1SU1LfV") {
		t.Errorf("PEM body leaked")
	}
}
