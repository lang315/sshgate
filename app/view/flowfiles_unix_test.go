//go:build unix

package view

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/internal/hubtest"
)

// filesFlow is a flow on an sshtestd serving SFTP, unlocked, with fake
// pickers (the e2e's stubOpen) and a window tall enough for the dialogs.
func filesFlow(t *testing.T) (*flowT, *fakeShell) {
	f := flowWith(t, hubtest.Options{SFTP: true})
	sh := &fakeShell{}
	f.a.Shell = sh
	f.tt.SetSize(1000, 700)
	f.unlock()
	return f, sh
}

// openFiles opens a Files tab on box from its card and waits for its listing.
func (f *flowT) openFiles() (string, *filesTab) {
	f.t.Helper()
	if f.a.tabs.Active != "" {
		f.click("Hosts")
	}
	f.click("Files box")
	id := f.a.tabs.Active
	v := f.a.files[id]
	f.until(func() bool { return v.listing != nil && !v.loading }, "the box listing")
	return id, v
}

// row waits until the table shows (or, with !shown, no longer shows) name.
func (f *flowT) row(name string, shown bool) {
	f.t.Helper()
	f.until(func() bool { return (count(f.tt.Texts(), name) > 0) == shown }, "row "+name)
}

func (f *flowT) read(path string) string {
	f.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

func isFile(r auditRec) bool { return r.Kind == "file" }

// files.spec.ts test 1: browse, upload, download, rename and delete over
// SFTP. The Electron Upload button on macOS is Upload folder here (plan
// Decision 4); main's native conflict box for the second download is the
// app's own dialog (plan Decision 3).
func TestFlowFiles(t *testing.T) {
	f, sh := filesFlow(t)
	_, v := f.openFiles()
	if v.pathIn != "/home" || !f.tt.HasText("Files on box") {
		t.Fatalf("path %q", v.pathIn)
	}

	f.click("New folder")
	f.tt.Type("made")
	f.click("Create")
	f.row("made", true)
	if r := f.lastRec(func(r auditRec) bool { return isFile(r) && r.Action == "mkdir" }, "the mkdir record"); !slices.Equal(r.Remote, []string{"/home/made"}) {
		t.Fatalf("%+v", r)
	}

	local := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(filepath.Join(local, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "a.txt"), []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "sub", "b.txt"), []byte("beta"), 0o600); err != nil {
		t.Fatal(err)
	}
	sh.pick(local)
	f.click("Upload folder")
	f.until(func() bool { return f.tt.HasText("Uploaded 2") }, "Uploaded 2")
	f.row("proj", true) // the relist after the job
	if got := f.read(filepath.Join(f.sftp, "home", "proj", "sub", "b.txt")); got != "beta" {
		t.Fatal(got)
	}
	if r := f.lastRec(func(r auditRec) bool { return isFile(r) && r.Action == "upload" && r.Phase == "end" }, "the upload's end record"); r.Files != 2 || !slices.Equal(r.Local, []string{local}) {
		t.Fatalf("%+v", r)
	}

	sh.pick(local)
	f.click("Upload folder")
	f.until(func() bool { return f.tt.HasText("2 files already exist") }, "the conflict dialog")
	f.pass()
	f.click("Skip existing")
	f.until(func() bool { return f.tt.HasText("skipped 2") }, "skipped 2")

	dl := filepath.Join(t.TempDir(), "dl")
	if err := os.Mkdir(dl, 0o700); err != nil {
		t.Fatal(err)
	}
	f.click("proj")
	sh.pick(dl)
	f.click("Download")
	f.until(func() bool { return f.tt.HasText("Downloaded 2") }, "Downloaded 2")
	if got := f.read(filepath.Join(dl, "proj", "a.txt")); got != "alpha" {
		t.Fatal(got)
	}
	f.click("Download") // proj is still selected
	f.until(func() bool { return f.tt.HasText("in " + dl) }, "the download conflict dialog")
	f.pass()
	f.click("Skip existing")
	f.until(func() bool { return f.tt.HasText("Downloaded 0 · skipped 2") }, "Downloaded 0 · skipped 2")

	f.click("proj")
	f.click("Rename")
	f.tt.Key(ui.Cmd, ui.KeyA)
	f.tt.Type("proj2")
	f.tt.Key(0, ui.KeyEnter)
	f.row("proj2", true)

	f.click("proj2")
	f.click("Delete")
	f.until(func() bool { return f.tt.HasText("Delete for good?") }, "the delete dialog")
	f.pass()
	f.click("Delete for good") // not armed: the folder has contents, the word is not typed
	f.tt.flush()
	if !f.tt.HasText("Delete for good?") {
		t.Fatal("Delete worked without the typed word")
	}
	f.tt.Type("delete")
	f.click("Delete for good")
	f.row("proj2", false)
	if _, err := os.Stat(filepath.Join(f.sftp, "home", "proj2")); !os.IsNotExist(err) {
		t.Fatalf("proj2 still there: %v", err)
	}
}

