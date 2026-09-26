//go:build unix

package files

import (
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/pkg/sftp"
)

// isFatal: errors that end a job instead of skipping one file.
func isFatal(err error) bool {
	return err != nil && (errors.Is(err, sftp.ErrSSHFxConnectionLost) || errors.Is(err, sftp.ErrSSHFxNoConnection) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT))
}
