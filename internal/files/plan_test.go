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
	if p.Errors.Count != 1 || !strings.Contains(p.Errors.List[0], "clash") {
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
	d := filepath.Join(root, "home", "deep")
	for range MaxDepth + 2 {
		d = filepath.Join(d, "d")
	}
	os.MkdirAll(d, 0o755)
	if _, err := PlanDownload(context.Background(), c, []string{"/home/deep"}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "levels") {
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
