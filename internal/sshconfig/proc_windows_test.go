//go:build windows

package sshconfig

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
}

// A cancel that lands after Start but before ssh is in its job must still
// stop it: the suspended process is never resumed.
func TestRunTreeCancelBeforeJob(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	ctx, cancel := context.WithCancel(context.Background())
	afterStart = func() {
		cancel()
		time.Sleep(200 * time.Millisecond) // exec calls cmd.Cancel on the still-empty job
	}
	t.Cleanup(func() { afterStart = func() {} })
	runTree(helper(ctx, "marker", marker))
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran after its context was cancelled")
	}
}
