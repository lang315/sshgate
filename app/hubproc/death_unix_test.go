//go:build unix

package hubproc

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/internal/hubtest"
)

// TestHubExitsWhenAppDies: when the app process dies, the hub sees EOF on
// stdin and exits, so the master key dies with it.
func TestHubExitsWhenAppDies(t *testing.T) {
	if os.Getenv("HUBPROC_CHILD") == "1" {
		p, err := Start(context.Background(), Launch{Command: os.Getenv("HUBPROC_BIN"), Args: []string{"hub", "--store=" + os.Getenv("HUBPROC_STORE")}, Env: os.Environ()}, nil)
		if err != nil {
			os.Stdout.WriteString("ERR " + err.Error() + "\n")
			os.Exit(1)
		}
		os.Stdout.WriteString("PID " + strconv.Itoa(p.Pid()) + "\n")
		time.Sleep(time.Minute)
		return
	}
	store := hubtest.SSHD(t)
	bin, _ := hubtest.Binaries(t)
	child := exec.Command(os.Args[0], "-test.run=^TestHubExitsWhenAppDies$")
	child.Env = append(hubtest.Env(t), "HUBPROC_CHILD=1", "HUBPROC_BIN="+bin, "HUBPROC_STORE="+store)
	out, _ := child.StdoutPipe()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, _ := out.Read(buf)
	line := strings.TrimSpace(string(buf[:n]))
	pid, err := strconv.Atoi(strings.TrimPrefix(line, "PID "))
	if err != nil {
		t.Fatalf("child said %q", line)
	}
	child.Process.Kill() // SIGKILL: no cleanup runs in the app
	child.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("hub still running 5 s after the app died")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
