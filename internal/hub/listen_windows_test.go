//go:build windows

package hub

import (
	"strings"
	"testing"
)

// SSHGATE_RUNTIME_DIR gives each value its own pipe, as it gives each its
// own socket on Unix, so tests (and their packages) never share a hub.
func TestSocketPathFollowsRuntimeDir(t *testing.T) {
	t.Setenv("SSHGATE_RUNTIME_DIR", "")
	base, err := SocketPath()
	if err != nil || !strings.HasPrefix(base, `\\.\pipe\sshgate-hub-S-`) || strings.Count(base, "-") < 3 {
		t.Fatalf("base %q %v", base, err)
	}
	t.Setenv("SSHGATE_RUNTIME_DIR", `C:\a`)
	a, _ := SocketPath()
	t.Setenv("SSHGATE_RUNTIME_DIR", `C:\b`)
	b, _ := SocketPath()
	if a == base || b == base || a == b || !strings.HasPrefix(a, base+"-") {
		t.Fatalf("base %q a %q b %q", base, a, b)
	}
}
