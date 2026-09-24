package hub

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// SocketPath returns the per-user MCP door endpoint. On Unix it is a socket
// in a short runtime directory (macOS caps sun_path at 104 bytes, so the
// config dir under ~/Library is unusable). On Windows it is a pipe name.
func SocketPath() (string, error) {
	if runtime.GOOS == "windows" {
		sid, err := currentUserSID()
		if err != nil {
			return "", err
		}
		return `\\.\pipe\ssh-mcp-hub-` + sid, nil
	}
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
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "hub.sock"), nil
}
