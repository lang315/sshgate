//go:build windows

package hub

import (
	"context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func currentUserSID() (string, error) {
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// SocketPath returns the per-user MCP door pipe name.
func SocketPath() (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return `\\.\pipe\sshgate-hub-` + sid, nil
}

func ListenMCPDoor() (net.Listener, error) {
	p, err := SocketPath()
	if err != nil {
		return nil, err
	}
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	// Owner-only DACL. go-winio v0.6.2 creates the first instance with
	// FILE_CREATE disposition (pipe.go:381), the NT equivalent of
	// FILE_FLAG_FIRST_PIPE_INSTANCE, so a second hub fails here instead of
	// sharing the name. Re-verify when upgrading go-winio.
	return winio.ListenPipe(p, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")"})
}

func DialMCPDoor(ctx context.Context) (net.Conn, error) {
	p, err := SocketPath()
	if err != nil {
		return nil, err
	}
	conn, err := winio.DialPipeContext(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := checkSID(conn, procGetNamedPipeServerProcId); err != nil {
		conn.Close()
		return nil, fmt.Errorf("pipe server is not owned by the current user: %w", err)
	}
	return conn, nil
}
