package model

import (
	"reflect"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

// Ports of desktop/test/files.test.ts and the dialog-title cases of
// files-main.test.ts. windowRange, nextSelection, newJobId, lastFolder and
// hiddenPref are not ported: ui.Table, the app's job counter and in-memory
// preferences replace them (plan Decisions 8, 10 and 14).

func e(name, kind string, size int64) hubapi.FileEntry {
	return hubapi.FileEntry{Name: name, Kind: kind, Size: size, Mode: 0o644}
}

func names(es []hubapi.FileEntry) []string {
	var out []string
	for _, x := range es {
		out = append(out, x.Name)
	}
	return out
}

func TestSortEntries(t *testing.T) {
	es := []hubapi.FileEntry{e("b", "file", 5), e("a", "file", 9), e("z", "dir", 0), e("c", "dir", 0)}
	for _, tc := range []struct {
		key  string
		desc bool
		want []string
	}{
		{"name", false, []string{"c", "z", "a", "b"}},
		{"size", true, []string{"c", "z", "a", "b"}},
		{"name", true, []string{"z", "c", "b", "a"}},
	} {
		if got := names(SortEntries(es, tc.key, tc.desc)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %v: %v, want %v", tc.key, tc.desc, got, tc.want)
		}
	}
	if names(es)[0] != "b" {
		t.Fatal("SortEntries sorted its argument in place")
	}
}

func TestVisibleEntries(t *testing.T) {
	es := []hubapi.FileEntry{e(".env", "file", 0), e("a", "file", 0)}
	if got := names(VisibleEntries(es, false)); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatal(got)
	}
	if len(VisibleEntries(es, true)) != 2 {
		t.Fatal("hidden entries dropped while shown")
	}
}

func TestFormatSizeModeTime(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 5 << 30: "5.0 GB"} {
		if got := FormatSize(n); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", n, got, want)
		}
	}
	for _, tc := range []struct {
		kind string
		mode uint32
		want string
	}{{"dir", 0o755, "drwxr-xr-x"}, {"link", 0o777, "lrwxrwxrwx"}, {"file", 0o640, "-rw-r-----"}, {"other", 0o4755, "-rwxr-xr-x"}} {
		if got := FormatMode(tc.kind, tc.mode); got != tc.want {
			t.Errorf("FormatMode(%s, %o) = %q, want %q", tc.kind, tc.mode, got, tc.want)
		}
	}
	if got := FormatTime(time.Date(2026, 10, 15, 9, 5, 59, 0, time.UTC).Unix(), time.UTC); got != "2026-10-15 09:05" {
		t.Fatal(got)
	}
}

func TestPaths(t *testing.T) {
	if JoinPath("/", "a") != "/a" || JoinPath("/home/u", "a") != "/home/u/a" {
		t.Fatal(JoinPath("/", "a"), JoinPath("/home/u", "a"))
	}
	for p, want := range map[string]string{"/home/u": "/home", "/home": "/"} {
		if got, ok := ParentPath(p); !ok || got != want {
			t.Errorf("ParentPath(%q) = %q %v", p, got, ok)
		}
	}
	for _, p := range []string{"/", ""} {
		if _, ok := ParentPath(p); ok {
			t.Errorf("ParentPath(%q) has a parent", p)
		}
	}
}

func TestNameError(t *testing.T) {
	for _, n := range []string{"", ".", "..", "a/b", "a\u0000b", "a\\b"} {
		if NameError(n) == "" {
			t.Errorf("%q accepted", n)
		}
	}
	if NameError("") != "Enter a name." || NameError("a/b") != `A name cannot contain /, \, or NUL.` || NameError("ok name") != "" {
		t.Fatal(NameError(""), NameError("a/b"), NameError("ok name"))
	}
}

func planned(files, dirs, links, errs int) hubapi.FilesPlanned {
	return hubapi.FilesPlanned{Files: files, Dirs: dirs, Links: links, ErrorCount: errs}
}

func TestDeleteNeedsTyping(t *testing.T) {
	for _, tc := range []struct {
		hasFolder bool
		selected  int
		p         hubapi.FilesPlanned
		want      bool
	}{
		{false, 2, planned(2, 0, 0, 0), false},
		{true, 1, planned(0, 1, 0, 0), false},
		{true, 1, planned(3, 1, 0, 0), true},
		{true, 2, planned(1, 1, 1, 0), true},
		{true, 1, planned(0, 1, 0, 1), true},
		{false, 1, planned(0, 0, 0, 1), false},
	} {
		if got := DeleteNeedsTyping(tc.hasFolder, tc.selected, tc.p); got != tc.want {
			t.Errorf("%+v: %v", tc, got)
		}
	}
}

func TestPruneSelection(t *testing.T) {
	es := []hubapi.FileEntry{e("a", "file", 0), e("c", "file", 0)}
	for _, tc := range []struct{ in, want []string }{
		{[]string{"a", "b", "c"}, []string{"a", "c"}},
		{nil, nil},
		{[]string{"b"}, nil},
	} {
		if got := PruneSelection(tc.in, es); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: %v", tc.in, got)
		}
	}
}

