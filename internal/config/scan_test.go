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
