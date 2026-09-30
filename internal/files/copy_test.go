package files

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"

	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

func noParts(t *testing.T, dir string) {
	t.Helper()
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".sshgate-part") {
			t.Errorf("part file left: %s", p)
		}
		return nil
	})
}

func TestDownloadRoundTrip(t *testing.T) {
	c, _, root := remote(t)
	src := filepath.Join(root, "home", "src")
	write(t, filepath.Join(src, "a.txt"), "alpha", 0o777|fs.ModeSetuid)
	write(t, filepath.Join(src, "ro", "b.txt"), "beta", 0o600)
	os.Chmod(filepath.Join(src, "ro"), 0o555)
	t.Cleanup(func() { os.Chmod(filepath.Join(src, "ro"), 0o755) })
	mt := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	os.Chtimes(filepath.Join(src, "a.txt"), mt, mt)
	dest := t.TempDir()
	p, err := PlanDownload(context.Background(), c, []string{"/home/src"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	var last int64
	r := p.Run(context.Background(), c, false, func(_ string, done, total int64) { last = done })
	p.Close()
	if r.Copied != 2 || r.Errors.Count != 0 || r.Bytes != 9 || last != 9 {
		t.Fatalf("%+v last %d", r, last)
	}
	a := filepath.Join(dest, "src", "a.txt")
	if read(t, a) != "alpha" {
		t.Fatal("content")
	}
	if fi, _ := os.Stat(a); fi.Mode().Perm() != 0o755 || fi.Mode()&fs.ModeSetuid != 0 || !fi.ModTime().Equal(mt) {
		t.Fatalf("a.txt mode %v mtime %v", fi.Mode(), fi.ModTime())
	}
	if fi, _ := os.Stat(filepath.Join(dest, "src", "ro")); fi.Mode().Perm() != 0o555 {
		t.Fatalf("ro folder mode %v", fi.Mode())
	}
	os.Chmod(filepath.Join(dest, "src", "ro"), 0o755)
	noParts(t, dest)
}

func TestUploadRoundTrip(t *testing.T) {
	c, _, root := remote(t)
	src := filepath.Join(t.TempDir(), "proj")
	write(t, filepath.Join(src, "x.sh"), "#!/bin/sh", 0o775)
	write(t, filepath.Join(src, "sub", "y"), "yy", 0o640)
	p, err := PlanUpload(context.Background(), c, []string{src}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	r := p.Run(context.Background(), c, false, nil)
	p.Close()
	if r.Copied != 2 || r.Errors.Count != 0 {
		t.Fatalf("%+v", r)
	}
	x := filepath.Join(root, "home", "proj", "x.sh")
	if read(t, x) != "#!/bin/sh" {
		t.Fatal("content")
	}
	if fi, _ := os.Stat(x); fi.Mode().Perm() != 0o775 {
		t.Fatalf("mode %v", fi.Mode())
	}
	noParts(t, filepath.Join(root, "home"))
}

func TestConflictsSkipOverwriteAndLateArrival(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "d", "same"), "new", 0o644)
	write(t, filepath.Join(root, "home", "d", "late"), "new", 0o644)
	dest := t.TempDir()
	write(t, filepath.Join(dest, "d", "same"), "old", 0o644)

	p, _ := PlanDownload(context.Background(), c, []string{"/home/d"}, dest)
	write(t, filepath.Join(dest, "d", "late"), "arrived after the plan", 0o644)
	r := p.Run(context.Background(), c, false, nil) // skip
	p.Close()
	if r.Skipped != 1 || r.Errors.Count != 1 || !strings.Contains(r.Errors.List[0], "late") {
		t.Fatalf("%+v", r)
	}
	if read(t, filepath.Join(dest, "d", "same")) != "old" || read(t, filepath.Join(dest, "d", "late")) != "arrived after the plan" {
		t.Fatal("skip replaced a file")
	}

	p, _ = PlanDownload(context.Background(), c, []string{"/home/d"}, dest)
	r = p.Run(context.Background(), c, true, nil) // overwrite
	p.Close()
	if r.Copied != 2 || read(t, filepath.Join(dest, "d", "same")) != "new" {
		t.Fatalf("%+v", r)
	}
	noParts(t, dest)
}

func TestDownloadNeverWritesThroughASymlink(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "d", "f"), "evil", 0o644)
	dest := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	write(t, outside, "safe", 0o644)
	write(t, filepath.Join(dest, "d", "f"), "old", 0o644)
	p, _ := PlanDownload(context.Background(), c, []string{"/home/d"}, dest)
	os.Remove(filepath.Join(dest, "d", "f"))
	os.Symlink(outside, filepath.Join(dest, "d", "f")) // swapped after the plan
	r := p.Run(context.Background(), c, true, nil)
	p.Close()
	if r.Copied != 0 || r.Errors.Count != 1 || read(t, outside) != "safe" {
		t.Fatalf("%+v, victim %q", r, read(t, outside))
	}
	noParts(t, dest)
}

