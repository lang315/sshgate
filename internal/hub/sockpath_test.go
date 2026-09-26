//go:build unix

package hub

import (
	"os"
	"path/filepath"
	"strconv"
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
	if !strings.HasSuffix(p, "/sshgate/hub.sock") && !strings.Contains(p, "/sshgate-") {
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
	dir := filepath.Join(isolateDoor(t), "sshgate")
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
	if err := os.Symlink(target, filepath.Join(base, "sshgate")); err != nil {
		t.Fatal(err)
	}
	if _, err := SocketPath(); err == nil || !strings.Contains(err.Error(), "not a private directory owned by you") {
		t.Fatalf("want private-dir error, got %v", err)
	}
}

// I4 / R37: MCP clients may not pass TMPDIR or XDG_RUNTIME_DIR to the
// bridge, so neither may change where the socket is.
func TestSocketPathIgnoresTMPDIRAndXDG(t *testing.T) {
	dir := isolateDoor(t)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	p, err := SocketPath()
	if err != nil || p != filepath.Join(dir, "sshgate", "hub.sock") {
		t.Fatalf("got %q, %v", p, err)
	}

	runUser := isolateDoor(t) // stands in for /run/user
	tmp := isolateDoor(t)     // stands in for /tmp
	t.Setenv("SSHGATE_RUNTIME_DIR", "")
	oldRun, oldTmp := runUserBase, tmpBase
	runUserBase, tmpBase = runUser, tmp
	t.Cleanup(func() { runUserBase, tmpBase = oldRun, oldTmp })

	uid := strconv.Itoa(os.Getuid())
	if p, err := SocketPath(); err != nil || p != filepath.Join(tmp, "sshgate-"+uid, "hub.sock") {
		t.Fatalf("no /run/user/<uid>: got %q, %v", p, err)
	}
	if err := os.Mkdir(filepath.Join(runUser, uid), 0o700); err != nil {
		t.Fatal(err)
	}
	if p, err := SocketPath(); err != nil || p != filepath.Join(runUser, uid, "sshgate", "hub.sock") {
		t.Fatalf("with /run/user/<uid>: got %q, %v", p, err)
	}
}
