//go:build unix || windows

package sshconfig

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The test binary doubles as the command runTree runs (SSHCONFIG_HELPER):
// "spawn" starts a "sleep" child, waits for its pid file and exits 0, as a
// Match exec that starts a helper does; "marker" writes a file and exits 0.
func TestMain(m *testing.M) {
	path := os.Getenv("SSHCONFIG_HELPER_PATH")
	switch os.Getenv("SSHCONFIG_HELPER") {
	case "":
		os.Exit(m.Run())
	case "spawn":
		c := helper(context.Background(), "sleep", path)
		if c.Start() != nil {
			os.Exit(2)
		}
		for range 500 {
			if b, _ := os.ReadFile(path); len(b) > 0 {
				os.Exit(0)
			}
			time.Sleep(20 * time.Millisecond)
		}
		os.Exit(3)
	case "sleep":
		if os.WriteFile(path+".tmp", []byte(strconv.Itoa(os.Getpid())), 0o600) != nil || os.Rename(path+".tmp", path) != nil {
			os.Exit(2)
		}
		time.Sleep(time.Minute)
	case "marker":
		os.WriteFile(path, []byte("ran"), 0o600)
	}
	os.Exit(0)
}

func helper(ctx context.Context, mode, path string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "SSHCONFIG_HELPER="+mode, "SSHCONFIG_HELPER_PATH="+path)
	return cmd
}

// Only a timeout ends ssh's process tree: a child left by a command that
// succeeded keeps running, on every platform.
func TestRunTreeKeepsChildrenOnSuccess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	if _, err := runTree(helper(context.Background(), "spawn", pidFile)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(string(b))
	if err != nil || pid <= 0 {
		t.Fatalf("pid file: %q %v", b, err)
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			p.Kill()
		}
	})
	time.Sleep(300 * time.Millisecond) // a kill as runTree returns need not be done yet
	if !alive(pid) {
		t.Fatal("runTree ended a child of a command that succeeded")
	}
}
