package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanDownloadConflictsMergeAndErrors(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "src", "a.txt"), "aa", 0o644)
	write(t, filepath.Join(root, "home", "src", "sub", "b.txt"), "b", 0o644)
	write(t, filepath.Join(root, "home", "src", "clash"), "file on the server", 0o644)
	os.Symlink("a.txt", filepath.Join(root, "home", "src", "inner-link"))
	dest := t.TempDir()
	write(t, filepath.Join(dest, "src", "a.txt"), "old", 0o644) // conflict
	os.MkdirAll(filepath.Join(dest, "src", "sub"), 0o755)      // merge
	os.MkdirAll(filepath.Join(dest, "src", "clash"), 0o755)    // folder where a file goes
	p, err := PlanDownload(context.Background(), c, []string{"/home/src"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Files != 2 || p.Dirs != 2 || p.Links != 1 || p.Bytes != 3 || p.Conflicts != 1 || p.Sample[0] != "src/a.txt" {
		t.Fatalf("%+v", p)
	}
	if p.Errors.Count != 1 || !strings.Contains(p.Errors.List[0], errNotFile.Error()) {
		t.Fatalf("errors %+v", p.Errors)
	}
}

func TestPlanDownloadHostileNames(t *testing.T) {
	c, s, _ := remote(t)
	s.HostileSFTP("x/..", "x/.", `a\b`, "nul\x00", "\xff", "A", "a", "ok")
	p, err := PlanDownload(context.Background(), c, []string{"/hostile"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// ok and A plan; ".." "." a\b nul \xff are bad names; "a" differs from "A" only in case.
	if p.Files != 2 || p.Errors.Count != 6 {
		t.Fatalf("files %d errors %+v", p.Files, p.Errors)
	}
}

func TestPlanDownloadSelectedLinkIsFollowedOnce(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "real", "f"), "x", 0o644)
	os.Symlink("real", filepath.Join(root, "home", "ln"))
	p, err := PlanDownload(context.Background(), c, []string{"/home/ln"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Dirs != 1 || p.Files != 1 {
		t.Fatalf("%+v", p)
	}
}

func TestPlanDepthLimit(t *testing.T) {
	c, _, root := remote(t)
	// The selected source is depth 0, so exactly MaxDepth levels are depths
	// 0..MaxDepth-1: the source folder plus MaxDepth-1 nested "d" folders.
	ok := filepath.Join(root, "home", "ok")
	d := ok
	for range MaxDepth - 1 {
		d = filepath.Join(d, "d")
	}
	os.MkdirAll(d, 0o755)
	ok2, err := PlanDownload(context.Background(), c, []string{"/home/ok"}, t.TempDir())
	if err != nil {
		t.Fatalf("exactly MaxDepth levels should plan: %v", err)
	}
	ok2.Close()

	bad := filepath.Join(root, "home", "bad")
	d = bad
	for range MaxDepth {
		d = filepath.Join(d, "d")
	}
	os.MkdirAll(d, 0o755)
	if _, err := PlanDownload(context.Background(), c, []string{"/home/bad"}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "levels") {
		t.Fatalf("err %v", err)
	}
}

func TestPlanDownloadNeedsAFolder(t *testing.T) {
	c, _, _ := remote(t)
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o644)
	if _, err := PlanDownload(context.Background(), c, []string{"/home"}, f); err == nil {
		t.Fatal("planned into a file")
	}
	if _, err := PlanDownload(context.Background(), c, []string{"home"}, t.TempDir()); err == nil {
		t.Fatal("planned a relative source")
	}
}

func TestPlanUpload(t *testing.T) {
	c, _, root := remote(t)
	src := filepath.Join(t.TempDir(), "proj")
	write(t, filepath.Join(src, "a"), "aaa", 0o644)
	write(t, filepath.Join(src, "sub", "b"), "b", 0o644)
	os.Symlink("/etc/passwd", filepath.Join(src, "leak"))
	write(t, filepath.Join(root, "home", "proj", "a"), "old", 0o644)
	p, err := PlanUpload(context.Background(), c, []string{src}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Files != 2 || p.Dirs != 2 || p.Links != 1 || p.Conflicts != 1 || p.Sample[0] != "proj/a" {
		t.Fatalf("%+v", p)
	}
	if _, err := PlanUpload(context.Background(), c, []string{src}, "home"); err == nil {
		t.Fatal("relative destination")
	}
}

func TestPlanUploadSelectedLinkIsFollowedOnce(t *testing.T) {
	c, _, _ := remote(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "real.txt"), "r", 0o644)
	os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "ln.txt"))
	p, err := PlanUpload(context.Background(), c, []string{filepath.Join(dir, "ln.txt")}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Files != 1 || p.Links != 0 || p.Sample != nil {
		t.Fatalf("%+v", p)
	}
}

func TestPlanDelete(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "tree", "a"), "aa", 0o644)
	write(t, filepath.Join(root, "home", "tree", "sub", "b"), "b", 0o644)
	os.MkdirAll(filepath.Join(root, "home", "keep"), 0o755)
	os.Symlink("../keep", filepath.Join(root, "home", "tree", "to-keep"))
	p, err := PlanDelete(context.Background(), c, []string{"/home/tree"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Files != 2 || p.Dirs != 2 || p.Links != 1 || p.Bytes != 3 {
		t.Fatalf("%+v", p)
	}
	for _, bad := range []string{"/", "/home", "home/tree"} {
		if _, err := PlanDelete(context.Background(), c, []string{bad}); err == nil {
			t.Fatalf("planned deleting %q", bad)
		}
	}
}

func TestPlanCancelled(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "x", "f"), "f", 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PlanDownload(ctx, c, []string{"/home/x"}, t.TempDir()); err == nil {
		t.Fatal("a cancelled plan succeeded")
	}
}

