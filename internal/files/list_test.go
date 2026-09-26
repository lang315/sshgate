package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestListHomeKindsAndLinks(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "a.txt"), "abc", 0o640)
	os.Mkdir(filepath.Join(root, "home", "dir"), 0o755)
	os.Symlink("a.txt", filepath.Join(root, "home", "ln"))
	l, err := List(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != "/home" || l.Truncated || l.Bad != 0 || len(l.Entries) != 3 {
		t.Fatalf("%+v", l)
	}
	got := map[string]Entry{}
	for _, e := range l.Entries {
		got[e.Name] = e
	}
	if e := got["a.txt"]; e.Kind != "file" || e.Size != 3 || e.Mode != 0o640 {
		t.Fatalf("file %+v", e)
	}
	if got["dir"].Kind != "dir" || got["ln"].Kind != "link" || got["ln"].Target != "a.txt" {
		t.Fatalf("%+v", got)
	}
}

func TestListRejectsRelativeAndCountsBadNames(t *testing.T) {
	c, s, _ := remote(t)
	if _, err := List(context.Background(), c, "home"); err == nil {
		t.Fatal("relative path listed")
	}
	s.HostileSFTP("x/..", `a\b`, "ok")
	l, err := List(context.Background(), c, "/hostile")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Name != "ok" || l.Bad != 2 {
		t.Fatalf("%+v", l)
	}
}

func TestListTruncates(t *testing.T) {
	c, _, root := remote(t)
	dir := filepath.Join(root, "home", "many")
	os.Mkdir(dir, 0o755)
	for i := range MaxList + 1 {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d", i)), nil, 0o644)
	}
	l, err := List(context.Background(), c, "/home/many")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Truncated || len(l.Entries) != MaxList {
		t.Fatalf("truncated %v entries %d", l.Truncated, len(l.Entries))
	}
}

func TestListPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	c, _, root := remote(t)
	d := filepath.Join(root, "home", "shut")
	os.Mkdir(d, 0o000)
	t.Cleanup(func() { os.Chmod(d, 0o755) })
	if _, err := List(context.Background(), c, "/home/shut"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("err %v, want permission denied", err)
	}
}

// TestListReadDirContextCancelled exercises ReadDirContext's own cancellation
// check (pkg/sftp's clientConn.sendPacket selects on ctx.Done()). With an
// already-cancelled context, that select wins the race against the (not yet
// arrived) server response, so List fails before the loop is ever reached.
func TestListReadDirContextCancelled(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "a.txt"), "abc", 0o640)
	os.Symlink("a.txt", filepath.Join(root, "home", "ln1"))
	os.Symlink("a.txt", filepath.Join(root, "home", "ln2"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := List(ctx, c, "/home"); err == nil {
		t.Fatal("cancelled context listed")
	}
}

// errCtx passes pkg/sftp's Done()-based checks (its embedded Done() never
// closes) but reports cancellation to code that polls Err(), so a test can
// reach List's per-entry ctx check after ReadDirContext has already
// succeeded.
type errCtx struct{ context.Context }

func (errCtx) Err() error { return context.Canceled }

// TestListPerEntryCtxCheck exercises the ctx.Err() check inside List's
// symlink loop specifically: ReadDirContext succeeds (errCtx's Done() never
// closes), so the loop is reached with at least one entry, and the loop must
// stop and report context.Canceled instead of continuing to call ReadLink.
func TestListPerEntryCtxCheck(t *testing.T) {
	c, _, root := remote(t)
	os.Symlink("a.txt", filepath.Join(root, "home", "ln"))
	_, err := List(errCtx{context.Background()}, c, "/home")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
}

func TestMkdirAndRename(t *testing.T) {
	c, _, root := remote(t)
	if err := Mkdir(c, "rel"); err == nil {
		t.Fatal("relative mkdir")
	}
	if err := Mkdir(c, "/home/new"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, "home", "new")); err != nil || !fi.IsDir() {
		t.Fatal("mkdir did not create")
	}
	write(t, filepath.Join(root, "home", "a"), "a", 0o644)
	write(t, filepath.Join(root, "home", "b"), "b", 0o644)
	if err := Rename(c, "/home/a", "/home/b"); err == nil {
		t.Fatal("rename replaced a file")
	}
	if err := Rename(c, "/home/new", "/home/b"); err == nil {
		t.Fatal("rename replaced with a folder")
	}
	if err := Rename(c, "/home", "/home2"); err == nil {
		t.Fatal("renamed the home directory")
	}
	if err := Rename(c, "/home/a", "/home/c"); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "home", "c")) != "a" {
		t.Fatal("rename lost content")
	}
}

// A new name the next listing would hide as bad (here a backslash) is refused,
// or the folder or file could never be selected again.
func TestMkdirAndRenameRefuseBadNewNames(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "a"), "a", 0o644)
	if err := Mkdir(c, `/home/x\y`); err == nil {
		t.Fatal(`mkdir made a name with \`)
	}
	if err := Rename(c, "/home/a", `/home/x\y`); err == nil {
		t.Fatal(`rename made a name with \`)
	}
	if _, err := os.Lstat(filepath.Join(root, "home", `x\y`)); err == nil {
		t.Fatal("the bad name exists")
	}
}

func TestRenameRefusesRootAndHomeAsTarget(t *testing.T) {
	c, _, root := remote(t)
	write(t, filepath.Join(root, "home", "a"), "a", 0o644)
	if err := Rename(c, "/", "/elsewhere"); err == nil {
		t.Fatal("renamed /")
	}
	if err := Rename(c, "/home/a", "/"); err == nil {
		t.Fatal("renamed onto /")
	}
	if err := Rename(c, "/home/a", "/home"); err == nil {
		t.Fatal("renamed onto the home directory")
	}
}
