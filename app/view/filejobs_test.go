package view

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
)

func (s *fakeShell) pick(paths ...string) { s.mu.Lock(); s.picks = paths; s.mu.Unlock() }

func (s *fakeShell) titles() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.asked...)
}

func plannedNote(id string, files, dirs, conflicts int, sample ...string) map[string]any {
	return map[string]any{"id": id, "files": files, "dirs": dirs, "links": 0, "bytes": 1536,
		"conflicts": map[string]any{"count": conflicts, "sample": append([]string{}, sample...)}, "errorCount": 0, "errors": []string{}}
}

func doneNote(id, op string, copied, skipped int, cancelled bool, reason string) map[string]any {
	return map[string]any{"id": id, "op": op, "copied": copied, "skipped": skipped, "deleted": 0, "bytes": 0,
		"errorCount": 0, "errors": []string{}, "cancelled": cancelled, "reason": reason}
}

// count of method among the door's calls.
func calls(d *fakeDoor, method string) int {
	n := 0
	for _, m := range d.callList() {
		if m == method {
			n++
		}
	}
	return n
}

func jobsApp(t *testing.T) (*App, *tester, *fakeDoor, *clock, *fakeShell, *filesTab) {
	a, tt, d, c, sh := filesApp(t, map[string][]hubapi.FileEntry{"/home": {dir("proj"), file("a.txt")}})
	_, v := openFilesTab(t, a, tt)
	return a, tt, d, c, sh, v
}

// upload starts an upload of paths through Upload folder.
func upload(t *testing.T, tt *tester, sh *fakeShell, paths ...string) {
	t.Helper()
	sh.pick(paths...)
	clickT(t, tt, "Upload folder")
	tt.flush()
}

func TestUploadRunsThenRelistsOnce(t *testing.T) {
	a, tt, d, _, sh, v := jobsApp(t)
	upload(t, tt, sh, "/l/proj")
	if got := sh.titles(); !reflect.DeepEqual(got, []string{"Upload to box:/home (folders)"}) {
		t.Fatal(got)
	}
	if params(d, "files.plan") != `{"id":"f1","server":"box","op":"upload","sources":["/l/proj"],"dest":"/home"}` || !tt.HasText("Checking…") {
		t.Fatalf("plan %s, %q", params(d, "files.plan"), tt.Texts())
	}
	d.resetCalls()
	note(t, a, tt, "files.planned", plannedNote("f1", 2, 1, 0))
	note(t, a, tt, "files.planned", plannedNote("f1", 2, 1, 0))
	if calls(d, "files.run") != 1 || params(d, "files.run") != `{"conflict":"skip","id":"f1"}` {
		t.Fatalf("runs %v %s", d.callList(), params(d, "files.run"))
	}
	note(t, a, tt, "files.progress", map[string]any{"id": "f1", "file": "proj/a.txt", "done": 1024, "total": 2048})
	if !tt.HasText("1.0 KB of 2.0 KB · proj/a.txt") {
		t.Fatalf("progress: %q", tt.Texts())
	}
	note(t, a, tt, "files.done", doneNote("f1", "upload", 2, 0, false, ""))
	note(t, a, tt, "files.done", doneNote("f1", "upload", 2, 0, false, ""))
	if calls(d, "files.list") != 1 || !tt.HasText("Uploaded 2") || len(v.jobs) != 1 {
		t.Fatalf("after done: %v %q", d.callList(), tt.Texts())
	}
	clickT(t, tt, "Dismiss Upload proj")
	if len(v.jobs) != 0 || tt.HasText("Uploaded 2") {
		t.Fatal("Dismiss left the row")
	}
}

