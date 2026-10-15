//go:build unix

package view

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/internal/hubtest"
	"github.com/lang315/sshgate/app/model"
)

// Ports of desktop/e2e/hosts.spec.ts, import.spec.ts and the editor part of
// keyboard.spec.ts.

// fill replaces a field's text, as the e2e's fill does.
func (f *flowT) fill(label, text string) {
	f.t.Helper()
	f.click(label)
	f.tt.Key(ui.Cmd, ui.KeyA)
	f.tt.Type(text)
	f.tt.Frame()
}

func (f *flowT) createVault() {
	f.t.Helper()
	f.until(func() bool { return f.tt.HasText("New master password") }, "Create vault")
	f.click("New master password")
	f.tt.Type("password1")
	f.click("Confirm master password")
	f.tt.Type("password1")
	f.click("Create vault")
	f.until(func() bool { return f.a.Screen() == model.ScreenReady && f.tt.HasText("Vault file:") }, "the ready screen")
}

// trust answers the unknown-key prompt of box once Trust is enabled.
func (f *flowT) trust() {
	f.t.Helper()
	f.until(func() bool { return f.tt.HasText("Trust this host key?") }, "the host-key prompt")
	for _, s := range []string{"SHA256:", fmt.Sprintf("test@127.0.0.1:%d", f.port)} {
		if !f.tt.HasText(s) {
			f.t.Fatalf("missing %q in %q", s, f.tt.Texts())
		}
	}
	f.pass()
	f.click("Trust and connect")
	f.until(func() bool { return !f.tt.HasText("Trust this host key?") }, "the prompt to go")
}

func (f *flowT) editBox() {
	f.t.Helper()
	f.click("Hosts")
	f.click("Edit box")
	f.until(func() bool { return f.a.editor != nil }, "the host editor")
}

func (f *flowT) saveEditor() {
	f.t.Helper()
	f.click("Save")
	f.until(func() bool { return f.a.editor == nil }, "the editor to close after Save")
}

// TestFlowHostsCreateTrustEditForgetDelete: hosts.spec.ts in order, then a
// delete; every host write leaves its config record in the audit log.
func TestFlowHostsCreateTrustEditForgetDelete(t *testing.T) {
	needLib(t)
	f := flowWith(t, hubtest.Options{EmptyStore: true})
	f.tt.SetSize(1000, 900) // the whole editor in view: the Tester clicks where an element is
	f.createVault()

	// 2. Add a host pointing at sshtestd.
	f.click("New host")
	f.fill("Label", "box")
	f.fill("Address", "127.0.0.1")
	f.fill("Port", strconv.Itoa(f.port))
	f.fill("User", "test")
	f.fill("Password", "testpass")
	f.saveEditor()
	f.until(func() bool { s := f.a.Servers(); return len(s) == 1 && s[0].Name == "box" && s[0].HasPassword }, "box in the host list")
	f.configRecords("box", "save", 1)

	// 3. Connect: the prompt shows the fingerprint; Trust gives a shell.
	f.click("Open box")
	f.trust()
	first := f.a.tabs.Active
	f.until(func() bool { return tabState(f.a, first) == "open" && f.tt.Focused("Terminal box") }, "the box tab")
	f.tt.Type("echo hosts-ok")
	f.shows(first, "echo hosts-ok")

	// 4. Edit the port: the editor warns, and saving closes the tab.
	f.editBox()
	f.fill("Port", strconv.Itoa(f.port+1))
	for _, s := range []string{"Changing host or port forgets the host key and saved passwords unless you re-enter them.", "Saving will close 1 open tab."} {
		if !f.tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, f.tt.Texts())
		}
	}
	f.saveEditor()
	f.until(func() bool { return tabState(f.a, first) == "exited" }, "the box tab to exit")
	// Back to the real port: the pin went with the old endpoint, so connecting asks again.
	f.editBox()
	f.fill("Port", strconv.Itoa(f.port))
	f.saveEditor()
	f.until(func() bool { s := f.a.Servers(); return len(s) == 1 && s[0].Port == f.port && s[0].HostKey == "" }, "box back on its port, unpinned")
	f.click("Hosts")
	f.click("Open box")
	f.trust()
	f.configRecords("box", "save", 3)
	f.configRecords("box", "trust", 2)

	// 5. Forget the key; reconnecting shows the prompt again.
	f.editBox()
	if !f.tt.HasText("SHA256:") {
		t.Fatal(f.tt.Texts())
	}
	f.click("Forget host key")
	f.until(func() bool { return f.tt.HasText("Not pinned") }, "the editor to show the key forgotten")
	f.configRecords("box", "forgetHostKey", 1)
	f.click("Close")
	f.click("Hosts")
	f.click("Open box")
	last := f.a.tabs.Active
	f.until(func() bool { return f.tt.HasText("Trust this host key?") }, "the prompt again")
	f.click("Cancel")
	f.until(func() bool { return !f.tt.HasText("Trust this host key?") && tabState(f.a, last) == "exited" }, "the cancelled tab")

	// Delete waits half a second, then removes the host.
	f.click("Hosts")
	f.click("Delete box")
	f.pass()
	f.click("Delete host")
	f.until(func() bool { return len(f.a.Servers()) == 0 && f.tt.HasText("No hosts yet.") }, "box gone")
	f.configRecords("box", "delete", 1)
}
