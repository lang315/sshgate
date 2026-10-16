package view

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/model"
)

func dir(name string) hubapi.FileEntry { return hubapi.FileEntry{Name: name, Kind: "dir", Mode: 0o755} }
func file(name string) hubapi.FileEntry {
	return hubapi.FileEntry{Name: name, Kind: "file", Size: 5, Mode: 0o644}
}

// listing answers files.list from dirs ("" is /home); a folder not in dirs
// fails as the hub does.
func listing(dirs map[string][]hubapi.FileEntry) func(string, *hubapi.Trust) (hubapi.FileListing, error) {
	return func(p string, _ *hubapi.Trust) (hubapi.FileListing, error) {
		if p == "" {
			p = "/home"
		}
		es, ok := dirs[p]
		if !ok {
			return hubapi.FileListing{}, errors.New("file does not exist")
		}
		return hubapi.FileListing{HostKey: hubapi.HostKey{Status: "listed"}, Path: p, Entries: es}, nil
	}
}

// filesApp is a ready app on box whose files.list answers from dirs.
func filesApp(t *testing.T, dirs map[string][]hubapi.FileEntry) (*App, *tester, *fakeDoor, *clock, *fakeShell) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}, list: listing(dirs)}
	a, tt, c, sh := newApp(t, d)
	tt.SetSize(1000, 600)
	tt.flush()
	return a, tt, d, c, sh
}

// openFilesTab clicks the card's Files button and waits for the listing.
func openFilesTab(t *testing.T, a *App, tt *tester) (string, *filesTab) {
	t.Helper()
	if a.tabs.Active != "" {
		clickT(t, tt, "Hosts")
	}
	clickT(t, tt, "Files box")
	tt.flush()
	id := a.tabs.Active
	v := a.files[id]
	if v == nil || v.loading {
		t.Fatalf("no Files tab listed: %q", tt.Texts())
	}
	return id, v
}

func TestFilesTabListsHomeAndNavigates(t *testing.T) {
	a, tt, d, _, _ := filesApp(t, map[string][]hubapi.FileEntry{
		"/home":      {file("a.txt"), dir("proj"), file(".env")},
		"/home/proj": {file("b.txt")},
		"/":          {dir("home")},
	})
	d.resetCalls()
	id, v := openFilesTab(t, a, tt)
	if v.pathIn != "/home" || params(d, "files.list") != `{"server":"box","path":""}` {
		t.Fatalf("path %q, params %s", v.pathIn, params(d, "files.list"))
	}
	if tab, _ := a.tabs.Get(id); tab.Kind != model.FilesKind || !tt.HasText("box · files") || !tt.HasText("Files on box") {
		t.Fatalf("%+v %q", tab, tt.Texts())
	}
	if !tt.HasText("proj") || !tt.HasText("a.txt") || tt.HasText(".env") || !tt.HasText("drwxr-xr-x") {
		t.Fatalf("rows %q", tt.Texts())
	}
	if names := []string{v.rows[0].Name, v.rows[1].Name}; names[0] != "proj" || names[1] != "a.txt" {
		t.Fatalf("folders first: %v", names)
	}
	clickT(t, tt, "Show hidden files")
	if !tt.HasText(".env") {
		t.Fatal("Show hidden files left .env out")
	}
	clickT(t, tt, "proj")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if v.shown != "/home/proj" || !tt.HasText("b.txt") || v.selection.Len() != 0 {
		t.Fatalf("Enter on a folder: %q %v", v.shown, tt.Texts())
	}
	clickT(t, tt, "b.txt")
	tt.Key(0, ui.KeyEnter) // a file does not open
	tt.Key(0, ui.KeyBackspace)
	tt.flush()
	if v.shown != "/home" {
		t.Fatalf("Backspace went to %q", v.shown)
	}
	clickT(t, tt, "Up")
	tt.flush()
	if v.shown != "/" || v.pathIn != "/" {
		t.Fatalf("Up went to %q", v.shown)
	}
	fill(t, tt, "Path", "/nowhere")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if v.listing != nil || !tt.HasText("file does not exist") || v.pathIn != "/nowhere" {
		t.Fatalf("a failed load: %+v %q", v.listing, tt.Texts())
	}
	if model.ActionsReady(v.listing, v.loading) {
		t.Fatal("actions ready without a listing")
	}
}