func TestUploadConflictAsksAndWaits(t *testing.T) {
	a, tt, d, c, sh, _ := jobsApp(t)
	upload(t, tt, sh, "/l/proj")
	d.resetCalls()
	note(t, a, tt, "files.planned", plannedNote("f1", 2, 1, 2, "proj/a.txt", "proj/sub/b.txt"))
	if hasCall(d, "files.run") || !tt.HasText("2 files already exist") || !tt.HasText("in box:/home") || !tt.HasText("proj/sub/b.txt") || !tt.HasText("Waiting for you") {
		t.Fatalf("no dialog: %v %q", d.callList(), tt.Texts())
	}
	if !tt.Focused("Cancel") {
		t.Fatal("Cancel does not have the focus")
	}
	clickT(t, tt, "Skip existing") // within the delay: nothing
	if hasCall(d, "files.run") {
		t.Fatal("Skip existing worked within the delay")
	}
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyEnter) // the keyboard reaches Cancel only
	tt.flush()
	if hasCall(d, "files.run") || !tt.HasText("Cancelled") || !slices.Contains(d.sentList(), `files.cancel {"id":"f1"}`) {
		t.Fatalf("Enter: %v %v %q", d.callList(), d.sentList(), tt.Texts())
	}
	upload(t, tt, sh, "/l/proj")
	note(t, a, tt, "files.planned", plannedNote("f2", 2, 1, 2, "proj/a.txt"))
	pass(c, tt)
	clickT(t, tt, "Skip existing")
	tt.flush()
	if params(d, "files.run") != `{"conflict":"skip","id":"f2"}` || tt.HasText("Files already exist") {
		t.Fatalf("Skip existing: %s %q", params(d, "files.run"), tt.Texts())
	}
	note(t, a, tt, "files.done", doneNote("f2", "upload", 0, 2, false, ""))
	if !tt.HasText("Uploaded 0 · skipped 2") {
		t.Fatalf("%q", tt.Texts())
	}
}

// Electron's native box asked for a download; here the app's own dialog
// does, and the hub gets overwrite or skip only (plan Decision 3).
func TestDownloadSkipsOrAsks(t *testing.T) {
	a, tt, d, c, sh, _ := jobsApp(t)
	clickT(t, tt, "proj")
	sh.pick("/Users/me/dl")
	clickT(t, tt, "Download")
	tt.flush()
	if params(d, "files.plan") != `{"id":"f1","server":"box","op":"download","sources":["/home/proj"],"dest":"/Users/me/dl"}` || !slices.Contains(sh.titles(), "Download from box to…") {
		t.Fatalf("plan %s %v", params(d, "files.plan"), sh.titles())
	}
	note(t, a, tt, "files.planned", plannedNote("f1", 2, 1, 0))
	if params(d, "files.run") != `{"conflict":"skip","id":"f1"}` {
		t.Fatal(params(d, "files.run"))
	}
	d.resetCalls()
	note(t, a, tt, "files.done", doneNote("f1", "download", 2, 0, false, ""))
	if hasCall(d, "files.list") || !tt.HasText("Downloaded 2") {
		t.Fatalf("a download relisted: %v", d.callList())
	}
	clickT(t, tt, "Download")
	tt.flush()
	note(t, a, tt, "files.planned", plannedNote("f2", 2, 1, 1, "proj/b\u202e.txt"))
	if hasCall(d, "files.run") || !tt.HasText("1 file already exists") || !tt.HasText("in /Users/me/dl") || !tt.HasText(`proj/b\u202e.txt`) {
		t.Fatalf("no dialog: %v %q", d.callList(), tt.Texts())
	}
	pass(c, tt)
	clickT(t, tt, "Overwrite all")
	tt.flush()
	if params(d, "files.run") != `{"conflict":"overwrite","id":"f2"}` {
		t.Fatal(params(d, "files.run"))
	}
}

