//go:build unix

package view

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/internal/hubtest"
	"github.com/lang315/sshgate/app/model"
)

// Ports of desktop/e2e/autoallow.spec.ts and autoallow-mcp.spec.ts. Every
// AI request goes through the real MCP bridge; autoallow.spec.ts sent its
// own straight to the door, with the same assertions.

// autoLabel is the Auto-allow select's text for mode.
func autoLabel(mode string) string {
	if m, ok := model.AutoModeOf(mode); ok {
		return m.Label
	}
	return "Off"
}

// editAuto opens server's editor, chooses mode (a select label) in AI
// access, turns on each switch or opt-in named in on, and presses Save:
// the e2e's editAuto. Opt-ins not named stay as they are.
func (f *flowT) editAuto(server, mode string, on ...string) {
	f.t.Helper()
	f.click("Hosts")
	f.click("Edit " + server)
	f.until(func() bool { return f.a.editor != nil }, "the host editor")
	openAI(f.t, f.tt)
	d := &f.a.editor.draft
	if cur := autoLabel(d.AutoAllow); cur != mode {
		choose(f.t, f.tt, cur, mode)
	}
	for _, l := range on {
		if !map[string]bool{"Visible to AI": d.AIVisible, "Allow on root hosts": d.AutoAllowRoot, "Also auto-allow sudo-exec": d.AutoAllowSudo}[l] {
			f.click(l)
			f.tt.Frame()
		}
	}
	f.click("Save")
}

// enable clicks the Auto-allow dialog's Enable once it may: sshtestd cannot
// answer the root check, and the failure landing restarts the delay, so
// it waits for that first.
func (f *flowT) enable() {
	f.t.Helper()
	f.until(func() bool { return f.tt.HasText("Could not check this host's sudo access.") }, "the root check")
	f.pass()
	f.click("Enable")
	f.until(func() bool { return f.a.editor == nil }, "the editor to close after Enable")
}

// chip waits until server's card shows a chip matching want ("" for none).
// The hub dates a timed grant by the real clock, so the app's clock moves up
// to it on each look; it never moves back.
func (f *flowT) chip(server, want string) {
	f.t.Helper()
	re := regexp.MustCompile("^" + want + "$")
	f.until(func() bool {
		if now := time.Now(); now.After(f.c.t) {
			f.c.t = now
			f.tt.Frame()
		}
		s := f.a.server(server)
		label := ""
		if s != nil {
			label = model.ChipLabel(s.AutoAllow, f.a.Now())
		}
		return s != nil && re.MatchString(label) && (label == "" || f.tt.HasText(label))
	}, server+"'s chip "+strconv.Quote(want))
}

// denyWaiting waits for cmd in the AI column, then denies it with Enter in
// its reason field: the e2e's waitThenDecide(deny).
func (f *flowT) denyWaiting(ch <-chan hubtest.ExecResult, cmd string) {
	f.t.Helper()
	f.until(func() bool { return f.tt.HasText(cmd) && len(f.a.Items()) == 1 }, cmd+" waiting in the AI column")
	f.click("Reason (optional)")
	f.tt.Key(0, ui.KeyEnter)
	f.denied(ch, "Denied by user")
}

// newHost adds a host on sshtestd through the editor.
func (f *flowT) newHost(label, user, password string, visible bool) {
	f.t.Helper()
	f.click("Hosts")
	f.click("New host")
	f.fill("Label", label)
	f.fill("Address", "127.0.0.1")
	f.fill("Port", strconv.Itoa(f.port))
	f.fill("User", user)
	f.fill("Password", password)
	if visible {
		openAI(f.t, f.tt)
		f.click("Visible to AI")
	}
	f.saveEditor()
}

// stopAndLock locks from the ready screen and ends every grant from the
// unlock screen, then unlocks again.
func (f *flowT) stopAndLock() {
	f.t.Helper()
	f.click("Lock")
	f.until(func() bool { return f.tt.HasText("Stop auto-allow and lock") }, "the soft-lock screen")
	f.click("Stop auto-allow and lock")
	f.until(func() bool { return !f.tt.HasText("AI auto-allow is still running on:") }, "the grants to end")
	f.unlock()
}

