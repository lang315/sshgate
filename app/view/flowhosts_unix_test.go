//go:build unix

package view

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	fill(f.t, f.tt, label, text)
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

// trust answers the unknown-key prompt for user on sshtestd once Trust is
// enabled.
func (f *flowT) trust(user string) {
	f.t.Helper()
	f.until(func() bool { return f.tt.HasText("Trust this host key?") }, "the host-key prompt")
	for _, s := range []string{"SHA256:", fmt.Sprintf("%s@127.0.0.1:%d", user, f.port)} {
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
	f.trust("test")
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
	f.trust("test")
	f.configRecords("box", "save", 3)
	f.configRecords("box", "trust", 2)

	// 5. Forget the key; reconnecting shows the prompt again.
	f.editBox()
	f.until(func() bool { return f.tt.HasText("SHA256:") }, "the pin in the editor")
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

// TestFlowImportPinsFromKnownHosts: import.spec.ts. hubtest.SSHConfig keeps
// ssh -G away from the real ~/.ssh.
func TestFlowImportPinsFromKnownHosts(t *testing.T) {
	needLib(t)
	for _, bin := range []string{"ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("import needs the OpenSSH client: ", err)
		}
	}
	dir := t.TempDir()
	key, kh, cfg := filepath.Join(dir, "id_ed25519"), filepath.Join(dir, "known_hosts"), filepath.Join(dir, "ssh_config")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v %s", err, out)
	}
	f := flowWith(t, hubtest.Options{KnownHosts: kh}, "--sshConfig="+cfg)
	hubtest.SSHConfig(t, cfg, strings.Join([]string{
		"Host work", "  HostName 127.0.0.1", fmt.Sprintf("  Port %d", f.port), "  User test", "  IdentityFile " + key,
		"Host viaproxy", "  HostName 127.0.0.1", "  ProxyCommand nc %h %p", "",
	}, "\n"), kh)
	f.unlock()
	f.tt.SetSize(1000, 900)
	f.click("Import from SSH config")
	f.until(func() bool { return f.tt.HasText("Import work") }, "the scanned rows")
	for _, s := range []string{"StrictHostKeyChecking accept-new", "SHA256:", "needs ProxyCommand"} {
		if !f.tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, f.tt.Texts())
		}
	}
	if !f.a.imp.checked["work"] || f.a.imp.checked["viaproxy"] {
		t.Fatalf("checked %v", f.a.imp.checked)
	}
	f.click("Import 1 host")
	f.until(func() bool { return f.a.imp == nil && len(f.a.Servers()) == 2 }, "the sheet to close with work imported")
	f.configRecords("work", "import", 1)

	// Pinned at import: no "New key" chip, and connecting asks nothing.
	if f.tt.HasText("New key") || !f.tt.HasText("Open work") {
		t.Fatal(f.tt.Texts())
	}
	f.click("Open work")
	id := f.a.tabs.Active
	f.until(func() bool { return tabState(f.a, id) == "open" && f.tt.Focused("Terminal work") }, "the work tab")
	if f.tt.HasText("Trust this host key?") {
		t.Fatal("an imported host asked to trust its key")
	}
	f.tt.Type("echo import-ok")
	f.shows(id, "echo import-ok")
}

// TestFlowKeyboardMovesTheAuthChoice: keyboard.spec.ts's editor test. The
// arrows move and choose among the Auth radios, wrapping; Esc closes.
func TestFlowKeyboardMovesTheAuthChoice(t *testing.T) {
	f := flow(t)
	f.unlock()
	f.tt.SetSize(1000, 900)
	f.click("New host")
	f.click("+ Key or agent")
	f.tt.Frame() // the radios replace the button
	f.click("password")
	f.tt.Frame()
	if !f.tt.Focused("password") {
		t.Fatal("password is not focused")
	}
	f.tt.Key(0, ui.KeyRight)
	f.tt.Frame()
	if !f.tt.Focused("key") || f.a.editor.draft.Auth != "key" {
		t.Fatal("ArrowRight: want key focused and chosen")
	}
	f.tt.Key(0, ui.KeyLeft)
	f.tt.Key(0, ui.KeyLeft)
	f.tt.Frame()
	if !f.tt.Focused("agent") || f.a.editor.draft.Auth != "agent" {
		t.Fatal("ArrowLeft twice: want agent focused and chosen")
	}
	f.tt.Key(0, ui.KeyEscape)
	f.until(func() bool { return f.a.editor == nil && !f.tt.HasText("Host editor") }, "Esc to close the editor")
}