func TestDownloadHostileStaysInside(t *testing.T) {
	c, s, _ := remote(t)
	s.HostileSFTP("x/..", `a\b`, "A", "a", "ok")
	dest := filepath.Join(t.TempDir(), "in")
	os.Mkdir(dest, 0o755)
	p, err := PlanDownload(context.Background(), c, []string{"/hostile"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	r := p.Run(context.Background(), c, false, nil)
	p.Close()
	if r.Copied != 2 {
		t.Fatalf("%+v", r)
	}
	if read(t, filepath.Join(dest, "hostile", "ok")) != "x" { // the 1 TiB size was a lie
		t.Fatal("content")
	}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	if len(entries) != 1 {
		t.Fatalf("something was written beside the picked folder: %v", entries)
	}
}

func TestCancelLeavesNoPartFile(t *testing.T) {
	c, s, root := remote(t)
	write(t, filepath.Join(root, "home", "big"), strings.Repeat("z", 1<<20), 0o644)
	dest := t.TempDir()
	p, _ := PlanDownload(context.Background(), c, []string{"/home/big"}, dest)
	gate := make(chan struct{})
	s.GateSFTP(gate)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result)
	go func() { done <- p.Run(ctx, c, false, nil) }()
	gate <- struct{}{} // one chunk through
	cancel()
	close(gate)
	r := <-done
	p.Close()
	if !r.Cancelled || r.Copied != 0 {
		t.Fatalf("%+v", r)
	}
	noParts(t, dest)
	if _, err := os.Stat(filepath.Join(dest, "big")); err == nil {
		t.Fatal("a cancelled download left the file")
	}
}

// TestRunErrorsNeverNameALocalPath: a local write failure inside download must
// report the bare cause, never the destination's absolute local path (the
// renderer only ever sees Result.Errors, and it never names a local path).
func TestRunErrorsNeverNameALocalPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only folder does not block writes into it on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes everywhere")
	}
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "src", "a.txt"), "aa", 0o644)
	dest := t.TempDir()
	os.MkdirAll(filepath.Join(dest, "src"), 0o755)
	p, err := PlanDownload(context.Background(), c, []string{"/home/src"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(filepath.Join(dest, "src"), 0o555) // read-only after the plan: writes inside it fail
	t.Cleanup(func() { os.Chmod(filepath.Join(dest, "src"), 0o755) })
	r := p.Run(context.Background(), c, false, nil)
	p.Close()
	if r.Errors.Count != 1 || r.Errors.List[0] != "src/a.txt: permission denied" {
		t.Fatalf("%+v", r.Errors)
	}
	if strings.Contains(r.Errors.List[0], dest) {
		t.Fatalf("leaked a local path: %q", r.Errors.List[0])
	}
	noParts(t, dest)
}

func TestDeleteNeverFollowsLinks(t *testing.T) {
	c, _, root := remote(t)
	home := filepath.Join(root, "home")
	write(t, filepath.Join(home, "keep", "precious"), "p", 0o644)
	write(t, filepath.Join(home, "tree", "a"), "a", 0o644)
	os.Symlink("../keep", filepath.Join(home, "tree", "to-keep"))
	os.Symlink("keep", filepath.Join(home, "direct"))
	p, err := PlanDelete(context.Background(), c, []string{"/home/tree", "/home/direct"})
	if err != nil {
		t.Fatal(err)
	}
	r := p.Run(context.Background(), c, false, nil)
	p.Close()
	if r.Deleted != 4 || r.Errors.Count != 0 {
		t.Fatalf("%+v", r)
	}
	if read(t, filepath.Join(home, "keep", "precious")) != "p" {
		t.Fatal("deleted through a symlink")
	}
	for _, gone := range []string{"tree", "direct"} {
		if _, err := os.Lstat(filepath.Join(home, gone)); err == nil {
			t.Fatalf("%s still there", gone)
		}
	}
}

func TestUploadOverwriteReplacesViaPosixRename(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "d", "same"), "old", 0o644)
	src := t.TempDir()
	write(t, filepath.Join(src, "same"), "new", 0o644)
	p, err := PlanUpload(context.Background(), c, []string{filepath.Join(src, "same")}, "/home/d")
	if err != nil {
		t.Fatal(err)
	}
	r := p.Run(context.Background(), c, true, nil) // overwrite
	p.Close()
	if r.Copied != 1 || r.Errors.Count != 0 {
		t.Fatalf("%+v", r)
	}
	if read(t, filepath.Join(root, "home", "d", "same")) != "new" {
		t.Fatal("content not replaced")
	}
	noParts(t, filepath.Join(root, "home"))
}

