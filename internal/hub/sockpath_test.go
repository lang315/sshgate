//go:build unix

package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketPathIsShortAndPerUser(t *testing.T) {
	isolateDoor(t)
	p, err := SocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if len(p) > 100 {
		t.Fatalf("path too long for sun_path: %d %q", len(p), p)
	}
	if !strings.HasSuffix(p, "/ssh-mcp/hub.sock") && !strings.Contains(p, "/ssh-mcp-") {
		t.Fatalf("unexpected path %q", p)
	}
	info, err := os.Stat(strings.TrimSuffix(p, "/hub.sock"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("parent dir must exist with 0700: %v %v", err, info)
	}
}

func TestListenMCPDoorSocketIsOwnerOnly(t *testing.T) {
	isolateDoor(t)
	ln, err := ListenMCPDoor()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	p, _ := SocketPath()
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket must be 0600: %v %v", err, info)
	}
}

func TestSocketPathRejectsLooseDir(t *testing.T) {
	dir := filepath.Join(isolateDoor(t), "ssh-mcp")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil { // defeat umask
		t.Fatal(err)
	}
	if _, err := SocketPath(); err == nil || !strings.Contains(err.Error(), "not a private directory owned by you") {
		t.Fatalf("want private-dir error, got %v", err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o755 {
		t.Fatalf("existing dir was chmodded to %v", info.Mode().Perm())
	}
}

func TestSocketPathRejectsSymlinkDir(t *testing.T) {
	base := isolateDoor(t)
	target := filepath.Join(base, "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(base, "ssh-mcp")); err != nil {
		t.Fatal(err)
	}
	if _, err := SocketPath(); err == nil || !strings.Contains(err.Error(), "not a private directory owned by you") {
		t.Fatalf("want private-dir error, got %v", err)
	}
}
