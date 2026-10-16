//go:build unix

package view

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/internal/hubtest"
	"github.com/lang315/sshgate/app/model"
	"github.com/lang315/sshgate/app/touchid"
)

// touchFlow is flow with Touch ID on the fake helper: no finger, the "seal"
// is base64. The real helper is the live check, not a test. userdata is the
// app's own directory, so the real touchid.json is never touched.
type touchFlow struct {
	*flowT
	file, mode string
}

func newTouchFlow(t *testing.T) *touchFlow {
	t.Helper()
	f := flow(t)
	helper, err := filepath.Abs("../touchid/testdata/fake-touchid.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	tf := &touchFlow{flowT: f, file: filepath.Join(dir, "userdata", "touchid.json"), mode: filepath.Join(dir, "mode")}
	t.Setenv("FAKE_TOUCHID_MODE", tf.mode)
	f.a.Touch = &touchid.Manager{Helper: helper, File: tf.file, Hub: func() touchid.Hub { return f.a.hub() }}
	f.a.Update(f.a.loadTouch) // the lock screen was already up when Touch ID was set
	f.tt.flush()
	return tf
}

func (tf *touchFlow) setMode(m string) {
	tf.t.Helper()
	if err := os.WriteFile(tf.mode, []byte(m), 0o600); err != nil {
		tf.t.Fatal(err)
	}
}

func (tf *touchFlow) fileExists() bool { _, err := os.Stat(tf.file); return err == nil }

// focus is the window gaining the focus.
func (tf *touchFlow) focus() {
	tf.a.Update(func() { tf.a.WindowFocus(false); tf.a.WindowFocus(true) })
	tf.tt.flush()
}

// openSettingsAndTurnOn opens Settings, turns Touch ID on and types password
// pw, then waits out the guard; Save is a click away.
func (tf *touchFlow) openSettingsAndTurnOn(pw string) {
	tf.t.Helper()
	tf.click("Settings")
	tf.until(func() bool { return tf.a.settings != nil && tf.tt.HasText("Unlock with Touch ID") }, "Settings with the Unlock section")
	tf.click("Unlock with Touch ID")
	tf.tt.Frame()
	tf.click("Master password")
	tf.tt.Key(ui.Cmd, ui.KeyA)
	tf.tt.Type(pw)
	tf.tt.Frame()
	tf.pass()
}

// TestFlowTouchID: touchid.spec.ts against a real hub and the fake helper.
func TestFlowTouchID(t *testing.T) {
	tf := newTouchFlow(t)
	tf.unlock()
	tf.tt.SetSize(1000, 900)

	// A wrong password writes nothing and the dialog stays with the hub's error.
	tf.openSettingsAndTurnOn("wrong")
	tf.click("Save")
	tf.until(func() bool { return tf.tt.HasText("wrong master password") }, "the hub's refusal")
	if tf.fileExists() || tf.a.settings == nil {
		t.Fatalf("file %v, dialog open %v", tf.fileExists(), tf.a.settings != nil)
	}
	// The right one writes touchid.json, 0600, and closes the dialog.
	tf.click("Master password")
	tf.tt.Key(ui.Cmd, ui.KeyA)
	tf.tt.Type(hubtest.Password)
	tf.tt.Frame()
	tf.pass()
	tf.click("Save")
	tf.until(func() bool { return tf.a.settings == nil }, "Save to close Settings")
	fi, err := os.Stat(tf.file)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("touchid.json: %v, %v", fi, err)
	}

	// Reopened with no lock between: on, and nothing to save.
	tf.click("Settings")
	tf.until(func() bool { return tf.a.settings != nil }, "Settings again")
	if !tf.a.settings.touchOn {
		t.Fatal("the switch is off after enabling")
	}
	tf.tt.Key(0, ui.KeyEscape)
	tf.until(func() bool { return tf.a.settings == nil }, "Esc to cancel")

	// Locking alone never asks; the next focus unlocks with no typing.
	tf.click("Lock")
	tf.until(func() bool { return tf.tt.HasText("Unlock with Touch ID") && tf.tt.HasText("Master password") }, "the lock screen with the Touch ID button")
	tf.tt.flush()
	if tf.a.Screen() != model.ScreenLocked {
		t.Fatal("locking asked Touch ID by itself")
	}
	tf.focus()
	tf.until(func() bool { return tf.a.Screen() == model.ScreenReady }, "the focus to unlock with Touch ID")
}

// A changed fingerprint set turns Touch ID off, and the password still unlocks.
func TestFlowTouchIDFingerprintsChanged(t *testing.T) {
	tf := newTouchFlow(t)
	tf.unlock()
	tf.tt.SetSize(1000, 900)
	tf.openSettingsAndTurnOn(hubtest.Password)
	tf.click("Save")
	tf.until(func() bool { return tf.a.settings == nil && tf.fileExists() }, "Touch ID turned on")

	tf.setMode("invalid")
	tf.click("Lock")
	tf.until(func() bool { return tf.tt.HasText("Unlock with Touch ID") }, "the Touch ID button")
	tf.click("Unlock with Touch ID")
	tf.until(func() bool { return tf.tt.HasText(touchid.ErrReset.Error()) }, "the reset message")
	if tf.fileExists() {
		t.Fatal("touchid.json stayed")
	}
	tf.until(func() bool { _, ok := tf.tt.Find("Unlock with Touch ID"); return !ok }, "the button to go")
	tf.unlock()
}

// A hub that stops closes a Touch ID dialog still up, and the lock screen
// then says why.
func TestFlowTouchIDHubStopClosesTheDialog(t *testing.T) {
	tf := newTouchFlow(t)
	tf.unlock()
	tf.tt.SetSize(1000, 900)
	tf.openSettingsAndTurnOn(hubtest.Password)
	tf.click("Save")
	tf.until(func() bool { return tf.a.settings == nil && tf.fileExists() }, "Touch ID turned on")

	tf.setMode("slow")
	tf.click("Lock")
	tf.until(func() bool { return tf.tt.HasText("Unlock with Touch ID") }, "the Touch ID button")
	tf.click("Unlock with Touch ID")
	var pid int
	tf.until(func() bool {
		b, err := os.ReadFile(tf.mode + ".pid")
		if err != nil {
			return false
		}
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		return pid > 0
	}, "the helper to start")
	tf.a.Update(func() { tf.a.HubState(hubproc.State{Kind: "restarting"}) })
	tf.until(func() bool { return syscall.Kill(pid, 0) != nil }, "the helper to die")
	tf.setMode("ok")
	running(tf.a, tf.tt)
	tf.until(func() bool { return tf.tt.HasText(touchid.ErrInterrupted.Error()) }, "the interrupted message")
}
