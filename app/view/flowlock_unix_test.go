//go:build unix

package view

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/model"
)

// Ports of desktop/e2e/settings.spec.ts, manuallock.spec.ts and
// softlock.spec.ts, through the real MCP bridge.

// next is the text shown right after s, or "".
func next(texts []string, s string) string {
	if i := slices.Index(texts, s); i >= 0 && i+1 < len(texts) {
		return texts[i+1]
	}
	return ""
}

// armForever arms box forever through the editor.
func (f *flowT) armForever() {
	f.t.Helper()
	f.editAuto("box", "Until turned off")
	f.fill("Type box to confirm", "box")
	f.enable()
	f.chip("box", "Auto ∞")
}

// softLockBefore checks the audit log has a softLock record for box with
// reason (any when ""), older than cmd's run, which was auto-allowed.
func (f *flowT) softLockBefore(cmd, reason string) {
	f.t.Helper()
	recs := f.records()
	soft := last(recs, func(r auditRec) bool { return r.Kind == "config" && r.Action == "softLock" })
	ran := last(recs, func(r auditRec) bool { return r.Kind == "" && r.Command == cmd })
	if soft < 0 || ran < soft || !slices.Equal(recs[soft].Servers, []string{"box"}) || reason != "" && recs[soft].Reason != reason {
		f.t.Fatalf("softLock record %d, run %d in %+v", soft, ran, recs)
	}
	if r := recs[ran]; r.Outcome != "allowed" || r.Approval != "auto" {
		f.t.Fatalf("run of %q: %+v", cmd, r)
	}
}

// TestFlowSettings: settings.spec.ts. Its colour checks (the amber and red
// notices, Save's danger outline) are not ported: the Tester shows no colour.
func TestFlowSettings(t *testing.T) {
	f := flow(t)
	f.until(func() bool { return f.tt.HasText("Master password") }, "the unlock screen")
	if _, ok := f.tt.Find("Settings"); ok {
		t.Fatal("Settings shows while locked")
	}
	f.unlock()
	f.tt.SetSize(1000, 900)
	f.armForever()

	f.click("Settings")
	f.until(func() bool { return f.tt.HasText("Keep running while locked") && f.tt.HasText("24 hours (default)") }, "Settings on 24 hours")
	f.tt.Key(0, ui.KeyEnter) // Cancel has the focus
	f.until(func() bool { return f.a.settings == nil }, "Enter to cancel")

	f.click("Settings")
	f.until(func() bool { return f.a.settings != nil }, "Settings")
	choose(t, f.tt, "24 hours (default)", "7 days")
	notice := model.LimitNotice("7d") + " On auto-allow now:"
	if !f.tt.HasText(notice) || next(f.tt.Texts(), notice) != "box" {
		t.Fatalf("7 days: %q", f.tt.Texts())
	}
	choose(t, f.tt, "7 days", "No limit")
	f.click("Save") // the notice just changed: Save waits
	f.tt.flush()
	notice = model.LimitNotice("none") + " On auto-allow now:"
	if f.a.settings == nil || !f.tt.HasText(notice) || next(f.tt.Texts(), notice) != "box" {
		t.Fatalf("No limit: %q", f.tt.Texts())
	}
	f.pass()
	f.click("Save")
	f.until(func() bool { return f.a.settings == nil }, "Save to close Settings")
	b, err := os.ReadFile(filepath.Join(filepath.Dir(f.audit), "servers.json"))
	var vault struct{ SoftLockLimit string }
	if err != nil || json.Unmarshal(b, &vault) != nil || vault.SoftLockLimit != "none" {
		t.Fatalf("vault softLockLimit %q, %v", vault.SoftLockLimit, err)
	}
	if r := f.lastConfig("", "softLockLimit"); r.Limit != "none" {
		t.Fatalf("softLockLimit record %+v", r)
	}

	// The value survives a lock, and the consent text follows it.
	f.click("Lock")
	f.until(func() bool { return f.tt.HasText("Master password") }, "the lock screen")
	if _, ok := f.tt.Find("Settings"); ok {
		t.Fatal("Settings shows while locked")
	}
	f.unlock()
	f.click("Settings")
	f.until(func() bool { return f.tt.HasText("No limit") }, "Settings on No limit")
	f.tt.Key(0, ui.KeyEscape)
	f.until(func() bool { return f.a.settings == nil }, "Esc to cancel")
	f.editAuto("box", "Off")
	f.until(func() bool { return f.a.editor == nil }, "the editor to close without a dialog")
	f.editAuto("box", "Until turned off")
	f.until(func() bool { return f.tt.HasText("Auto-allow AI commands on box") }, "the Auto-allow dialog")
	if !f.tt.HasText("with no time limit") || f.tt.HasText("24 hours") {
		t.Fatalf("consent text: %q", f.tt.Texts())
	}
	f.click("Cancel")
}

