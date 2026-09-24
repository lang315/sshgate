//go:build unix

package hub

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// SocketPath returns the per-user MCP door socket in a short runtime
// directory (macOS caps sun_path at 104 bytes, so the config dir under
// ~/Library is unusable). The windows pipe name is in listen_windows.go.
//
// The socket directory must be a real directory owned by us with mode 0700.
// An existing directory is never chmodded or followed through a symlink:
// on the shared /tmp fallback another user could pre-create it.
func SocketPath() (string, error) {
	var base string
	switch runtime.GOOS {
	case "darwin":
		base = os.Getenv("TMPDIR")
	default:
		base = os.Getenv("XDG_RUNTIME_DIR")
	}
	dir := filepath.Join(base, "ssh-mcp")
	if base == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("ssh-mcp-%d", os.Getuid()))
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
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(st.Uid) != os.Getuid() || info.Mode().Perm() != 0o700 {
		return "", fmt.Errorf("socket directory %s is not a private directory owned by you", dir)
	}
	return filepath.Join(dir, "hub.sock"), nil
}
