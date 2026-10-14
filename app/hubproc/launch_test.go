package hubproc

import (
	"path/filepath"
	"slices"
	"testing"
)

func has(paths ...string) func(string) bool {
	return func(p string) bool { return slices.Contains(paths, p) }
}

func TestLaunchUnpackagedUsesKnobs(t *testing.T) {
	env := []string{"HOME=/h", "SSHGATE_STORE=/s.json", "SSHGATE_SSH_CONFIG=/c", "SSHGATE_IDLE_LOCK=3s", "SSHGATE_RUNTIME_DIR=/r"}
	l := LaunchFor(env, false, "", "/repo", "darwin", has(filepath.FromSlash("/repo/sshgate")))
	if l.Command != filepath.FromSlash("/repo/sshgate") {
		t.Errorf("command %q", l.Command)
	}
	if want := []string{"hub", "--store=/s.json", "--sshConfig=/c", "--idleLock=3s"}; !slices.Equal(l.Args, want) {
		t.Errorf("args %q", l.Args)
	}
	if !slices.Contains(l.Env, "SSHGATE_RUNTIME_DIR=/r") {
		t.Errorf("env lost SSHGATE_RUNTIME_DIR: %q", l.Env)
	}
}

func TestLaunchBinKnob(t *testing.T) {
	l := LaunchFor([]string{"SSHGATE_BIN=/x/sshgate"}, false, "", "/repo", "linux", has("/repo/sshgate"))
	if l.Command != "/x/sshgate" {
		t.Errorf("command %q", l.Command)
	}
}

func TestLaunchPackagedIgnoresAndStripsKnobs(t *testing.T) {
	env := []string{"HOME=/h", "SSHGATE_BIN=/evil", "SSHGATE_STORE=/other.json", "SSHGATE_RUNTIME_DIR=/r"}
	l := LaunchFor(env, true, "/App.app/Contents/Resources", "/repo", "darwin", has(filepath.FromSlash("/App.app/Contents/Resources/sshgate")))
	if l.Command != filepath.FromSlash("/App.app/Contents/Resources/sshgate") {
		t.Errorf("command %q", l.Command)
	}
	if !slices.Equal(l.Args, []string{"hub"}) {
		t.Errorf("args %q", l.Args)
	}
	if !slices.Equal(l.Env, []string{"HOME=/h"}) {
		t.Errorf("env %q", l.Env)
	}
}

func TestLaunchFallsBackToPath(t *testing.T) {
	l := LaunchFor(nil, false, "", "/repo", "windows", has())
	if l.Command != "sshgate.exe" {
		t.Errorf("command %q", l.Command)
	}
}

func TestLaunchPackagedOnlyKnobsEnvIsEmptyNotNil(t *testing.T) {
	l := LaunchFor([]string{"SSHGATE_STORE=/s", "SSHGATE_BIN=/evil"}, true, "/R", "/repo", "linux", has())
	if l.Env == nil || len(l.Env) != 0 {
		t.Errorf("env %#v, want non-nil empty", l.Env)
	}
}
