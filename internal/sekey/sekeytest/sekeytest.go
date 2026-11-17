// Package sekeytest builds the fake Secure Enclave helper for tests.
package sekeytest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	once sync.Once
	path string
	err  error
	dir  string
)

// Build compiles internal/sekey/fakehelper once per test binary and returns its path.
func Build(t testing.TB) string {
	t.Helper()
	once.Do(func() {
		if dir, err = os.MkdirTemp("", "sekey-fake"); err != nil {
			return
		}
		path = filepath.Join(dir, binName(runtime.GOOS))
		out, e := exec.Command("go", "build", "-o", path, "github.com/lang315/sshgate/internal/sekey/fakehelper").CombinedOutput()
		if e != nil {
			err = &buildErr{e, string(out)}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// binName is the helper's file name: Windows runs only files with an extension from PATHEXT.
func binName(goos string) string {
	if goos == "windows" {
		return "fakehelper.exe"
	}
	return "fakehelper"
}

type buildErr struct {
	err error
	out string
}

func (b *buildErr) Error() string { return "build fakehelper: " + b.err.Error() + "\n" + b.out }
