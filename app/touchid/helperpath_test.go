package touchid

import (
	"path/filepath"
	"testing"
)

// The helper is the knob when unpackaged, else sshgate-touchid in Resources
// (packaged) or beside the module root, on macOS only; a packaged app ignores
// the knob (test/touchid.test.ts, touchIdHelper).
func TestHelperPath(t *testing.T) {
	has := func(paths ...string) func(string) bool {
		return func(p string) bool {
			for _, x := range paths {
				if x == p {
					return true
				}
			}
			return false
		}
	}
	res, repo := filepath.Join("/res", "sshgate-touchid"), filepath.Join("/repo", "sshgate-touchid")
	knob := []string{"A=b", "SSHGATE_TOUCHID_HELPER=/tmp/fake"}
	for _, tc := range []struct {
		name     string
		env      []string
		packaged bool
		goos     string
		exists   func(string) bool
		want     string
	}{
		{"the knob, unpackaged", knob, false, "linux", has(), "/tmp/fake"},
		{"the knob, unpackaged, on macOS", knob, false, "darwin", has(repo), "/tmp/fake"},
		{"the knob is ignored when packaged", knob, true, "darwin", has(res), res},
		{"an empty knob is none", []string{"SSHGATE_TOUCHID_HELPER="}, false, "darwin", has(repo), repo},
		{"beside the module root, unpackaged", nil, false, "darwin", has(repo), repo},
		{"in Resources, packaged", nil, true, "darwin", has(res), res},
		{"a missing helper", nil, true, "darwin", has(), ""},
		{"not macOS", nil, true, "linux", has(res), ""},
		{"not macOS, unpackaged", nil, false, "windows", has(repo), ""},
	} {
		if got := HelperPath(tc.env, tc.packaged, "/res", "/repo", tc.goos, tc.exists); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