func TestPlanUploadRejectsDuplicateBasenames(t *testing.T) {
	c, _, _ := remote(t)
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	write(t, filepath.Join(dir1, "same"), "a", 0o644)
	write(t, filepath.Join(dir2, "same"), "b", 0o644)
	p, err := PlanUpload(context.Background(), c, []string{filepath.Join(dir1, "same"), filepath.Join(dir2, "same")}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Files != 1 || p.Errors.Count != 1 {
		t.Fatalf("%+v errors %+v", p, p.Errors)
	}
}

func TestPlanUploadDanglingSelectedSymlinkIsReported(t *testing.T) {
	c, _, _ := remote(t)
	dir := t.TempDir()
	link := filepath.Join(dir, "broken")
	os.Symlink(filepath.Join(dir, "does-not-exist"), link)
	p, err := PlanUpload(context.Background(), c, []string{link}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Files != 0 || p.Links != 0 || p.Errors.Count != 1 {
		t.Fatalf("%+v errors %+v", p, p.Errors)
	}
}

func TestPlanDeleteSelectedSymlinkToFolderIsJustALink(t *testing.T) {
	c, _, root := remote(t)
	os.MkdirAll(filepath.Join(root, "home", "target"), 0o755)
	os.Symlink("target", filepath.Join(root, "home", "ln"))
	p, err := PlanDelete(context.Background(), c, []string{"/home/ln"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Links != 1 || p.Dirs != 0 || p.Files != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestPlanErrorsNeverNameALocalPath(t *testing.T) {
	c, _, root := remote(t)

	// Upload: a source whose parent folder can't be opened as an os.Root.
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "gone", "src")
	up, err := PlanUpload(context.Background(), c, []string{missing}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	if up.Errors.Count == 0 {
		t.Fatal("expected an upload error, got none")
	}
	for _, e := range up.Errors.List {
		if strings.Contains(e, tmp) {
			t.Fatalf("upload error leaked a local path: %q", e)
		}
	}

	// Download: a conflict at the destination should never name the
	// destination folder either.
	write(t, filepath.Join(root, "home", "d", "clash"), "x", 0o644)
	dest := t.TempDir()
	os.MkdirAll(filepath.Join(dest, "d", "clash"), 0o755)
	down, err := PlanDownload(context.Background(), c, []string{"/home/d"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	defer down.Close()
	if down.Errors.Count == 0 {
		t.Fatal("expected a download error, got none")
	}
	for _, e := range down.Errors.List {
		if strings.Contains(e, dest) {
			t.Fatalf("download error leaked a local path: %q", e)
		}
	}
}

func TestPlanDownloadOpenRootFailureIsGeneric(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	c, _, _ := remote(t)
	dest := t.TempDir()
	os.Chmod(dest, 0o000)
	t.Cleanup(func() { os.Chmod(dest, 0o755) })
	_, err := PlanDownload(context.Background(), c, []string{"/home"}, dest)
	if err == nil {
		t.Fatal("expected an error opening dest")
	}
	if strings.Contains(err.Error(), dest) {
		t.Fatalf("error leaked the dest path: %v", err)
	}
}
