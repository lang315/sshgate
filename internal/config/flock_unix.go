//go:build unix

package config

// ponytail: duplicated from internal/web to avoid a web→config layering issue; consolidate if a third user appears

import (
	"os"
	"syscall"
)

// withFlock serializes writers across processes (e.g. two `ssh-mcp web`
// instances) using an OS advisory lock on a sidecar "<path>.lock" file.
func withFlock(path string, fn func() error) error {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
