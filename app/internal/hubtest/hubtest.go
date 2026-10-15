// Package hubtest builds sshgate and sshtestd from the root module and runs
// sshtestd, for the app's tests.
package hubtest

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

// Options picks what Start has sshtestd write besides serving.
type Options struct {
	EmptyStore bool   // no vault: a hub on Server.Store starts on Create vault
	KnownHosts string // when set, sshtestd writes its host key there as a known_hosts line
}

// Server is a running sshtestd: it accepts any password and any key.
type Server struct {
	Store string // the hub's --store; a ready vault (password Password, server "box", pinned) unless EmptyStore
	Port  int
}

// Start runs sshtestd until the test ends.
func Start(t *testing.T, o Options) Server {
	t.Helper()
	_, bin := Binaries(t)
	store := filepath.Join(t.TempDir(), "servers.json")
	args := []string{"-password=" + Password}
	if !o.EmptyStore {
		args = append(args, "-write-store="+store)
	}
	if o.KnownHosts != "" {
		args = append(args, "-write-known-hosts="+o.KnownHosts)
	}
	cmd := exec.Command(bin, args...)
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
	port, perr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PORT=")))
	if err != nil || !strings.HasPrefix(line, "PORT=") || perr != nil {
		t.Fatalf("sshtestd: %q, %v, %v", line, err, perr)
	}
	return Server{Store: store, Port: port}
}

// SSHD starts sshtestd with a ready vault (Start with no options) and
// returns the vault's path.
func SSHD(t *testing.T) string { return Start(t, Options{}).Store }

// Runtime is Env plus the runtime directory, where the hub puts its MCP
// door socket (MCPSocket).
func Runtime(t *testing.T) ([]string, string) {
	dir := RuntimeDir(t)
	return append(os.Environ(), "SSHGATE_RUNTIME_DIR="+dir), dir
}

// Env is the environment for a hub under test.
func Env(t *testing.T) []string {
	env, _ := Runtime(t)
	return env
}