func TestUploadNeverWritesThroughARemoteSymlink(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "victim"), "safe", 0o644)
	write(t, filepath.Join(root, "home", "d", "f"), "old", 0o644) // conflict at plan time
	src := t.TempDir()
	write(t, filepath.Join(src, "d", "f"), "evil", 0o644)
	p, err := PlanUpload(context.Background(), c, []string{filepath.Join(src, "d")}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(root, "home", "d", "f"))
	os.Symlink("../victim", filepath.Join(root, "home", "d", "f")) // swapped after the plan
	r := p.Run(context.Background(), c, true, nil)
	p.Close()
	if r.Copied != 0 || r.Errors.Count != 1 {
		t.Fatalf("%+v", r)
	}
	if read(t, filepath.Join(root, "home", "victim")) != "safe" {
		t.Fatal("wrote through the symlink")
	}
	noParts(t, filepath.Join(root, "home"))
}

func TestUploadLateArrivalNotReplaced(t *testing.T) {
	c, _, root := remote(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "late"), "new content", 0o644)
	p, err := PlanUpload(context.Background(), c, []string{filepath.Join(src, "late")}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "home", "late"), "arrived after the plan", 0o644)
	r := p.Run(context.Background(), c, false, nil) // skip
	p.Close()
	if r.Errors.Count != 1 || !strings.Contains(r.Errors.List[0], "appeared") {
		t.Fatalf("%+v", r)
	}
	if read(t, filepath.Join(root, "home", "late")) != "arrived after the plan" {
		t.Fatal("the late arrival was replaced")
	}
	noParts(t, filepath.Join(root, "home"))
}

// TestUploadNoPosixRenameOverwriteIsPerFileError sets sftpExtensions (a
// package global) to drop posix-rename@openssh.com from the server's
// advertised extensions, so it must not run in parallel with anything else
// that dials this package's test servers.
func TestUploadNoPosixRenameOverwriteIsPerFileError(t *testing.T) {
	if err := sftp.SetSFTPExtensions("hardlink@openssh.com", "statvfs@openssh.com"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sftp.SetSFTPExtensions("hardlink@openssh.com", "posix-rename@openssh.com", "statvfs@openssh.com")
	})
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "same"), "old", 0o644)
	src := t.TempDir()
	write(t, filepath.Join(src, "same"), "new", 0o644)
	write(t, filepath.Join(src, "clean"), "new2", 0o644)
	p, err := PlanUpload(context.Background(), c, []string{filepath.Join(src, "same"), filepath.Join(src, "clean")}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	var touched []string
	r := p.Run(context.Background(), c, true, func(file string, _, _ int64) { touched = append(touched, file) })
	p.Close()
	if r.Copied != 1 || r.Errors.Count != 1 || !strings.Contains(r.Errors.List[0], "posix-rename") {
		t.Fatalf("%+v", r)
	}
	for _, f := range touched {
		if f == "same" {
			t.Fatal("the conflicting file was sent even though it could never be replaced")
		}
	}
	if read(t, filepath.Join(root, "home", "same")) != "old" {
		t.Fatal("the conflicting file was replaced")
	}
	if read(t, filepath.Join(root, "home", "clean")) != "new2" {
		t.Fatal("the non-conflicting file did not copy")
	}
	noParts(t, filepath.Join(root, "home"))
}