// TestFlowAutoAllow: autoallow.spec.ts, its three tests in order.
func TestFlowAutoAllow(t *testing.T) {
	f := flow(t)
	f.unlock()
	f.tt.SetSize(1000, 900)

	// A timed grant runs exec without a click; Stop on the card and Stop
	// auto-allow and lock end it.
	f.editAuto("box", "15 min")
	f.until(func() bool { return f.tt.HasText("Auto-allow AI commands on box") }, "the Auto-allow dialog")
	f.enable()
	f.chip("box", "Auto 1[45]m")
	f.ran(f.aiExec("box", "echo auto-one"), "echo auto-one")
	f.click("AI requests")
	f.until(func() bool { return f.tt.HasText("Auto-allowed") && f.tt.HasText("echo auto-one") }, "the run in the Auto-allowed feed")
	f.click("Stop auto-allow box")
	f.chip("box", "")
	f.denyWaiting(f.aiExec("box", "echo must-ask"), "echo must-ask")

	f.editAuto("box", "15 min")
	f.enable()
	f.chip("box", "Auto 1[45]m")
	f.stopAndLock()
	f.chip("box", "")
	f.denyWaiting(f.aiExec("box", "echo after-lock"), "echo after-lock")

	// A forever grant pauses after Stop auto-allow and lock, until Resume.
	f.armForever()
	f.stopAndLock()
	f.until(func() bool { return f.tt.HasText("Auto-allow is paused on box.") }, "the paused banner")
	f.denyWaiting(f.aiExec("box", "echo paused-asks"), "echo paused-asks")
	f.pass()
	f.click("Resume")
	f.chip("box", "Auto ∞") // armed before the next exec reaches the hub
	f.ran(f.aiExec("box", "echo resumed"), "echo resumed")
	f.click("Stop auto-allow box")
	f.chip("box", "")

	// The sudo-exec opt-in runs sudo-exec without a click.
	f.editAuto("box", "15 min", "Also auto-allow sudo-exec")
	f.until(func() bool {
		return f.tt.HasText("sudo-exec will run without asking: the AI has full root on this host")
	}, "the sudo warning")
	f.enable()
	f.chip("box", "Auto 1[45]m")
	f.ran(f.aiSudo("box", "echo sudo-auto"), "echo sudo-auto")
	f.click("Stop auto-allow box")
	f.chip("box", "")
}

// TestFlowAutoAllowMCP: autoallow-mcp.spec.ts but its root-host test
// (TestFlowAutoAllowRootHost), each step checked against the audit log.
func TestFlowAutoAllowMCP(t *testing.T) {
	f := flow(t)
	f.unlock()
	f.tt.SetSize(1000, 900)

	// Baseline: an MCP exec waits for a human decision.
	out := f.aiExec("box", "echo base")
	f.until(func() bool { return f.tt.HasText("echo base") && len(f.a.Items()) == 1 }, "echo base waiting")
	f.pass()
	f.click("Allow")
	f.ran(out, "echo base")
	if r := f.lastExec("echo base"); r.Outcome != "allowed" || r.Approval != "" || r.WaitMs <= 0 {
		t.Fatalf("audit of echo base: %+v", r)
	}

	// stdin: the card shows it, Send to tab is off, and the command gets it.
	out = f.tool("exec", map[string]any{"server": "box", "command": "stdin-echo", "stdin": "line one\nline two\n", "description": "flow"})
	f.until(func() bool { return f.tt.HasText("stdin · 18 bytes, 2 lines") && f.tt.HasText("line two") }, "the stdin card")
	f.pass()
	f.click("Send to tab")
	f.tt.flush()
	if len(f.a.tabs.Tabs) != 0 || f.tt.HasText("Send to tab is not available for a command with stdin") {
		t.Fatal("Send to tab worked for a request with stdin")
	}
	f.click("Allow")
	f.ran(out, "line one\nline two")
	if r := f.lastExec("stdin-echo"); r.Outcome != "allowed" || r.Stdin != "line one\nline two\n" {
		t.Fatalf("audit of stdin-echo: %+v", r)
	}

	// A timed grant from the editor: MCP execs run with no decision;
	// sudo-exec still asks.
	f.editAuto("box", "15 min")
	f.enable()
	f.chip("box", "Auto 1[45]m")
	if on := f.lastConfig("box", "autoAllowOn"); on.Until == "" || on.Forever {
		t.Fatalf("autoAllowOn: %+v", on)
	}
	f.ran(f.aiExec("box", "echo auto-1"), "echo auto-1")
	f.ran(f.aiExec("box", "echo auto-2"), "echo auto-2")
	f.auto("echo auto-1")
	f.auto("echo auto-2")
	f.until(func() bool { return f.tt.HasText("echo auto-2") }, "echo auto-2 in the Auto-allowed feed")
	f.denyWaiting(f.aiSudo("box", "echo sudo-asks"), "echo sudo-asks")
	if r := f.lastExec("echo sudo-asks"); r.Outcome != "denied" || !r.Sudo {
		t.Fatalf("audit of echo sudo-asks: %+v", r)
	}

	// stdin under the grant runs with no decision and is audited.
	f.ran(f.tool("exec", map[string]any{"server": "box", "command": "stdin-echo", "stdin": "auto in\n", "description": "flow"}), "auto in")
	if r := f.lastExec("stdin-echo"); r.Outcome != "allowed" || r.Approval != "auto" || r.Stdin != "auto in\n" {
		t.Fatalf("audit of stdin-echo under the grant: %+v", r)
	}
	f.until(func() bool { return f.tt.HasText("stdin 8 B") }, "the stdin size in the feed")

	// The sudo-exec opt-in: sudo-exec runs with no decision and is tagged.
	f.editAuto("box", "15 min", "Also auto-allow sudo-exec")
	f.until(func() bool {
		return f.tt.HasText("sudo-exec will run without asking: the AI has full root on this host")
	}, "the sudo warning")
	f.enable()
	if save := f.lastConfig("box", "save"); !strings.Contains(strings.Join(save.Changed, " "), "autoAllowSudo: false → true") {
		t.Fatalf("save record: %+v", save)
	}
	f.ran(f.aiSudo("box", "echo sudo-auto"), "echo sudo-auto")
	if r := f.lastExec("echo sudo-auto"); r.Outcome != "allowed" || r.Approval != "auto" || !r.Sudo {
		t.Fatalf("audit of echo sudo-auto: %+v", r)
	}
	f.until(func() bool { return count(f.tt.Texts(), "sudo") == 1 }, "the feed's sudo tag") // the tag, not the command's text

	// Stop on the card ends the grant.
	f.click("Hosts")
	f.click("Stop auto-allow box")
	f.chip("box", "")
	if off := f.lastConfig("box", "autoAllowOff"); off.Reason != "turned off" {
		t.Fatalf("autoAllowOff: %+v", off)
	}
	f.denyWaiting(f.aiExec("box", "echo after-stop"), "echo after-stop")

	// Stop auto-allow and lock ends a timed grant.
	f.editAuto("box", "15 min")
	f.enable()
	f.chip("box", "Auto 1[45]m")
	f.stopAndLock()
	f.chip("box", "")
	if off := f.lastConfig("box", "autoAllowOff"); off.Reason != "locked" {
		t.Fatalf("autoAllowOff: %+v", off)
	}
	f.denyWaiting(f.aiExec("box", "echo after-lock"), "echo after-lock")
	if r := f.lastExec("echo after-lock"); r.Approval != "" {
		t.Fatalf("audit of echo after-lock: %+v", r)
	}

	// A forever grant is paused after Stop auto-allow and lock until Resume.
	f.armForever()
	if on := f.lastConfig("box", "autoAllowOn"); !on.Forever {
		t.Fatalf("autoAllowOn: %+v", on)
	}
	f.stopAndLock()
	f.until(func() bool { return f.tt.HasText("Auto-allow is paused on box.") }, "the paused banner")
	f.denyWaiting(f.aiExec("box", "echo paused-asks"), "echo paused-asks")
	f.pass()
	f.click("Resume")
	f.chip("box", "Auto ∞")
	f.ran(f.aiExec("box", "echo resumed"), "echo resumed")
	f.auto("echo resumed")
	f.click("Stop auto-allow box")
	f.chip("box", "")
}

