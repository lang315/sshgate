//go:build unix

package hub

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// runUserBase and tmpBase are variables only so tests can redirect them.
var runUserBase, tmpBase = "/run/user", "/tmp"

// SocketPath returns the per-user MCP door socket in a short runtime
// directory (macOS caps sun_path at 104 bytes, so the config dir under
// ~/Library is unusable). The windows pipe name is in listen_windows.go.
//
// R37: MCP clients may start the bridge without TMPDIR or XDG_RUNTIME_DIR,
// so neither is consulted. The directory is $SSH_MCP_RUNTIME_DIR/ssh-mcp if
// that is set, else /run/user/<uid>/ssh-mcp when /run/user/<uid> is a real
// directory owned by us (Linux), else /tmp/ssh-mcp-<uid>.
//
// The socket directory must be a real directory owned by us with mode 0700.
// An existing directory is never chmodded or followed through a symlink:
// on the shared /tmp fallback another user could pre-create it.
func SocketPath() (string, error) {
	uid := os.Getuid()
	var dir string
	runUser := filepath.Join(runUserBase, strconv.Itoa(uid))
	switch {
	case os.Getenv("SSH_MCP_RUNTIME_DIR") != "":
		dir = filepath.Join(os.Getenv("SSH_MCP_RUNTIME_DIR"), "ssh-mcp")
	case ownedDir(runUser, uid):
		dir = filepath.Join(runUser, "ssh-mcp")
	default:
		dir = filepath.Join(tmpBase, fmt.Sprintf("ssh-mcp-%d", uid))
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !ownedDir(dir, uid) || info.Mode().Perm() != 0o700 {
		return "", fmt.Errorf("socket directory %s is not a private directory owned by you", dir)
	}
	return filepath.Join(dir, "hub.sock"), nil
}

// ownedDir reports whether p is a real directory (not a symlink) owned by uid.
func ownedDir(p string, uid int) bool {
	info, err := os.Lstat(p)
	if err != nil || !info.IsDir() {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid
}
