package sshx

import (
	"bufio"
	"strings"
	"testing"
	"time"
)

func TestReadCommandOutputParsesExitCodeAndOutput(t *testing.T) {
	n := "SSHMCPabc123"
	stream := "hello\nworld\n" + n + ":7\n"
	out, code, err := readCommandOutput(bufio.NewReader(strings.NewReader(stream)), time.Second, n)
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Fatalf("code=%d want 7", code)
	}
	if out != "hello\nworld\n" {
		t.Fatalf("out=%q", out)
	}
}

func TestReadCommandOutputIgnoresNonceMidLine(t *testing.T) {
	n := "SSHMCPabc123"
	// simulated echoed command line contains the nonce mid-line and must NOT
	// terminate the read early; only the real "<nonce>:0" line at line-start does.
	stream := "id -u; echo " + n + ":$?\n0\n" + n + ":0\n"
	out, code, err := readCommandOutput(bufio.NewReader(strings.NewReader(stream)), time.Second, n)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("code=%d want 0", code)
	}
	if lastNonEmptyLine(out) != "0" {
		t.Fatalf("lastNonEmptyLine(out)=%q want 0", lastNonEmptyLine(out))
	}
}

func TestReadCommandOutputNoMarkerReturnsError(t *testing.T) {
	n := "SSHMCPabc123"
	if _, _, err := readCommandOutput(bufio.NewReader(strings.NewReader("no marker here\n")), 200*time.Millisecond, n); err == nil {
		t.Fatal("missing marker must return an error, not a false success")
	}
}

func TestLastNonEmptyLine(t *testing.T) {
	if lastNonEmptyLine("a\nb\n\n") != "b" {
		t.Fatalf("got %q", lastNonEmptyLine("a\nb\n\n"))
	}
	if lastNonEmptyLine("") != "" {
		t.Fatal("empty")
	}
}