func TestUploadKeepsMtimeAndDropsSetuid(t *testing.T) {
	c, _, root := remote(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "a"), "aa", 0o777|fs.ModeSetuid)
	mt := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	os.Chtimes(filepath.Join(src, "a"), mt, mt)
	p, err := PlanUpload(context.Background(), c, []string{filepath.Join(src, "a")}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	r := p.Run(context.Background(), c, false, nil)
	p.Close()
	if r.Copied != 1 || r.Errors.Count != 0 {
		t.Fatalf("%+v", r)
	}
	a := filepath.Join(root, "home", "a")
	fi, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o777 || fi.Mode()&fs.ModeSetuid != 0 || !fi.ModTime().Equal(mt) {
		t.Fatalf("mode %v mtime %v", fi.Mode(), fi.ModTime())
	}
	noParts(t, filepath.Join(root, "home"))
}

func TestUploadSourceSwappedForSymlinkIsRefused(t *testing.T) {
	c, _, root := remote(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "victim"), "secret", 0o600)
	write(t, filepath.Join(src, "f"), "original", 0o644)
	p, err := PlanUpload(context.Background(), c, []string{filepath.Join(src, "f")}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(src, "f"))
	os.Symlink("victim", filepath.Join(src, "f")) // swapped after the plan
	r := p.Run(context.Background(), c, false, nil)
	p.Close()
	if r.Copied != 0 || r.Errors.Count != 1 {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, "home", "f")); err == nil {
		t.Fatal("the swapped-in file's content was uploaded")
	}
	noParts(t, filepath.Join(root, "home"))
}

func TestFailedMkdirSkipsItsContents(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "d", "a"), "aa", 0o644)
	write(t, filepath.Join(root, "home", "d", "sub", "b"), "bb", 0o644)
	dest := t.TempDir()
	p, err := PlanDownload(context.Background(), c, []string{"/home/d"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dest, "d"), "blocking file", 0o644) // a file where the folder "d" would go
	r := p.Run(context.Background(), c, false, nil)
	p.Close()
	if r.Copied != 0 || r.Errors.Count != 4 {
		t.Fatalf("%+v", r)
	}
	// List[0] is the mkdir failure itself; every entry after it must be the
	// dedicated "parent missing" error, not a raw filesystem error (proving
	// the descendants were never attempted, not that they each failed on
	// their own for unrelated reasons such as ENOTDIR).
	for _, e := range r.Errors.List[1:] {
		if !strings.Contains(e, "could not be created") {
			t.Fatalf("expected a parent-missing error, got %q in %+v", e, r.Errors)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "d", "a")); err == nil {
		t.Fatal("a file was written under a folder whose mkdir failed")
	}
	if read(t, filepath.Join(dest, "d")) != "blocking file" {
		t.Fatal("the blocking file was overwritten")
	}
	noParts(t, dest)
}

// opening records, for each file the server is asked to create, its
// root-relative path and its folder's mode at that moment, before the server
// opens it: what another user on the server could reach.
type opening struct {
	path string
	dir  fs.FileMode
}

func recordOpens(t *testing.T, s *sshtest.Server, root string) *[]opening {
	t.Helper()
	var mu sync.Mutex
	var got []opening
	s.OnSFTPOpen(func(p string) {
		fi, err := os.Stat(filepath.Join(root, filepath.Dir(p)))
		if err != nil {
			t.Errorf("stat %s: %v", filepath.Dir(p), err)
			return
		}
		mu.Lock()
		got = append(got, opening{p, fi.Mode()})
		mu.Unlock()
	})
	return &got
}

func upload(t *testing.T, c *sftp.Client, src, dest string) Result {
	t.Helper()
	p, err := PlanUpload(context.Background(), c, []string{src}, dest)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	return p.Run(context.Background(), c, false, nil)
}

var stagedPart = regexp.MustCompile(`^home/\.big\.[0-9a-f]+\.sshgate-part/data$`)

