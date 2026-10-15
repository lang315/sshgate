package view

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// ImportSheet.test.ts's rows: web ready and pinned, db ready without a pin
// and with a passphrase, jump skipped, box already a server.
var scanRows = hubapi.ImportScan{Candidates: []hubapi.Candidate{
	{Alias: "web", Host: "10.0.0.5", Port: 2200, User: "deploy", Auth: "key", KeyPath: "~/.ssh/id_web", HostKey: "SHA256:abc", HostKeyAlgo: "ssh-ed25519", Status: "ready"},
	{Alias: "db", Host: "10.0.0.6", Port: 22, User: "pg", Auth: "agent", NeedsPassphrase: true, Status: "ready"},
	{Alias: "jump", Host: "jump", Port: 22, User: "u", Auth: "agent", Status: "skipped", Reason: "needs ProxyJump"},
	{Alias: "box", Host: "h", Port: 22, User: "u", Auth: "agent", Status: "exists", Reason: "Already in vault"},
}}

func importApp(t *testing.T, scan hubapi.ImportScan) (*App, *tester, *fakeDoor) {
	a, tt, d := edReady(t, []hubapi.Server{boxServer})
	d.set(func() { d.scan = scan })
	d.resetCalls()
	clickT(t, tt, "Import from SSH config")
	tt.flush()
	return a, tt, d
}

