//go:build unix

package hub

import (
	"os"
	"syscall"
)

// ownedByUs reports whether the file belongs to the running user.
func ownedByUs(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return !ok || int(st.Uid) == os.Getuid()
}