// files.spec.ts test 2: New folder creates in the folder it opened in. The
// modal takes the pointer, so the navigation under it goes through the
// model (Electron's test filled the Path box behind the dialog).
func TestFlowFilesNewFolderStays(t *testing.T) {
	f, _ := filesFlow(t)
	id, v := f.openFiles()
	if err := os.MkdirAll(filepath.Join(f.sftp, "home", "elsewhere"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.click("New folder")
	f.tt.Type("trap")
	f.a.Update(func() { f.a.loadFiles(id, "/home/elsewhere", nil, false) })
	f.until(func() bool { return v.shown == "/home/elsewhere" && !v.loading }, "the listing of /home/elsewhere")
	f.click("Create")
	f.until(func() bool { _, err := os.Stat(filepath.Join(f.sftp, "home", "trap")); return err == nil }, "/home/trap")
	if _, err := os.Stat(filepath.Join(f.sftp, "home", "elsewhere", "trap")); !os.IsNotExist(err) {
		t.Fatal("made in the folder navigated to")
	}
	f.tt.flush()
	if v.pathIn != "/home/elsewhere" || count(f.tt.Texts(), "trap") != 0 {
		t.Fatalf("pulled back: %q %q", v.pathIn, f.tt.Texts())
	}
}

// Plan Decision 6: a lock closes a waiting dialog and cancels its job in
// the hub; a host saved with a new port ends the jobs the hub holds for it
// ("server changed"), and the editor counts them first.
func TestFlowFilesLockAndServerChange(t *testing.T) {
	f, _ := filesFlow(t)
	if err := os.MkdirAll(filepath.Join(f.sftp, "home", "keep", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	id, v := f.openFiles()
	f.click("keep")
	f.click("Delete")
	f.until(func() bool { return f.tt.HasText("Delete for good?") }, "the delete dialog")
	job := v.jobs[0].id
	// The lock button sits behind the modal: lock from the setup door, as an idle lock would.
	if err := f.h.Lock(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	f.unlock()
	f.until(func() bool { return f.a.tabs.Active == id }, "the Files tab")
	if f.tt.HasText("Delete for good?") || !f.tt.HasText("Cancelled: vault locked") {
		t.Fatalf("after the lock: %q", f.tt.Texts())
	}
	if err := f.h.FilesRun(context.Background(), job, "skip"); err == nil || err.Error() != `no job "`+job+`"` {
		t.Fatalf("the hub still holds the job: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.sftp, "home", "keep", "inner")); err != nil {
		t.Fatal(err)
	}

	f.click("keep")
	f.click("Delete")
	f.until(func() bool { return f.tt.HasText("Delete for good?") }, "the delete dialog")
	// The dialog shows on its tab only; the Hosts tab hides it (the modal takes the pointer).
	f.a.Update(func() { f.a.tabs.ShowHome() })
	f.tt.flush()
	f.click("Edit box")
	f.click("Port")
	f.tt.Key(ui.Cmd, ui.KeyA)
	f.tt.Type("1")
	f.until(func() bool { return f.tt.HasText("Saving will cancel 1 transfer.") }, "the transfer warning")
	f.click("Save")
	f.lastConfig("box", "save")
	f.click("box · files")
	f.until(func() bool { return f.tt.HasText("Cancelled: server changed · deleted 0") }, "the server change in the row")
	if f.tt.HasText("Delete for good?") {
		t.Fatal("the dialog outlived its job")
	}
}
