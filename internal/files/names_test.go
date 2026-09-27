package files

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestRemoteNameRules(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "nul\x00", "\xff\xfe"} {
		if checkRemoteName(bad) == nil {
			t.Errorf("remote name %q accepted", bad)
		}
	}
	for _, ok := range []string{"a", ".env", "a b", "ünï", "x..y", "-rf"} {
		if err := checkRemoteName(ok); err != nil {
			t.Errorf("remote name %q refused: %v", ok, err)
		}
	}
}

func TestLocalNameRules(t *testing.T) {
	if checkLocalName("..") == nil || checkLocalName("a/b") == nil {
		t.Fatal("local rules must include the remote rules")
	}
	if runtime.GOOS == "windows" {
		for _, bad := range []string{"CON", "a:b", "x.", "x "} {
			if checkLocalName(bad) == nil {
				t.Errorf("windows name %q accepted", bad)
			}
		}
	}
	if err := checkLocalName("report.pdf"); err != nil {
		t.Fatal(err)
	}
}

func TestPathRules(t *testing.T) {
	for _, bad := range []string{"", "rel", "/a/../b", "/a/", "//a"} {
		if CheckAbs(bad) == nil {
			t.Errorf("path %q accepted", bad)
		}
	}
	if err := CheckAbs("/srv/app"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"/", "/home/u"} {
		if checkMutable(bad, "/home/u") == nil {
			t.Errorf("mutation of %q allowed", bad)
		}
	}
	if err := checkMutable("/home/u/x", "/home/u"); err != nil {
		t.Fatal(err)
	}
}

func TestModes(t *testing.T) {
	if got := uploadMode(fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky | 0o777); got != 0o777 {
		t.Fatalf("upload %o", got)
	}
	if got := downloadMode(0o777); got != 0o755 {
		t.Fatalf("download %o", got)
	}
	if got := downloadMode(0o600); got != 0o600 {
		t.Fatalf("download %o", got)
	}
}

func TestErrorsKeepFirstTwenty(t *testing.T) {
	var e Errors
	for i := range 25 {
		e.Add(fmt.Sprintf("/f%d", i), errors.New("boom"))
	}
	if e.Count != 25 || len(e.List) != MaxErrors || e.List[0] != "/f0: boom" {
		t.Fatalf("%+v", e)
	}
}

func TestFatal(t *testing.T) {
	if !isFatal(syscall.ENOSPC) || isFatal(fs.ErrPermission) || isFatal(nil) {
		t.Fatal("fatal classification")
	}
}

func TestPartName(t *testing.T) {
	a, b := partName("x.txt"), partName("x.txt")
	if a == b || !strings.HasPrefix(a, ".x.txt.") || !strings.HasSuffix(a, ".sshgate-part") || checkRemoteName(a) != nil {
		t.Fatalf("%q %q", a, b)
	}
}
