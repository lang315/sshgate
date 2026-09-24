//go:build unix

package hub

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"time"
)

func ListenMCPDoor() (net.Listener, error) {
	p, err := SocketPath()
	if err != nil {
		return nil, err
	}
	// Never unlink a live hub's socket: only a socket that refuses
	// connections is stale (left by a crashed hub).
	c, err := net.DialTimeout("unix", p, time.Second)
	switch {
	case err == nil:
		c.Close()
		return nil, errors.New("another ssh-mcp hub is already running")
	case errors.Is(err, syscall.ECONNREFUSED):
		if err := os.Remove(p); err != nil {
			return nil, err
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	ln, err := net.Listen("unix", p)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(p, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

func DialMCPDoor(ctx context.Context) (net.Conn, error) {
	p, err := SocketPath()
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	return d.DialContext(ctx, "unix", p)
}

func currentUserSID() (string, error) { return "", nil } // windows only
