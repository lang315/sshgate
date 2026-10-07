package config

import (
	"maps"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
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
	if !maps.Equal(counts, map[string]int{"password": 1, "private_key": 1}) {
		t.Errorf("counts = %v", counts)
	}
	marker := truncMarker(len(in) - capMax)
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
	if got != want || !maps.Equal(gc, wc) {
		t.Errorf("got %q %v, want %q %v", got, gc, want, wc)
	}
}

// BEGIN before the tail window, END inside the kept tail (last max/2 bytes):
// the window starts mid-body, and no body line may survive in the result.
func TestRedactCapPEMStraddlesTailWindow(t *testing.T) {
	const line = "MIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun\n"
	// END sits 10 KiB from the end; the body (~91 KiB) starts before the
	// 96 KiB window.
	in := filler(3<<20) + "-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat(line, 1400) +
		"-----END RSA PRIVATE KEY-----\n" + filler(10<<10)
	if !strings.Contains(CapOutput(in, capMax), line[:24]) {
		t.Fatal("setup: plain CapOutput should keep body lines")
	}
	out, _ := RedactCap(NewRedactor(), in, capMax)
	if strings.Contains(out, line[:24]) {
		t.Errorf("PEM body leaked")
	}
}

// pemBlocks is n fake ~4 KiB private keys; masked, each shrinks to a tag.
func pemBlocks(n int) string {
	b := "-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("MIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun\n", 60) + "-----END RSA PRIVATE KEY-----\n"
	return strings.Repeat(b, n)
}

// When masking shrinks a window below max/2 the whole masked window is kept,
// so a line the window boundary cuts through must not be in it.
func TestRedactCapWindowCutLeaks(t *testing.T) {
	w := capMax/2 + redactMargin
	for _, c := range []struct {
		line, leak string
		keep       int
	}{
		{"DATABASE_URL=postgres://user:Zq7Lm2Kp9Xw4@h/db\n", "Zq7Lm2", 6 + len("DATABASE_URL=postgres://user:")},
		{"GH=ghp_Ab3dE6gH9jK2mN5pQ8sT1vW4yZ7bC0eF3hJ6\n", "ghp_Ab3dE6gH", 15},
	} {
		// head: the window boundary falls c.keep bytes into the line.
		blocks := pemBlocks((w - c.keep - 10) / len(pemBlocks(1)))
		pad := strings.Repeat("x", w-c.keep-len(blocks)-1) + "\n"
		in := blocks + pad + c.line + filler(150<<10)
		if u, _ := RedactPatterns(in[:w]); !strings.Contains(u, c.leak) {
			t.Fatalf("setup: unaligned head window does not leak %q", c.leak)
		}
		out, _ := RedactCap(NewRedactor(), in, capMax)
		if strings.Contains(out, c.leak) {
			t.Errorf("head cut leaked %q", c.leak)
		}
	}
	for _, c := range []struct {
		line, leak string
		rem        int
	}{
		{"DB_PASSWORD=Wm4tQz8vLp2Rk7Xs\n", "Wm4tQz8vLp2Rk7Xs", len("ORD=Wm4tQz8vLp2Rk7Xs\n")},
		{"> Authorization: Bearer 9f8e7d6c5b4a39281706f5e4d3c2b1a0\n", "5b4a39281706f5e4d3c2b1a0", 26},
	} {
		// tail: the window starts c.rem bytes before the line's end.
		blocks := pemBlocks((w - c.rem - 10) / len(pemBlocks(1)))
		pad := strings.Repeat("x", w-c.rem-len(blocks)-1) + "\n"
		in := filler(150<<10) + c.line + pad + blocks
		if u, _ := RedactPatterns(in[len(in)-w:]); !strings.Contains(u, c.leak) {
			t.Fatalf("setup: unaligned tail window does not leak %q", c.leak)
		}
		out, _ := RedactCap(NewRedactor(), in, capMax)
		if strings.Contains(out, c.leak) {
			t.Errorf("tail cut leaked %q", c.leak)
		}
	}
}

