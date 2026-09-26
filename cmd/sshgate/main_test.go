package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/config"
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
	t.Setenv("SSHGATE_RUNTIME_DIR", t.TempDir())
	store := t.TempDir() + "/sub/servers.json"
	err := runHub([]string{"--insecureIgnoreHostKey", "--store=" + store})
	if err == nil || !strings.Contains(err.Error(), "--insecureIgnoreHostKey is not supported by hub") {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Stat(filepath.Dir(store)); !os.IsNotExist(statErr) {
		t.Fatal("hub touched the store before refusing the flag")
	}
}

func TestParseIdleLock(t *testing.T) {
	for _, c := range []struct {
		args []string
		want time.Duration
		ok   bool
	}{
		{nil, 0, true},
		{[]string{"--idleLock=3s"}, 3 * time.Second, true},
		{[]string{"--idleLock=1m30s"}, 90 * time.Second, true},
		{[]string{"--idleLock=1s"}, time.Second, true},
		{[]string{"--idleLock=999ms"}, 0, false},
		{[]string{"--idleLock=0s"}, 0, false},
		{[]string{"--idleLock=-5s"}, 0, false},
		{[]string{"--idleLock=3"}, 0, false},
		{[]string{"--idleLock=soon"}, 0, false},
		{[]string{"--idleLock="}, 0, false},
		{[]string{"--idleLock"}, 0, false},
	} {
		got, err := parseIdleLock(config.ParseArgv(c.args))
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%v: got %v, %v", c.args, got, err)
		}
	}
}

// An invalid --idleLock fails at startup, before the store is touched.
func TestHubRejectsBadIdleLock(t *testing.T) {
	t.Setenv("SSHGATE_RUNTIME_DIR", t.TempDir())
	store := t.TempDir() + "/sub/servers.json"
	err := runHub([]string{"--idleLock=500ms", "--store=" + store})
	if err == nil || !strings.Contains(err.Error(), "--idleLock") {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Stat(filepath.Dir(store)); !os.IsNotExist(statErr) {
		t.Fatal("hub touched the store before refusing the flag")
	}
}
