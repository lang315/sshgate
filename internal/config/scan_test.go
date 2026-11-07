package config

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"testing/iotest"
)

func TestScanSecretsClean(t *testing.T) {
	got, err := ScanSecrets(NewRedactor("hunter2x"), strings.NewReader("just text\n"))
	if err != nil || got != nil {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestScanSecretsVaultSecretAndPattern(t *testing.T) {
	got, err := ScanSecrets(NewRedactor("s3cr3tVal"), strings.NewReader("a s3cr3tVal b\npassword=hunter2x\n"))
	if err != nil || got["secret"] != 1 || got["password"] != 1 {
		t.Fatalf("got %v %v", got, err)
	}
}

// A PEM block across the 1 MiB window edge still matches: the overlap
// carries the start of the block into the next window.
func TestScanSecretsAcrossWindowEdge(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun\n-----END RSA PRIVATE KEY-----\n"
	var b bytes.Buffer
	b.WriteString(strings.Repeat("x", scanWindow-20))
	b.WriteString(pem)
	got, err := ScanSecrets(NewRedactor(), iotest.HalfReader(&b))
	if err != nil || got["private_key"] != 1 {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestScanSecretsReadError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := ScanSecrets(NewRedactor(), iotest.ErrReader(boom)); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

// Chromium-style binary bytes: format and UI strings that the text heuristics
// read as secrets must not refuse a download.
func TestScanSecretsBinaryIgnoresHeuristics(t *testing.T) {
	bin := "\x00VkRenderPass = %p)\n\x00Enrollment Token:Device ID\x00cookie:\xd0\x97\xd0\xb0\x00"
	got, err := ScanSecrets(NewRedactor("s3cr3tVal"), strings.NewReader(bin))
	if err != nil || got != nil {
		t.Fatalf("got %v %v", got, err)
	}
}

// A binary window still reports exact vault secrets, PEM blocks and
// prefixed tokens.
func TestScanSecretsBinaryKeepsPrecise(t *testing.T) {
	cases := map[string]string{
		"secret":      "\x00 s3cr3tVal \x00",
		"private_key": "\x00-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0t\n-----END RSA PRIVATE KEY-----\x00",
		"token":       "\x00ghp_0123456789abcdefghij0123456789abcdef\x00",
	}
	for kind, in := range cases {
		got, err := ScanSecrets(NewRedactor("s3cr3tVal"), strings.NewReader(in))
		if err != nil || got[kind] != 1 {
			t.Fatalf("%s: got %v %v", kind, got, err)
		}
	}
}