// A window edge that falls inside a line of at least redactMargin bytes keeps
// the byte-boundary window, so long-line output still returns max/2 per half.
func TestRedactCapLongLines(t *testing.T) {
	long := strings.Repeat("a", 1<<20) + "\n"
	for name, in := range map[string]string{
		"one line":        long,
		"short then line": "HTTP/1.1 200 OK\n" + long,
	} {
		out, _ := RedactCap(NewRedactor(), in, capMax)
		head, tail, ok := strings.Cut(out, truncMarker(len(in)-capMax))
		if !ok || len(head) != capMax/2 || len(tail) != capMax/2 {
			t.Errorf("%s: head %d tail %d, want %d each", name, len(head), len(tail), capMax/2)
		}
	}
}

// A7: a lone END is masked from the start only in a tail window whose head the
// cap cut off, never on whole output or in the head window.
func TestRedactCapOrphanEnd(t *testing.T) {
	const end = "-----END RSA PRIVATE KEY-----\n"
	whole := "src/a.go:1: first\n" + end + "src/b.go:2: last\n"
	if out, c := RedactCap(NewRedactor(), whole, capMax); out != whole || c != nil {
		t.Errorf("whole output changed: %q %v", out, c)
	}
	if out, c := redactPatterns(whole, true); !strings.HasPrefix(out, "[REDACTED:private_key]\n") || c["private_key"] != 1 {
		t.Errorf("tail window: %q %v", out, c)
	}
	// Head window of a windowed capture: an END near the start stays.
	in := "head-line-kept\n" + end + filler(3<<20)
	if out, _ := RedactCap(NewRedactor(), in, capMax); !strings.HasPrefix(out, "head-line-kept\n"+end) {
		t.Errorf("head window masked a lone END: %.80q", out)
	}
}

var wholeMarker = regexp.MustCompile(`\[REDACTED:[a-z_]+\]`)

// A8: the clamps never leave a piece of a marker or a split rune.
func TestRedactCapCutIsMarkerAndRuneSafe(t *testing.T) {
	const line = "DB_PASSWORD=Wm4tQz8vLp2Rk7Xs é\n" // masked: 33 bytes, one 2-byte rune
	step := 1                                       // 33 pads cover every byte phase of the 33-byte line
	if raceOn {
		step = 8
	}
	for pad := 0; pad <= 32; pad += step {
		for _, in := range []string{
			strings.Repeat(line, 12000), // windowed
			strings.Repeat(line, 1500),  // short path, masked output over max
		} {
			in = strings.Repeat("é", pad) + in
			out, _ := RedactCap(NewRedactor(), in, capMax)
			if !utf8.ValidString(out) {
				t.Fatalf("pad %d: invalid UTF-8 at the cut", pad)
			}
			if rest := wholeMarker.ReplaceAllString(out, ""); strings.Contains(rest, "REDACTED") || strings.Contains(rest, "password]") || strings.Contains(rest, "ACTED:") {
				t.Fatalf("pad %d: split marker in output", pad)
			}
			if len(out) > capMax+len(truncMarker(len(in))) {
				t.Fatalf("pad %d: output %d bytes exceeds max", pad, len(out))
			}
		}
	}
}

// A vault secret is masked as "***" and counted as kind "secret", so the AI
// learns the output was masked and does not write it back whole.
func TestRedactCapCountsVaultSecrets(t *testing.T) {
	out, c := RedactCap(NewRedactor("s3cr3t-pw"), "DB_PASSWORD=s3cr3t-pw\nAPI=s3cr3t-pw\n", capMax)
	if out != "DB_PASSWORD=***\nAPI=***\n" || !maps.Equal(c, map[string]int{"secret": 2}) {
		t.Fatalf("got %q %v", out, c)
	}
	// A cut output still counts the masks in the part the cap drops.
	big := "x=s3cr3t-pw\n" + filler(1<<20) + "y=s3cr3t-pw\n" + filler(1<<20) + "z=s3cr3t-pw\n"
	if _, c := RedactCap(NewRedactor("s3cr3t-pw"), big, capMax); c["secret"] != 3 {
		t.Fatalf("big: counts %v", c)
	}
}