func TestDeleteNeedsTheWordAndTheDelay(t *testing.T) {
	a, tt, d, c, _, _ := jobsApp(t)
	clickT(t, tt, "proj")
	tt.Key(0, ui.KeyDelete)
	tt.flush()
	if params(d, "files.plan") != `{"id":"f1","server":"box","op":"delete","sources":["/home/proj"]}` {
		t.Fatal(params(d, "files.plan"))
	}
	note(t, a, tt, "files.planned", plannedNote("f1", 3, 1, 0))
	if !tt.HasText("Delete for good?") || !tt.HasText("3 files, 1 folder, 0 links · 1.5 KB. There is no undo.") || !tt.Focused("Type delete to confirm") {
		t.Fatalf("%q", tt.Texts())
	}
	pass(c, tt)
	d.resetCalls()
	if err := tt.Click("Delete for good"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if hasCall(d, "files.run") {
		t.Fatal("Delete worked without the typed word")
	}
	tt.Type("Delete")
	tt.Key(0, ui.KeyEnter) // Enter in the field cancels
	tt.flush()
	if hasCall(d, "files.run") || !tt.HasText("Cancelled") {
		t.Fatalf("Enter: %v %q", d.callList(), tt.Texts())
	}
	clickT(t, tt, "proj")
	clickT(t, tt, "Delete")
	tt.flush()
	note(t, a, tt, "files.planned", plannedNote("f2", 3, 1, 0))
	tt.Type("delete")
	d.resetCalls()
	if err := tt.Click("Delete for good"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if hasCall(d, "files.run") {
		t.Fatal("Delete worked within the delay")
	}
	pass(c, tt)
	d.resetCalls()
	if err := tt.Click("Delete for good"); err != nil {
		t.Fatal(err)
	}
	tt.flush()
	if params(d, "files.run") != `{"conflict":"skip","id":"f2"}` {
		t.Fatalf("Delete: %v", d.callList())
	}
	note(t, a, tt, "files.done", map[string]any{"id": "f2", "op": "delete", "deleted": 4, "errors": []string{}})
	if !tt.HasText("Deleted 4") || calls(d, "files.list") != 1 {
		t.Fatalf("%v %q", d.callList(), tt.Texts())
	}
}

// One file, nothing behind it: no word to type, Cancel has the focus, and
// Esc cancels (plan Decision 9).
func TestDeleteOfOneFileAsksNoWord(t *testing.T) {
	a, tt, d, _, _, _ := jobsApp(t)
	clickT(t, tt, "a.txt")
	clickT(t, tt, "Delete")
	tt.flush()
	note(t, a, tt, "files.planned", plannedNote("f1", 1, 0, 0))
	if tt.HasText("Type delete to confirm") || !tt.Focused("Cancel") || !tt.HasText("1 file, 0 folders, 0 links · 1.5 KB. There is no undo.") {
		t.Fatalf("%q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.flush()
	if hasCall(d, "files.run") || tt.HasText("Delete for good?") || !slices.Contains(d.sentList(), `files.cancel {"id":"f1"}`) {
		t.Fatalf("Esc: %v %v", d.callList(), d.sentList())
	}
}

// A Cancel pressed while planning is sent again when the plan lands, and
// the job never runs.
func TestCancelWhilePlanning(t *testing.T) {
	a, tt, d, _, sh, _ := jobsApp(t)
	upload(t, tt, sh, "/l/proj")
	clickT(t, tt, "Cancel Upload proj")
	note(t, a, tt, "files.planned", plannedNote("f1", 2, 1, 0))
	if hasCall(d, "files.run") || !reflect.DeepEqual(d.sentList(), []string{`files.cancel {"id":"f1"}`, `files.cancel {"id":"f1"}`}) {
		t.Fatalf("%v %v", d.callList(), d.sentList())
	}
	note(t, a, tt, "files.done", doneNote("f1", "upload", 0, 0, true, "cancelled"))
	if !tt.HasText("Cancelled · uploaded 0") {
		t.Fatalf("%q", tt.Texts())
	}
}

// A plan that fails here may still have been accepted by the hub (a timeout):
// the job is cancelled there too, and never run.
func TestFailedPlanCancelsTheJobOnTheHub(t *testing.T) {
	_, tt, d, _, sh, _ := jobsApp(t)
	d.set(func() { d.errs = map[string]error{"files.plan": errors.New("timed out")} })
	upload(t, tt, sh, "/l/proj")
	if !tt.HasText("timed out") || hasCall(d, "files.run") || !slices.Contains(d.sentList(), `files.cancel {"id":"f1"}`) {
		t.Fatalf("%v %v %q", d.callList(), d.sentList(), tt.Texts())
	}
}

func TestPlanAndRunErrorsShowInTheRow(t *testing.T) {
	a, tt, d, _, sh, _ := jobsApp(t)
	d.set(func() { d.errs = map[string]error{"files.plan": errors.New("sources: 1 to 1000 paths")} })
	upload(t, tt, sh, "/l/proj")
	if !tt.HasText("sources: 1 to 1000 paths") {
		t.Fatalf("%q", tt.Texts())
	}
	d.set(func() { d.errs = map[string]error{"files.run": errors.New("Vault is locked; unlock it in the app")} })
	upload(t, tt, sh, "/l/proj")
	note(t, a, tt, "files.planned", plannedNote("f2", 1, 0, 0))
	if !tt.HasText("Vault is locked; unlock it in the app") || !slices.Contains(d.sentList(), `files.cancel {"id":"f2"}`) {
		t.Fatalf("%v %q", d.sentList(), tt.Texts())
	}
	note(t, a, tt, "files.done", doneNote("f2", "upload", 0, 0, false, "the upload folder is missing or not a folder"))
	if tt.HasText("Failed: the upload folder is missing or not a folder · uploaded 0") {
		t.Fatal("a job ended here took the hub's later files.done")
	}
	d.set(func() { d.errs = nil })
	upload(t, tt, sh, "/l/proj")
	note(t, a, tt, "files.done", doneNote("f3", "upload", 0, 0, false, "the upload folder is missing or not a folder"))
	if !tt.HasText("Failed: the upload folder is missing or not a folder · uploaded 0") {
		t.Fatalf("%q", tt.Texts())
	}
}

// A lock closes the dialog and cancels its job; a job planned behind the
// lock is cancelled, not run; a running one keeps going.
func TestLockCancelsWhatWaitsForTheDialog(t *testing.T) {
	a, tt, d, _, sh, v := jobsApp(t)
	upload(t, tt, sh, "/l/one")
	upload(t, tt, sh, "/l/two")
	upload(t, tt, sh, "/l/three")
	note(t, a, tt, "files.planned", plannedNote("f1", 1, 0, 1, "one"))
	note(t, a, tt, "files.planned", plannedNote("f3", 1, 0, 0))
	if !tt.HasText("1 file already exists") || v.jobs[2].state != "running" {
		t.Fatalf("%q", tt.Texts())
	}
	d.resetCalls()
	d.set(func() { d.status = hubapi.Status{HasVault: true, Locked: true} })
	note(t, a, tt, "locked", map[string]string{"reason": "manual"})
	note(t, a, tt, "files.planned", plannedNote("f2", 1, 0, 0))
	note(t, a, tt, "files.done", doneNote("f3", "upload", 1, 0, false, ""))
	for _, m := range d.callList() {
		if m != "status" {
			t.Fatalf("called %s behind the lock: %v", m, d.callList())
		}
	}
	if !reflect.DeepEqual(d.sentList(), []string{`files.cancel {"id":"f1"}`, `files.cancel {"id":"f2"}`}) {
		t.Fatal(d.sentList())
	}
	d.set(func() { d.status = hubapi.Status{HasVault: true}; d.pw = "pw" })
	a.Update(func() { a.unlock("pw") })
	tt.flush()
	if tt.HasText("1 file already exists") || v.jobs[0].err != "Cancelled: vault locked" || v.jobs[1].err != "Cancelled: vault locked" || !tt.HasText("Uploaded 1") {
		t.Fatalf("after unlock: %q", tt.Texts())
	}
}

func TestHubStopAndCloseEndJobs(t *testing.T) {
	a, tt, d, _, sh, v := jobsApp(t)
	upload(t, tt, sh, "/l/proj")
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", In: time.Second}) })
	tt.flush()
	if v.jobs[0].err != "hub restarted" || len(a.jobs) != 0 {
		t.Fatalf("%+v", v.jobs[0])
	}
	running(a, tt)
	tt.flush()
	upload(t, tt, sh, "/l/again")
	id := v.jobs[1].id
	clickT(t, tt, "Close tab")
	if !slices.Contains(d.sentList(), `files.cancel {"id":"`+id+`"}`) || len(a.jobs) != 0 || len(a.files) != 0 {
		t.Fatalf("close: %v %v", d.sentList(), a.jobs)
	}
	d.resetCalls()
	note(t, a, tt, "files.planned", plannedNote(id, 1, 0, 0))
	if hasCall(d, "files.run") {
		t.Fatal("a closed tab's job ran")
	}
}

func TestDropChecksThePaths(t *testing.T) {
	a, tt, d, _, _, v := jobsApp(t)
	id := a.tabs.Active
	real := filepath.Join(t.TempDir(), "drop-me")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	drop := func(paths ...string) {
		a.Update(func() { a.dropFiles(id, paths) })
		tt.flush()
	}
	drop("rel/x")
	if v.err != "cannot upload rel/x: not an absolute path" || hasCall(d, "files.plan") {
		t.Fatal(v.err)
	}
	missing := filepath.Join(real, "missing")
	_, want := os.Lstat(missing)
	drop(missing)
	if want == nil || v.err != want.Error() || hasCall(d, "files.plan") {
		t.Fatal(v.err)
	}
	drop(make([]string, 1001)...)
	if v.err != "Drop at most 1000 items at a time." || hasCall(d, "files.plan") {
		t.Fatal(v.err)
	}
	drop(real)
	if want := `{"id":"f1","server":"box","op":"upload","sources":["` + real + `"],"dest":"/home"}`; params(d, "files.plan") != want {
		t.Fatalf("plan %s", params(d, "files.plan"))
	}
	if !tt.HasText("Upload drop-me") {
		t.Fatalf("%q", tt.Texts())
	}
}

// A picker answer that lands behind the lock starts nothing.
func TestPickerAnswerAfterALockIsDropped(t *testing.T) {
	a, tt, d, _, sh, _ := jobsApp(t)
	gate := make(chan struct{})
	sh.pick("/l/proj")
	a.Shell = gatedShell{sh, gate}
	clickT(t, tt, "Upload files")
	d.set(func() { d.status = hubapi.Status{HasVault: true, Locked: true} })
	note(t, a, tt, "locked", map[string]string{"reason": "idle"})
	close(gate)
	tt.flush()
	if hasCall(d, "files.plan") {
		t.Fatal("planned behind the lock")
	}
}

type gatedShell struct {
	*fakeShell
	gate chan struct{}
}

func (g gatedShell) PickPaths(title string, dirs bool) ([]string, error) {
	<-g.gate
	return g.fakeShell.PickPaths(title, dirs)
}

func TestEditorCountsTransfers(t *testing.T) {
	a, tt, _, _, sh, _ := jobsApp(t)
	tt.SetSize(1000, 900)
	upload(t, tt, sh, "/l/proj")
	clickT(t, tt, "Hosts")
	clickT(t, tt, "Edit box")
	fill(t, tt, "Port", "23")
	if !tt.HasText("Saving will cancel 1 transfer.") {
		t.Fatalf("%q", tt.Texts())
	}
	if a.transfers("box") != 1 {
		t.Fatal(a.transfers("box"))
	}
}

func TestQueuedConflictWaitsAgain(t *testing.T) {
	a, tt, d, c, sh, _ := jobsApp(t)
	upload(t, tt, sh, "/l/one")
	upload(t, tt, sh, "/l/two")
	note(t, a, tt, "files.planned", plannedNote("f1", 1, 0, 1, "one"))
	note(t, a, tt, "files.planned", plannedNote("f2", 1, 0, 1, "two"))
	pass(c, tt)
	clickT(t, tt, "Skip existing")
	tt.flush()
	d.resetCalls()
	clickT(t, tt, "Skip existing") // f2's dialog, just shown
	tt.flush()
	if hasCall(d, "files.run") {
		t.Fatal("the next job's dialog took the click meant for the last one")
	}
	pass(c, tt)
	clickT(t, tt, "Skip existing")
	tt.flush()
	if params(d, "files.run") != `{"conflict":"skip","id":"f2"}` {
		t.Fatal(params(d, "files.run"))
	}
}

func TestConflictEscCancels(t *testing.T) {
	a, tt, d, _, sh, _ := jobsApp(t)
	upload(t, tt, sh, "/l/proj")
	d.resetCalls()
	note(t, a, tt, "files.planned", plannedNote("f1", 2, 1, 1, "proj/a.txt"))
	if !tt.HasText("1 file already exists") {
		t.Fatalf("no dialog: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.flush()
	if hasCall(d, "files.run") || tt.HasText("1 file already exists") || !slices.Contains(d.sentList(), `files.cancel {"id":"f1"}`) {
		t.Fatalf("Esc: %v %v %q", d.callList(), d.sentList(), tt.Texts())
	}
}