// TestFlowAutoAllowRootHost: autoallow-mcp.spec.ts's root-host test. A
// root login is refused until "Allow on root hosts" is ticked. Pinning the
// new host opens a terminal, so it needs the terminal library.
func TestFlowAutoAllowRootHost(t *testing.T) {
	needLib(t)
	f := flow(t)
	f.unlock()
	f.tt.SetSize(1000, 900)
	f.newHost("rootbox", "root", "rootpass", false)
	f.click("Open rootbox")
	f.trust("root")
	rootOn := func() int {
		n := 0
		for _, r := range f.records() {
			if r.Kind == "config" && r.Action == "autoAllowOn" && r.Server == "rootbox" {
				n++
			}
		}
		return n
	}

	// The hub listed rootbox as not visible to AI, so the app cannot foresee
	// the root refusal: the save stands and the app says so.
	f.editAuto("rootbox", "15 min", "Visible to AI")
	f.enable()
	f.until(func() bool {
		return f.tt.HasText("Saved, but auto-allow was not turned on: auto-allow refused: root login")
	}, "the saved-but alert")
	if save := f.lastConfig("rootbox", "save"); !strings.Contains(strings.Join(save.Changed, " "), "aiVisible: false → true") || rootOn() != 0 {
		t.Fatalf("save record %+v, autoAllowOn records %d", save, rootOn())
	}
	f.chip("rootbox", "")

	// Now visible, the refusal shows up front and Enable stays disabled.
	f.editAuto("rootbox", "15 min")
	f.until(func() bool { return f.tt.HasText("Auto-allow is not available here: root login") }, "the refusal")
	f.pass()
	f.click("Enable")
	f.tt.flush()
	if f.a.editor == nil || f.a.editor.confirm == nil || f.a.editor.busy {
		t.Fatal("Enable worked on a refused host")
	}
	f.click("Cancel")
	f.click("Close")
	f.until(func() bool { return f.a.editor == nil }, "the editor to close")
	if rootOn() != 0 {
		t.Fatal("a refused host was armed")
	}

	f.editAuto("rootbox", "15 min", "Allow on root hosts")
	f.until(func() bool { return f.tt.HasText("Allowing root") }, "the root warning")
	f.enable()
	f.chip("rootbox", "Auto 1[45]m")
	f.ran(f.aiExec("rootbox", "echo root-auto"), "echo root-auto")
	f.auto("echo root-auto")
	f.click("Stop auto-allow rootbox")
	f.chip("rootbox", "")
}