// files.spec.ts test 2: the dialog keeps the folder it opened in. mygo's
// modal takes the pointer, so the navigation under it goes through the
// model, as a Shift+Tab to the Path box did in Electron.
func TestNewFolderGoesWhereItOpened(t *testing.T) {
	a, tt, d, _, _ := filesApp(t, map[string][]hubapi.FileEntry{"/home": {dir("elsewhere")}, "/home/elsewhere": {}})
	id, v := openFilesTab(t, a, tt)
	clickT(t, tt, "New folder")
	if !tt.Focused("Name") {
		t.Fatalf("Name not focused: %q", tt.Texts())
	}
	tt.Type("trap")
	a.Update(func() { a.loadFiles(id, "/home/elsewhere", nil, false) })
	tt.flush()
	if v.shown != "/home/elsewhere" {
		t.Fatal(v.shown)
	}
	d.resetCalls()
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if params(d, "files.mkdir") != `{"path":"/home/trap","server":"box"}` || v.name != nil {
		t.Fatalf("mkdir %s, dialog %+v", params(d, "files.mkdir"), v.name)
	}
	if hasCall(d, "files.list") || v.shown != "/home/elsewhere" {
		t.Fatalf("relisted a folder no longer shown: %v %q", d.callList(), v.shown)
	}
	if !tt.Focused("Files on box") {
		t.Fatal("the table did not get the focus back")
	}
}

