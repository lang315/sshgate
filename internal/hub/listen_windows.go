//go:build windows

package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

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

// SocketPath returns the per-user MCP door pipe name. With
// SSHGATE_RUNTIME_DIR set (tests; the packaged app strips it) a hash of it
// is appended, so each value gets its own pipe, as on Unix.
func SocketPath() (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	p := `\\.\pipe\sshgate-hub-` + sid
	if dir := os.Getenv("SSHGATE_RUNTIME_DIR"); dir != "" {
		sum := sha256.Sum256([]byte(dir))
		p += "-" + hex.EncodeToString(sum[:8])
	}
	return p, nil
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
	ln, err := winio.ListenPipe(p, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")"})
	if err != nil {
		// The name is taken (Access is denied); say so only when our own
		// hub answers there, as Unix does.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if c, derr := DialMCPDoor(ctx); derr == nil {
			c.Close()
			return nil, errors.New("another sshgate hub is already running")
		}
		return nil, err
	}
	return ln, nil
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
