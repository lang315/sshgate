package files

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