// TestUploadPartFileIsNeverWorldReadable: pkg/sftp cannot create a file with
// a mode, so the server creates the part file at its own default (0644
// here). No other user may open it before the chmod, since an fd opened then
// outlives it: the file must be created inside a folder that is already 0700.
func TestUploadPartFileIsNeverWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	c, s, root := remote(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "big"), strings.Repeat("z", 1<<16), 0o644)
	opens := recordOpens(t, s, root)
	if r := upload(t, c, filepath.Join(src, "big"), "/home"); r.Copied != 1 || r.Errors.Count != 0 {
		t.Fatalf("%+v", r)
	}
	if len(*opens) != 1 || !stagedPart.MatchString((*opens)[0].path) {
		t.Fatalf("opens %+v, want one part file inside a staging folder", *opens)
	}
	if perm := (*opens)[0].dir.Perm(); perm != 0o700 {
		t.Fatalf("staging folder mode %v when the part file was created", perm)
	}
	if read(t, filepath.Join(root, "home", "big")) != strings.Repeat("z", 1<<16) {
		t.Fatal("content")
	}
	noParts(t, filepath.Join(root, "home"))
}

// A setgid destination's files must keep its group: the staging folder's
// chmod keeps the setgid bit it inherited (Linux clears it on chmod).
func TestUploadStagingKeepsSetgid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	c, s, root := remote(t)
	shared := filepath.Join(root, "home", "shared")
	os.Mkdir(shared, 0o755)
	if err := os.Chmod(shared, 0o775|fs.ModeSetgid); err != nil {
		t.Skip("cannot set setgid here:", err)
	}
	src := t.TempDir()
	write(t, filepath.Join(src, "big"), "g", 0o644)
	opens := recordOpens(t, s, root)
	if r := upload(t, c, filepath.Join(src, "big"), "/home/shared"); r.Copied != 1 || r.Errors.Count != 0 {
		t.Fatalf("%+v", r)
	}
	if len(*opens) != 1 {
		t.Fatalf("opens %+v", *opens)
	}
	if d := (*opens)[0].dir; d.Perm() != 0o700 || d&fs.ModeSetgid == 0 {
		t.Fatalf("staging folder mode %v, want 0700 with setgid", d)
	}
	noParts(t, shared)
}

// A folder the run made stays 0700 until its files are in, so they are
// written straight into it, without a staging folder each.
func TestUploadIntoNewFolderIsNotStaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	c, s, root := remote(t)
	src := filepath.Join(t.TempDir(), "d")
	write(t, filepath.Join(src, "big"), "a", 0o644)
	opens := recordOpens(t, s, root)
	if r := upload(t, c, src, "/home"); r.Copied != 1 || r.Errors.Count != 0 {
		t.Fatalf("%+v", r)
	}
	want := regexp.MustCompile(`^home/d/\.big\.[0-9a-f]+\.sshgate-part$`)
	if len(*opens) != 1 || !want.MatchString((*opens)[0].path) {
		t.Fatalf("opens %+v, want one part file directly in the new folder", *opens)
	}
	if perm := (*opens)[0].dir.Perm(); perm != 0o700 {
		t.Fatalf("new folder mode %v while its file was created", perm)
	}
	noParts(t, filepath.Join(root, "home"))
}

// A server that refuses folders (sftp-server -P mkdir) still takes uploads:
// the part file goes beside the destination, as before staging existed.
func TestUploadWithoutMkdirFallsBack(t *testing.T) {
	c, s, root := remote(t)
	s.DenySFTP("Mkdir")
	src := t.TempDir()
	write(t, filepath.Join(src, "big"), "fallback", 0o644)
	opens := recordOpens(t, s, root)
	if r := upload(t, c, filepath.Join(src, "big"), "/home"); r.Copied != 1 || r.Errors.Count != 0 {
		t.Fatalf("%+v", r)
	}
	want := regexp.MustCompile(`^home/\.big\.[0-9a-f]+\.sshgate-part$`)
	if len(*opens) != 1 || !want.MatchString((*opens)[0].path) {
		t.Fatalf("opens %+v, want the part file beside the destination", *opens)
	}
	if read(t, filepath.Join(root, "home", "big")) != "fallback" {
		t.Fatal("content")
	}
	noParts(t, filepath.Join(root, "home"))
}
