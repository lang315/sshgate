//go:build windows

package sshconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// afterStart runs between Start and entering the job; tests use it to land a cancel there.
var afterStart = func() {}

// runTree is cmd.Output with ssh and every process it starts (a Match exec
// child) in one job object, so a timeout terminates them all, as a process
// group kill does on Unix. ssh starts suspended and resumes only once it is
// in the job, so no child can start outside it. As on Unix, children still
// running when ssh exits on its own are left alone.
func runTree(cmd *exec.Cmd) ([]byte, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(job)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	cmd.Cancel = func() error {
		err := windows.TerminateJobObject(job, 1)
		cmd.Process.Kill() // a cancel before ssh is in the job: it never resumes
		return err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	afterStart()
	var jerr error
	if err := cmd.Process.WithHandle(func(h uintptr) { jerr = enterJob(job, windows.Handle(h)) }); err != nil {
		jerr = err
	}
	if jerr != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, jerr
	}
	err = cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		ee.Stderr = stderr.Bytes() // as cmd.Output does, for Resolve's error text
	}
	return stdout.Bytes(), err
}

// enterJob puts the suspended process h in job, then resumes it.
// NtResumeProcess is undocumented but has been in ntdll since NT; it is the
// one call that resumes a whole process without walking its threads.
func enterJob(job, h windows.Handle) error {
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		return err
	}
	if st, _, _ := ntResumeProcess.Call(uintptr(h)); int32(st) < 0 { // NT_SUCCESS
		return fmt.Errorf("NtResumeProcess: NTSTATUS %#x", uint32(st))
	}
	return nil
}
