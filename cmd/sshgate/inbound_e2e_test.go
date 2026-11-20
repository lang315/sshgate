//go:build unix

package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/hub"
)

// The real forced command under the system sshd, on loopback, as the current
// user. Nothing here reads or writes the real ~/.ssh: the hub renders into a
// temp authorized_keys, and sshd gives the forced command its audit location
// through SetEnv SSHGATE_STORE (a session otherwise gets a minimal environment).
func TestInboundEndToEndWithSystemSSHD(t *testing.T) {
	const sshd = "/usr/sbin/sshd"
	if _, err := os.Stat(sshd); err != nil {
		t.Skip("no system sshd")
	}
	for _, p := range []string{"/usr/bin/ssh", "/usr/bin/ssh-keygen", "/usr/bin/sftp"} {
		if _, err := os.Stat(p); err != nil {
			t.Skip("no", p)
		}
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "sshgate")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	keygen := func(path string) {
		t.Helper()
		if out, err := exec.Command("/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", path).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v\n%s", err, out)
		}
	}
	hostKey, clientKey := filepath.Join(dir, "host"), filepath.Join(dir, "client")
	keygen(hostKey)
	keygen(clientKey)
	pub, err := os.ReadFile(clientKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}

	store := filepath.Join(dir, "servers.json")
	auditFile := filepath.Join(dir, "audit.jsonl")
	ak := filepath.Join(dir, "ssh", "authorized_keys")
	h, err := hub.New(hub.Options{StorePath: store, AuthorizedKeysPath: ak, InboundBinary: bin})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	if err := h.CreateVault("longenough"); err != nil {
		t.Fatal(err)
	}
	if err := h.AddInbound("iphone", string(pub), "127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	conf := filepath.Join(dir, "sshd_config")
	cfg := strings.Join([]string{
		"Port " + port, "ListenAddress 127.0.0.1", "HostKey " + hostKey, "PidFile " + filepath.Join(dir, "sshd.pid"),
		"AuthorizedKeysFile " + ak, "PasswordAuthentication no", "KbdInteractiveAuthentication no", "UsePAM no",
		"Subsystem sftp /usr/libexec/sftp-server", "StrictModes no", "SetEnv SSHGATE_STORE=" + store, "",
	}, "\n")
	if err := os.WriteFile(conf, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var sshdLog bytes.Buffer
	d := exec.Command(sshd, "-D", "-e", "-f", conf)
	d.Stderr = &sshdLog
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Process.Kill(); d.Wait() })
	for i := 0; ; i++ {
		c, err := net.Dial("tcp", "127.0.0.1:"+port)
		if err == nil {
			c.Close()
			break
		}
		if i > 100 {
			t.Fatalf("sshd did not start: %v\n%s", err, sshdLog.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	opts := []string{"-i", clientKey, "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=" + filepath.Join(dir, "known_hosts"), "-o", "ConnectTimeout=10"}
	run := func(stdin string, name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, name, args...)
		c.Stdin = strings.NewReader(stdin)
		out, err := c.CombinedOutput()
		return string(out), err
	}
	sshArgs := func(extra ...string) []string {
		return append(append(append([]string{"-p", port}, opts...), extra...), "-l", me.Username, "127.0.0.1")
	}
	logins := func() int {
		b, _ := os.ReadFile(auditFile)
		return strings.Count(string(b), `"kind":"login"`)
	}
	fail := func(format string, a ...any) {
		t.Helper()
		t.Fatalf(format+"\nsshd log:\n%s", append(a, sshdLog.String())...)
	}

	// A command, a shell and SFTP each leave one record.
	if out, err := run("", "/usr/bin/ssh", append(sshArgs(), "echo", "from-the-forced-command")...); err != nil || !strings.Contains(out, "from-the-forced-command") {
		fail("command: %v\n%s", err, out)
	}
	if out, err := run("echo shell-works\nexit\n", "/usr/bin/ssh", sshArgs("-T")...); err != nil || !strings.Contains(out, "shell-works") {
		fail("shell: %v\n%s", err, out)
	}
	batch := filepath.Join(dir, "batch")
	os.WriteFile(batch, []byte("ls\n"), 0o600)
	if out, err := run("", "/usr/bin/sftp", append(append([]string{"-P", port, "-b", batch}, opts...), "-o", "User="+me.Username, "127.0.0.1")...); err != nil {
		fail("sftp: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(auditFile)
	log := string(b)
	for _, w := range []string{`"session":"command"`, `"session":"shell"`, `"session":"sftp"`, `"device":"iphone"`, `"from":"127.0.0.1:`} {
		if !strings.Contains(log, w) {
			fail("audit lacks %s:\n%s", w, log)
		}
	}
	if logins() != 3 || strings.Contains(log, "from-the-forced-command") {
		fail("want 3 login records and no command text:\n%s", log)
	}

	// An IP that is not in from= is refused, and nothing is recorded.
	if err := h.AddInbound("other", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDfvTxQb4OiX5D9mYQ2hHf1qkK5mKQvXn1m8Tn0m9E5H", "192.0.2.9"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetInboundIP("iphone", "127.0.0.1", "blocked"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetInboundIP("iphone", "192.0.2.9", "allowed"); err != nil {
		t.Fatal(err)
	}
	if out, err := run("", "/usr/bin/ssh", append(sshArgs(), "echo", "refused-must-not-run")...); err == nil || strings.Contains(out, "refused-must-not-run") || logins() != 3 {
		fail("blocked IP got in: %v\n%s", err, out)
	}

	// A forward-only connection carries no data: restrict turns forwarding off.
	if err := h.SetInboundIP("iphone", "127.0.0.1", "allowed"); err != nil {
		t.Fatal(err)
	}
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	reached := make(chan struct{}, 1)
	go func() {
		if c, err := target.Accept(); err == nil {
			reached <- struct{}{}
			c.Close()
		}
	}()
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	lport := fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	fctx, fcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer fcancel()
	fwd := exec.CommandContext(fctx, "/usr/bin/ssh", append(sshArgs("-N", "-L", lport+":"+target.Addr().String()), []string{}...)...)
	if err := fwd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { fwd.Process.Kill(); fwd.Wait() }()
	var c net.Conn
	for i := 0; i < 100; i++ {
		if c, err = net.Dial("tcp", "127.0.0.1:"+lport); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		fail("the forward never listened: %v", err)
	}
	c.Write([]byte("probe"))
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if n, _ := c.Read(make([]byte, 16)); n != 0 {
		fail("the forward carried data")
	}
	c.Close()
	select {
	case <-reached:
		fail("the forward reached its target")
	case <-time.After(500 * time.Millisecond):
	}
	if logins() != 3 {
		fail("a forward-only connection left %d login records", logins())
	}
}
