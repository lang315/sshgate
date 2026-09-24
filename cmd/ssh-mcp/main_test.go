package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoute(t *testing.T) {
	cases := []struct {
		in   []string
		mode string
	}{
		{[]string{"web", "--port", "9000"}, "web"},
		{[]string{"hub", "--cli"}, "hub"},
		{[]string{"hub"}, "hub"},
		{[]string{"--host=1.2.3.4", "--user=root"}, "mcp"},
		{[]string{}, "mcp"},
	}
	for _, c := range cases {
		got, _ := route(c.in)
		if got != c.mode {
			t.Fatalf("route(%v) = %q, want %q", c.in, got, c.mode)
		}
	}
}

// I5: the hub gates AI execs on pinned host keys; it must not offer a flag
// that silently turns that off. It refuses before touching the store.
func TestHubRejectsInsecureIgnoreHostKey(t *testing.T) {
	// If the refusal regressed, keep the hub away from the real socket.
	t.Setenv("SSH_MCP_RUNTIME_DIR", t.TempDir())
	store := t.TempDir() + "/sub/servers.json"
	err := runHub([]string{"--insecureIgnoreHostKey", "--store=" + store})
	if err == nil || !strings.Contains(err.Error(), "--insecureIgnoreHostKey is not supported by hub") {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Stat(filepath.Dir(store)); !os.IsNotExist(statErr) {
		t.Fatal("hub touched the store before refusing the flag")
	}
}
