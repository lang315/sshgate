//go:build unix

package sshconfig

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// killGroup makes a timeout kill ssh's whole process group, so a hung
// Match exec child dies with it.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return os.ErrProcessDone
	}
}
