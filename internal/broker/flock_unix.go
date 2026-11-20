//go:build unix

package broker

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on f, so the hub and `sshgate
// inbound` (which appends login records) do not interleave a size check and a write.
func lockFile(f *os.File) (unlock func(), err error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }, nil
}
