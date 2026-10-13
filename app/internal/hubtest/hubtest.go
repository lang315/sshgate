// Package hubtest builds sshgate and sshtestd from the root module and runs
// sshtestd, for the app's tests.
package hubtest

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Password is the master password of the vault SSHD writes; its server is "box".
const Password = "pw"

var (
	once          sync.Once
	sshgate, sshd string
	buildErr      error
)

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0) // app/internal/hubtest/hubtest.go
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// Binaries builds sshgate and sshtestd once per test binary.
func Binaries(t *testing.T) (string, string) {
	t.Helper()
	once.Do(func() {
		dir, err := os.MkdirTemp("", "sgbin")
		if err != nil {
			buildErr = err
			return
		}
		exe := ""
		if runtime.GOOS == "windows" {
			exe = ".exe"
		}
		sshgate, sshd = filepath.Join(dir, "sshgate"+exe), filepath.Join(dir, "sshtestd"+exe)
		for _, b := range [][2]string{{sshgate, "./cmd/sshgate"}, {sshd, "./internal/sshx/sshtest/sshtestd"}} {
			cmd := exec.Command("go", "build", "-o", b[0], b[1])
			cmd.Dir = repoRoot()
			if out, err := cmd.CombinedOutput(); err != nil {
				buildErr = &buildError{string(out), err}
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return sshgate, sshd
}

type buildError struct {
	out string
	err error
}

func (e *buildError) Error() string { return e.err.Error() + ": " + e.out }

// RuntimeDir is a short private directory for SSHGATE_RUNTIME_DIR: a Unix
// socket path must stay under 104 bytes, which t.TempDir() can exceed.
func RuntimeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sg")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// SSHD starts sshtestd, which writes a ready vault (password Password,
// server "box", host key pinned), and returns the vault's path.
func SSHD(t *testing.T) string {
	t.Helper()
	_, bin := Binaries(t)
	store := filepath.Join(t.TempDir(), "servers.json")
	cmd := exec.Command(bin, "-write-store="+store, "-password="+Password)
	stdin, err := cmd.StdinPipe() // sshtestd exits when its stdin closes
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "PORT=") {
		t.Fatalf("sshtestd: %q, %v", line, err)
	}
	return store
}

// Env is the environment for a hub under test.
func Env(t *testing.T) []string {
	return append(os.Environ(), "SSHGATE_RUNTIME_DIR="+RuntimeDir(t))
}
