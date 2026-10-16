//go:build unix

package view

import (
	"slices"
	"strings"
	"testing"

	"github.com/lang315/sshgate/app/internal/hubtest"
	"github.com/lang315/sshgate/app/model"
)

// auditExec sends cmd as request id through the MCP door, with the
// description the e2e gives.
func (f *flowT) auditExec(id, cmd string) <-chan hubtest.ExecResult {
	return hubtest.Exec(f.sock, map[string]any{"requestId": id, "client": "e2e", "server": "box", "command": cmd, "description": "audit e2e"})
}

// auditViews is the Audit tab's rows as listed, newest first.
func (f *flowT) auditViews() []model.RowView {
	var out []model.RowView
	for _, e := range f.a.audit.list.Entries {
		out = append(out, model.AuditRowView(e.Rec, f.a.Now()))
	}
	return out
}

// auditRows counts the rows whose main text is main.
func (f *flowT) auditRows(main string) int {
	n := 0
	for _, v := range f.auditViews() {
		if v.Main == main {
			n++
		}
	}
	return n
}

// auditFirst waits until the newest row's main text is main, and it shows.
func (f *flowT) auditFirst(main string) {
	f.t.Helper()
	f.until(func() bool {
		v := f.auditViews()
		return len(v) > 0 && v[0].Main == main && f.tt.HasText(main)
	}, main+" as the newest audit row")
}

// TestFlowAudit: audit.spec.ts. The Audit tab lists, filters, expands,
// updates live, and reads again after a lock.
func TestFlowAudit(t *testing.T) {
	f := flow(t)
	f.unlock()
	f.denyWaiting(f.auditExec("d1", "echo audit-denied"), "echo audit-denied")

	// Auto-allow box (the editor's Save also writes a save record), run one exec, stop.
	f.editAuto("box", "15 min")
	f.enable()
	f.chip("box", "Auto 1[45]m")
	f.ran(f.auditExec("a1", "echo audit-auto"), "echo audit-auto")
	f.click("Stop auto-allow box")
	f.chip("box", "")

	// Everything is listed, newest first.
	f.tt.SetSize(1100, 900)
	f.click("Audit")
	f.until(func() bool { return f.auditRows("echo audit-auto") == 1 && f.tt.HasText("echo audit-auto") }, "the audit rows")
	if f.auditRows("echo audit-denied") != 1 || !f.tt.HasText("echo audit-denied") {
		t.Fatalf("no denied row: %q", f.tt.Texts())
	}
	saves, grants := 0, 0
	for _, v := range f.auditViews() {
		if len(v.Badges) == 1 && v.Badges[0].Text == "save" {
			saves++
		}
		if len(v.Badges) == 1 && v.Badges[0].Text == "auto allow on" && (v.Main == "15m" || v.Main == "14m") {
			grants++
		}
	}
	if saves != 1 || grants != 1 || !f.tt.HasText("auto allow on") || !f.tt.HasText("save") {
		t.Fatalf("save rows %d, grant rows %d: %q", saves, grants, f.tt.Texts())
	}
	var seqs []int
	for _, e := range f.a.audit.list.Entries {
		seqs = append(seqs, e.Seq)
	}
	if !slices.IsSortedFunc(seqs, func(a, b int) int { return b - a }) {
		t.Fatalf("not newest first: %v", seqs)
	}
	texts := f.tt.Texts()
	if slices.Index(texts, "echo audit-auto") > slices.Index(texts, "echo audit-denied") {
		t.Fatalf("the auto run is not above the denied one: %q", texts)
	}
	if !strings.Contains(strings.Join(texts, "\n"), "audit.jsonl") {
		t.Fatalf("no audit file in the footer: %q", texts)
	}

	// Auto shows only the auto-allowed run.
	f.click("Filter Auto")
	f.until(func() bool { return len(f.a.audit.list.Entries) == 1 && f.a.audit.list.Status == "loaded" }, "the Auto filter")
	if v := f.auditViews(); v[0].Main != "echo audit-auto" || f.tt.HasText("echo audit-denied") {
		t.Fatalf("Auto shows %+v", v)
	}
	f.click("Filter Auto")
	f.until(func() bool { return f.auditRows("echo audit-denied") == 1 && f.tt.HasText("echo audit-denied") }, "every record again")

	// Expanding a row shows the full command and the AI's description.
	f.click("echo audit-denied")
	f.until(func() bool { return f.tt.HasText("AI's description · unverified") && f.tt.HasText("audit e2e") }, "the denied row's detail")

	// With the tab open, a new record appears without Refresh.
	gen := f.a.audit.gen // every read bumps it
	f.denyWaiting(f.auditExec("l1", "echo audit-live"), "echo audit-live")
	f.auditFirst("echo audit-live")
	if f.a.audit.gen != gen {
		t.Fatal("a live record read the log again")
	}

	// Lock drops the records; unlocking reads them again.
	f.click("Lock")
	f.until(func() bool { return f.tt.HasText("Master password") }, "the unlock screen")
	if n := len(f.a.audit.list.Entries); n != 0 || f.tt.HasText("echo audit-live") {
		t.Fatalf("%d records kept across the lock", n)
	}
	f.unlock()
	f.auditFirst("echo audit-live")
	if f.auditRows("echo audit-denied") != 1 {
		t.Fatal("the denied row is gone after the unlock")
	}
}
