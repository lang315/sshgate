//go:build windows

package hub

import (
	"fmt"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	procGetNamedPipeServerProcId = kernel32.NewProc("GetNamedPipeServerProcessId")
	procGetNamedPipeClientProcId = kernel32.NewProc("GetNamedPipeClientProcessId")
)

// pipePeerPID asks Windows for the pid at the other end of the pipe. go-winio
// pipe conns embed *win32File, which has Fd() returning the pipe handle.
func pipePeerPID(conn net.Conn, proc *windows.LazyProc) (uint32, error) {
	f, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return 0, fmt.Errorf("pipe conn has no Fd")
	}
	var pid uint32
	r, _, e := proc.Call(f.Fd(), uintptr(unsafe.Pointer(&pid)))
	if r == 0 {
		return 0, e
	}
	return pid, nil
}

func pidUserSID(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return "", err
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// checkSID fails unless the process at the other end of the pipe (found via
// proc) runs as the current user.
func checkSID(conn net.Conn, proc *windows.LazyProc) error {
	pid, err := pipePeerPID(conn, proc)
	if err != nil {
		return err
	}
	theirs, err := pidUserSID(pid)
	if err != nil {
		return err
	}
	mine, err := currentUserSID()
	if err != nil {
		return err
	}
	if theirs != mine {
		return fmt.Errorf("peer SID %s is not %s", theirs, mine)
	}
	return nil
}

func checkPeer(conn net.Conn) error { return checkSID(conn, procGetNamedPipeClientProcId) }