func TestImportRowsShowTheScan(t *testing.T) {
	a, tt, _ := importApp(t, scanRows)
	for _, s := range []string{"deploy@10.0.0.5:2200 · ~/.ssh/id_web", "ssh-ed25519 SHA256:abc", "pg@10.0.0.6:22 · agent", "Key has a passphrase: add it in the editor after import.",
		"needs ProxyJump", "Skipped", "Already in vault", "Import web", "Import db", "Import 2 hosts"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if count(tt.Texts(), "not in known_hosts") != 1 || tt.HasText("Import jump") || tt.HasText("Import box") {
		t.Fatalf("only db lacks a pin, and only ready rows have a checkbox: %q", tt.Texts())
	}
	if !reflect.DeepEqual(a.imp.checked, map[string]bool{"web": true, "db": true}) || !tt.Focused("Close") {
		t.Fatalf("checked %v", a.imp.checked)
	}
}

func TestImportSendsCheckedReadyAliasesOnly(t *testing.T) {
	a, tt, d := importApp(t, scanRows)
	clickT(t, tt, "Import db")
	clickT(t, tt, "jump") // a row without a checkbox: nothing to check
	if !tt.HasText("Import 1 host") {
		t.Fatal(tt.Texts())
	}
	d.resetCalls()
	clickT(t, tt, "Import 1 host")
	tt.flush()
	if got := d.callList(); len(got) != 2 || got[0] != "import.apply" || got[1] != "servers" || params(d, "import.apply") != `{"aliases":["web"]}` {
		t.Fatalf("calls %v %s", got, params(d, "import.apply"))
	}
	if a.imp != nil || !tt.Focused("Search hosts") {
		t.Fatal("the sheet stays after a full import, or the focus went nowhere")
	}
}

func TestImportBeforeTheScanAnswers(t *testing.T) {
	a, tt, d := edReady(t, []hubapi.Server{boxServer})
	d.set(func() { d.hang = map[string]bool{"import.scan": true} })
	clickT(t, tt, "Import from SSH config")
	for _, s := range []string{"Close import", "StrictHostKeyChecking accept-new", "Reading your SSH config…", "Import 0 hosts"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	clickT(t, tt, "Import 0 hosts")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if hasCall(d, "import.apply") || a.imp == nil {
		t.Fatal("Import ran with nothing scanned, or Esc closed a busy sheet")
	}
}

func TestImportSkippedRescansKeepingTheChoice(t *testing.T) {
	a, tt, d := importApp(t, scanRows)
	clickT(t, tt, "Import db")
	d.set(func() {
		d.imported = hubapi.ImportResult{Imported: []string{}, Skipped: []hubapi.ImportSkip{{Alias: "web", Reason: "Already in vault"}}}
	})
	clickT(t, tt, "Import 1 host")
	tt.flush()
	if !tt.HasText("Not imported: web (Already in vault)") || count(d.callList(), "import.scan") != 2 {
		t.Fatalf("%q %v", tt.Texts(), d.callList())
	}
	if !reflect.DeepEqual(a.imp.checked, map[string]bool{"web": true}) {
		t.Fatal("the rescan lost the choice:", a.imp.checked)
	}
}

func TestImportNoteAndScanError(t *testing.T) {
	_, tt, d := importApp(t, hubapi.ImportScan{Note: "No ~/.ssh/config"})
	if !tt.HasText("No ~/.ssh/config") || !tt.HasText("Import 0 hosts") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Close")
	d.set(func() { d.errs = map[string]error{"import.scan": errors.New("OpenSSH client (ssh) not found")} })
	clickT(t, tt, "Import from SSH config")
	tt.flush()
	if !tt.HasText("OpenSSH client (ssh) not found") {
		t.Fatal(tt.Texts())
	}
}

func TestImportAndEditorTakeEachOthersPlace(t *testing.T) {
	a, tt, _ := importApp(t, scanRows)
	clickT(t, tt, "New host")
	if a.imp != nil || a.editor == nil {
		t.Fatal("New host left the import sheet open")
	}
	clickT(t, tt, "Import from SSH config")
	tt.flush()
	if a.editor != nil || a.imp == nil {
		t.Fatal("Import left the editor open")
	}
}

// An import reply that lands after its sheet closed leaves the editor
// opened since alone, typed secret and all.
func TestLateImportReplyLeavesANewEditorAlone(t *testing.T) {
	a, tt, d := importApp(t, scanRows)
	gate := make(chan struct{})
	d.set(func() { d.gates = map[string]chan struct{}{"import.apply": gate} })
	clickT(t, tt, "Import 2 hosts")
	clickT(t, tt, "Close")
	clickT(t, tt, "New host")
	clickT(t, tt, "Password")
	tt.Type("typed-secret")
	tt.Frame()
	close(gate) // the import succeeds now
	tt.flush()
	if a.imp != nil || a.editor == nil || a.editor.name != "" || a.editor.err != "" || a.alert != "" ||
		a.editor.draft.Secrets[model.SecretPassword].Value != "typed-secret" {
		t.Fatalf("the late reply closed or touched the new editor: imp %v, editor %+v, alert %q", a.imp != nil, a.editor, a.alert)
	}
}

// Scan and import replies that land behind the lock screen change nothing
// and ask the locked hub for nothing.
func TestImportRepliesAfterALockChangeNothing(t *testing.T) {
	for _, method := range []string{"import.apply", "import.scan"} {
		a, tt, d := importApp(t, scanRows)
		gate := make(chan struct{})
		d.set(func() { d.gates = map[string]chan struct{}{method: gate} })
		if method == "import.apply" {
			clickT(t, tt, "Import 2 hosts")
		} else {
			clickT(t, tt, "Close")
			clickT(t, tt, "Import from SSH config")
		}
		lockNow(t, a, tt, d)
		d.resetCalls()
		close(gate)
		tt.flush()
		if a.imp != nil || a.alert != "" || len(d.callList()) != 0 {
			t.Fatalf("%s: imp %v, alert %q, calls %v after a reply behind the lock", method, a.imp != nil, a.alert, d.callList())
		}
	}
}

// Untrusted text: an alias from ~/.ssh/config shows only escaped.
func TestImportShowsAliasesEscaped(t *testing.T) {
	_, tt, _ := importApp(t, hubapi.ImportScan{Candidates: []hubapi.Candidate{
		{Alias: "we\u202eb", Host: "h", Port: 22, User: "u", Auth: "agent", Status: "ready"},
		{Alias: "ju\u202emp", Host: "j", Port: 22, User: "u", Auth: "agent", Status: "skipped", Reason: "needs ProxyJump"},
	}})
	for _, s := range []string{`we\u202eb`, `Import we\u202eb`, `ju\u202emp`} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	for _, x := range tt.Texts() {
		if strings.ContainsRune(x, '\u202e') {
			t.Fatalf("%q shown unescaped", x)
		}
	}
}

// A rescan with nothing checked re-checks none: only the first scan checks
// every ready row. Here a partial import leaves nothing checked (web is in
// the vault now, db was unchecked), then the sheet scans again.
func TestRescanWithNothingCheckedChecksNothing(t *testing.T) {
	a, tt, d := importApp(t, scanRows)
	clickT(t, tt, "Import db")
	after := hubapi.ImportScan{Candidates: append([]hubapi.Candidate(nil), scanRows.Candidates...)}
	after.Candidates[0].Status = "exists"
	d.set(func() {
		d.scan = after
		d.imported = hubapi.ImportResult{Imported: []string{}, Skipped: []hubapi.ImportSkip{{Alias: "web", Reason: "Already in vault"}}}
	})
	clickT(t, tt, "Import 1 host")
	tt.flush()
	if a.imp == nil || len(a.imp.importAliases()) != 0 {
		t.Fatalf("after the partial import: %+v", a.imp)
	}
	a.Update(func() { a.scanImport(true) }) // the UI rescans only after an import, and Import is disabled at 0
	tt.flush()
	if a.imp.checked == nil || len(a.imp.importAliases()) != 0 || !tt.HasText("Import 0 hosts") {
		t.Fatalf("checked %v after a rescan", a.imp.checked)
	}
}
