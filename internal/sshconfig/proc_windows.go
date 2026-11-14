//go:build windows

package sshconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// runTree is cmd.Output with ssh and every process it starts (a Match exec
// child) in one job object: a timeout terminates the job, and closing the
// job's handle on return ends anything still in it, as a process group
// kill does on Unix. ssh starts suspended and resumes only once it is in
// the job, so no child can start outside it.
func runTree(cmd *exec.Cmd) ([]byte, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(job)
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	cmd.Cancel = func() error { return windows.TerminateJobObject(job, 1) }
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if err := enterJob(job, cmd.Process.Pid); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, err
	}
	err = cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		ee.Stderr = stderr.Bytes() // as cmd.Output does, for Resolve's error text
	}
	return stdout.Bytes(), err
}

// enterJob puts the suspended process pid in job, then resumes it.
func enterJob(job windows.Handle, pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		return err
	}
	if st, _, _ := ntResumeProcess.Call(uintptr(h)); st != 0 {
		return fmt.Errorf("NtResumeProcess: NTSTATUS %#x", st)
	}
	return nil
}