func TestActionsReady(t *testing.T) {
	l := &hubapi.FileListing{Path: "/x"}
	if ActionsReady(nil, false) || ActionsReady(nil, true) || ActionsReady(l, true) || !ActionsReady(l, false) {
		t.Fatal("ActionsReady")
	}
}

func TestRelistAfterJob(t *testing.T) {
	for _, tc := range []struct {
		job, shown, requested string
		loading               bool
		want                  string
	}{
		{"/a", "/a", "/a", false, "now"},
		{"/b", "/a", "/a", false, "no"},
		{"/a", "/a", "/b", true, "no"},
		{"/a", "/a", "/a", true, "after"},
		{"/a", "/a", "/b", false, "no"},
	} {
		if got := RelistAfterJob(tc.job, tc.shown, tc.requested, tc.loading); got != tc.want {
			t.Errorf("%+v: %s", tc, got)
		}
	}
}

// A download with conflicts asks in the app's own dialog, as an upload does
// (plan Decision 3): Electron's "ask" row becomes "confirm".
func TestPlannedAction(t *testing.T) {
	for _, tc := range []struct {
		op        string
		conflicts int
		cancel    bool
		want      string
	}{
		{"upload", 0, false, "skip"},
		{"upload", 2, false, "confirm"},
		{"download", 3, false, "confirm"},
		{"download", 0, false, "skip"},
		{"delete", 0, false, "confirm"},
		{"upload", 0, true, "cancel"},
		{"download", 0, true, "cancel"},
		{"delete", 0, true, "cancel"},
		{"delete", 5, true, "cancel"},
	} {
		if got := PlannedAction(tc.op, tc.conflicts, tc.cancel); got != tc.want {
			t.Errorf("%+v: %s", tc, got)
		}
	}
}

func TestDoneSummary(t *testing.T) {
	for _, tc := range []struct {
		d    *hubapi.FilesDone
		err  string
		want string
	}{
		{&hubapi.FilesDone{Op: "upload", Copied: 3, Skipped: 1, ErrorCount: 2}, "", "Uploaded 3 · skipped 1 · 2 errors"},
		{&hubapi.FilesDone{Op: "delete", Deleted: 4}, "", "Deleted 4"},
		{&hubapi.FilesDone{Op: "download", Copied: 1, Cancelled: true, Reason: "server changed"}, "", "Cancelled: server changed · downloaded 1"},
		{nil, "hub restarted", "hub restarted"},
		// Plan Decision 9: no "Cancelled: cancelled", a failure shows why, "1 error".
		{&hubapi.FilesDone{Op: "download", Copied: 2, Cancelled: true, Reason: "cancelled"}, "", "Cancelled · downloaded 2"},
		{&hubapi.FilesDone{Op: "upload", Reason: "the upload folder is missing or not a folder"}, "", "Failed: the upload folder is missing or not a folder · uploaded 0"},
		{&hubapi.FilesDone{Op: "upload", Copied: 1, ErrorCount: 1}, "", "Uploaded 1 · 1 error"},
	} {
		if got := DoneSummary(tc.d, tc.err); got != tc.want {
			t.Errorf("%+v %q: %q, want %q", tc.d, tc.err, got, tc.want)
		}
	}
}

func TestFilesTexts(t *testing.T) {
	if JobLabel([]string{"proj"}) != "proj" || JobLabel([]string{"a", "b"}) != "2 items" {
		t.Fatal(JobLabel([]string{"proj"}), JobLabel([]string{"a", "b"}))
	}
	if ConflictTitle(1) != "1 file already exists" || ConflictTitle(2) != "2 files already exist" {
		t.Fatal(ConflictTitle(1), ConflictTitle(2))
	}
	p := hubapi.FilesPlanned{Files: 1, Dirs: 2, Links: 0, Bytes: 1536}
	if got := DeleteSummary(p); got != "1 file, 2 folders, 0 links · 1.5 KB. There is no undo." {
		t.Fatal(got)
	}
	if BadNote(1) != "1 name is not shown: not safe to display or copy." || BadNote(3) != "3 names are not shown: not safe to display or copy." {
		t.Fatal(BadNote(1), BadNote(3))
	}
	if got := UploadTitle("box", "/home/u"); got != "Upload to box:/home/u" {
		t.Fatal(got)
	}
	if got := UploadTitle("b\u202eox", "/x"); got != `Upload to b\u202eox:/x` {
		t.Fatal(got)
	}
	if got := DownloadTitle("box"); got != "Download from box to\u2026" {
		t.Fatal(got)
	}
	if got := ProgressText(1536, 5<<20, "proj/a.txt"); got != "1.5 KB of 5.0 MB · proj/a.txt" {
		t.Fatal(got)
	}
}
