//go:build unix

package config

// Only Update calls withFlock; every store writer goes through Update.

import (
	"os"
	"syscall"
)

// withFlock serializes writers across processes (e.g. two `sshgate web`
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
