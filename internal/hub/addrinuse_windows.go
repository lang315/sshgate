//go:build windows

package hub

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Windows reports a busy port as WSAEADDRINUSE, which syscall.EADDRINUSE does not match.
func addrInUse(err error) bool { return errors.Is(err, windows.WSAEADDRINUSE) }
