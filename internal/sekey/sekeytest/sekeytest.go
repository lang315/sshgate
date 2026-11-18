// Package sekeytest builds the fake Secure Enclave helper for tests.
package sekeytest

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Build compiles internal/sekey/fakehelper into the test's temp dir, which
// the test removes; the Go build cache keeps a rebuild to a quick link.
func Build(t testing.TB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), binName(runtime.GOOS))
	if out, err := exec.Command("go", "build", "-o", path, "github.com/lang315/sshgate/internal/sekey/fakehelper").CombinedOutput(); err != nil {
		t.Fatalf("build fakehelper: %v\n%s", err, out)
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