func TestNameDialogErrorsAndCancel(t *testing.T) {
	a, tt, d, _, _ := filesApp(t, map[string][]hubapi.FileEntry{"/home": {dir("proj")}})
	_, v := openFilesTab(t, a, tt)
	clickT(t, tt, "New folder")
	d.resetCalls()
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if !tt.HasText("Enter a name.") || len(d.callList()) != 0 {
		t.Fatalf("an empty name: %q %v", tt.Texts(), d.callList())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if v.name != nil || !tt.Focused("Files on box") {
		t.Fatal("Esc did not close the dialog back to the table")
	}
	clickT(t, tt, "proj")
	clickT(t, tt, "Rename")
	if v.name == nil || v.name.value != "proj" || !tt.Focused("Name") {
		t.Fatalf("Rename opened %+v", v.name)
	}
	d.set(func() { d.errs = map[string]error{"files.rename": errors.New("already exists; not replaced")} })
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("proj2")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if params(d, "files.rename") != `{"from":"/home/proj","server":"box","to":"/home/proj2"}` || v.name == nil || !tt.HasText("already exists; not replaced") {
		t.Fatalf("rename %s, %q", params(d, "files.rename"), tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if v.name != nil {
		t.Fatal("Esc left the Rename dialog open")
	}
}

func TestRenameSucceedsAndRelists(t *testing.T) {
	a, tt, d, _, _ := filesApp(t, map[string][]hubapi.FileEntry{"/home": {dir("proj")}})
	_, v := openFilesTab(t, a, tt)
	clickT(t, tt, "proj")
	tt.Key(0, ui.KeyF2)
	tt.Frame()
	if v.name == nil || !v.name.rename {
		t.Fatal("F2 did not open Rename")
	}
	d.set(func() { d.list = listing(map[string][]hubapi.FileEntry{"/home": {dir("proj2")}}) })
	d.resetCalls()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("proj2")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if v.name != nil || !hasCall(d, "files.list") || !tt.HasText("proj2") {
		t.Fatalf("after rename: %v %q", d.callList(), tt.Texts())
	}
}

// A second Files tab lists the folder the last one showed, and home when
// that folder went away.
func TestFilesRemembersTheFolderThisRun(t *testing.T) {
	dirs := map[string][]hubapi.FileEntry{"/home": {dir("proj")}, "/home/proj": {}}
	a, tt, d, _, _ := filesApp(t, dirs)
	id, _ := openFilesTab(t, a, tt)
	a.Update(func() { a.loadFiles(id, "/home/proj", nil, false) })
	tt.flush()
	d.resetCalls()
	_, w := openFilesTab(t, a, tt)
	if w.shown != "/home/proj" || params(d, "files.list") != `{"server":"box","path":"/home/proj"}` {
		t.Fatalf("second tab at %q", w.shown)
	}
	delete(dirs, "/home/proj")
	_, x := openFilesTab(t, a, tt)
	if x.shown != "/home" || x.err != "" {
		t.Fatalf("third tab at %q, %q", x.shown, x.err)
	}
	if len(a.tabs.Tabs) != 3 || a.tabs.OpenCount("box") != 0 {
		t.Fatalf("tabs %+v", a.tabs.Tabs)
	}
}

func TestFilesHostKeyPromptRetriesTheListing(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{{Name: "box", Host: "h", Port: 22, User: "u"}}}
	d.list = func(p string, trust *hubapi.Trust) (hubapi.FileListing, error) {
		if trust == nil {
			return hubapi.FileListing{HostKey: hubapi.HostKey{Status: "hostKeyUnknown", Server: "box", Host: "h", Port: 22, User: "u", Fingerprint: "SHA256:new", KeyType: "ssh-ed25519", KnownHosts: "absent"}}, nil
		}
		return hubapi.FileListing{HostKey: hubapi.HostKey{Status: "listed"}, Path: "/home", Entries: []hubapi.FileEntry{}}, nil
	}
	a, tt, c, _ := newApp(t, d)
	tt.SetSize(1000, 600)
	tt.flush()
	clickT(t, tt, "Files box")
	tt.flush()
	v := a.files[a.tabs.Active]
	if !tt.HasText("Trust this host key?") || !v.loading {
		t.Fatalf("no prompt: %q", tt.Texts())
	}
	d.resetCalls()
	pass(c, tt)
	clickT(t, tt, "Trust and connect")
	tt.flush()
	if !strings.Contains(params(d, "files.list"), `"trustHostKey":{"fingerprint":"SHA256:new","keyType":"ssh-ed25519"}`) || !hasCall(d, "servers") || v.listing == nil {
		t.Fatalf("retry %s, calls %v", params(d, "files.list"), d.callList())
	}
	clickT(t, tt, "Refresh")
	tt.flush()
	clickT(t, tt, "Cancel")
	tt.flush()
	if v.listing != nil || v.err != "host key not trusted" || v.loading {
		t.Fatalf("Cancel: %+v %q", v.listing, v.err)
	}
}

func TestFilesHostKeyMismatch(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}}
	d.list = func(string, *hubapi.Trust) (hubapi.FileListing, error) {
		return hubapi.FileListing{HostKey: hubapi.HostKey{Status: "hostKeyMismatch", Server: "box", Host: "h", Port: 22, User: "u", Pinned: "SHA256:old", Presented: "SHA256:new"}}, nil
	}
	a, tt, _, _ := newApp(t, d)
	tt.SetSize(1000, 600)
	tt.flush()
	clickT(t, tt, "Files box")
	tt.flush()
	if v := a.files[a.tabs.Active]; !tt.HasText("Host key changed") || v.err != "host key mismatch" {
		t.Fatalf("%q", tt.Texts())
	}
}

// A hub stop drops a load in flight: its reply never comes.
func TestHubStopEndsALoadInFlight(t *testing.T) {
	a, tt, d, _, _ := filesApp(t, map[string][]hubapi.FileEntry{"/home": {}})
	d.set(func() { d.hang = map[string]bool{"files.list": true} })
	clickT(t, tt, "Files box")
	tt.flush()
	v := a.files[a.tabs.Active]
	if !v.loading {
		t.Fatal("not loading")
	}
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", In: time.Second}) })
	tt.flush()
	if v.loading || v.err != "hub restarted" {
		t.Fatalf("after a hub stop: loading %v, %q", v.loading, v.err)
	}
	if tab, _ := a.tabs.Get(a.tabs.Active); tab.State != "open" {
		t.Fatalf("a Files tab exited: %+v", tab)
	}
}