// TestFlowManualLock: manuallock.spec.ts. The Lock button's tooltip is
// model.LockTitle's (TestLockTitleAndAIButton): the Tester shows no tooltip.
func TestFlowManualLock(t *testing.T) {
	f := flow(t)
	f.unlock()
	f.tt.SetSize(1000, 900)
	f.armForever()
	f.click("Lock")
	f.until(func() bool {
		return f.tt.HasText("Master password") && f.tt.HasText("AI auto-allow is still running on: box")
	}, "the soft-lock screen")
	f.ran(f.aiExec("box", "echo manual-1"), "echo manual-1")
	f.softLockBefore("echo manual-1", "manual")

	// The unlock screen's stop still ends it.
	f.click("Stop auto-allow and lock")
	f.until(func() bool { return !f.tt.HasText("AI auto-allow is still running on: box") }, "the auto-allow list to go")
	f.denied(f.aiExec("box", "echo manual-2"), "Vault is locked")
	if off := f.lastConfig("box", "autoAllowOff"); off.Reason != "locked" {
		t.Fatalf("autoAllowOff: %+v", off)
	}
}

// TestFlowSoftLock: softlock.spec.ts, with the idle lock at 3 s (the e2e's
// 5 s; arming through the UI makes a hub call at least every second here).
// The second host is not pinned: nothing the spec checks on it needs a pin,
// and pinning would open a terminal (TestFlowHostsCreateTrustEditForgetDelete
// covers that).
func TestFlowSoftLock(t *testing.T) {
	f := flow(t, "--idleLock=3s")
	f.unlock()
	f.tt.SetSize(1000, 900)
	f.newHost("other", "u2", "x", true)
	f.armForever()

	// Idle with a grant: the app locks and the AI keeps running on box only.
	f.idleLocked("box")
	f.ran(f.aiExec("box", "echo soft-1"), "echo soft-1")
	f.softLockBefore("echo soft-1", "idle")
	f.denied(f.aiExec("other", "echo soft-other"), "Vault is locked")
	if last(f.records(), func(r auditRec) bool { return r.Command == "echo soft-other" }) >= 0 {
		t.Fatal("the refused run on other was audited")
	}
	list := f.result(f.tool("list-servers", map[string]any{})).Stdout
	if !strings.Contains(list, "- other (127.0.0.1) [locked: unlock the app]") || !strings.Contains(list, "- box (127.0.0.1)\n") || strings.Contains(list, "box (127.0.0.1) [locked") {
		t.Fatalf("list-servers: %q", list)
	}

	// Unlock keeps the grant and says what ran.
	f.unlock()
	f.until(func() bool { return f.tt.HasText("While the app was locked the AI ran 1 command on box") }, "the ran-while-locked banner")
	if f.tt.HasText("Auto-allow is paused") {
		t.Fatal("the grant paused across a soft lock")
	}
	f.click("Hosts")
	f.chip("box", "Auto ∞")
	f.ran(f.aiExec("box", "echo soft-2"), "echo soft-2")
	f.auto("echo soft-2")

	// The unlock screen stops auto-allow without the password.
	f.idleLocked("box")
	f.click("Stop auto-allow and lock")
	f.until(func() bool { return !f.tt.HasText("AI auto-allow is still running on: box") }, "the auto-allow list to go")
	f.denied(f.aiExec("box", "echo soft-3"), "Vault is locked")
	if off := f.lastConfig("box", "autoAllowOff"); off.Reason != "locked" {
		t.Fatalf("autoAllowOff: %+v", off)
	}
}
