package sshconfig

import (
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sshBin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("no OpenSSH client on PATH")
	}
	return p
}

func writeKey(t *testing.T, path, passphrase string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var blk *pem.Block
	if passphrase == "" {
		blk, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		blk, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, string(pem.EncodeToMemory(blk)))
}

func TestAliases(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "conf.d", "b.conf"), "Host fromglob\n")
	write(t, filepath.Join(dir, "abs"), "Host fromabs web\n")
	cfg := filepath.Join(dir, "config")
	write(t, cfg, "# comment\nHost web db\n  HostName 10.0.0.1\nHost=eq\nHost * !bad web? \"quoted\"\n"+
		"Include "+filepath.Join(dir, "conf.d", "*.conf")+"\n  include "+filepath.Join(dir, "abs")+" # trailing\nHost db\n")
	got, err := Aliases(cfg)
	if want := []string{"web", "db", "eq", "quoted", "fromglob", "fromabs"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %v %v, want %v", got, err, want)
	}
}

// fakeHome points homeDir at a temp dir. HOME cannot: homeDir reads the
// account database, as ssh does.
func fakeHome(t *testing.T) string {
	home, old := t.TempDir(), homeDir
	homeDir = func() string { return home }
	t.Cleanup(func() { homeDir = old })
	return home
}

func TestHomeIsFromAccountDatabase(t *testing.T) {
	u, err := user.Current()
	if err != nil || u.HomeDir == "" {
		t.Skip("no account database entry:", err)
	}
	t.Setenv("HOME", t.TempDir())
	if got, want := expandHome("~/x"), filepath.Join(u.HomeDir, "x"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestAliasesRelativeIncludeIsUnderDotSSH(t *testing.T) {
	home := fakeHome(t)
	write(t, filepath.Join(home, ".ssh", "extra"), "Host rel\n")
	cfg := filepath.Join(t.TempDir(), "config")
	write(t, cfg, "Include extra\n")
	if got, err := Aliases(cfg); err != nil || !slices.Equal(got, []string{"rel"}) {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestAliasesIncludeCycle(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config")
	write(t, cfg, "Host a\nInclude "+cfg+"\n")
	if _, err := Aliases(cfg); err == nil || !strings.Contains(err.Error(), "too deep") {
		t.Fatalf("want a depth error, got %v", err)
	}
}

func TestAliasesSkipsDanglingInclude(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "conf.d", "real.conf"), "Host real\n")
	if err := os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "conf.d", "dangling.conf")); err != nil {
		t.Skip("no symlinks:", err)
	}
	cfg := filepath.Join(dir, "config")
	write(t, cfg, "Host top\nInclude "+filepath.Join(dir, "conf.d", "*.conf")+"\nHost after\n")
	if got, err := Aliases(cfg); err != nil || !slices.Equal(got, []string{"top", "real", "after"}) {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestAliasesMissingFile(t *testing.T) {
	if got, err := Aliases(filepath.Join(t.TempDir(), "none")); err != nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestResolveAndCheck(t *testing.T) {
	bin := sshBin(t)
	dir := t.TempDir()
	k1, k2, enc, missing := filepath.Join(dir, "k1"), filepath.Join(dir, "k2"), filepath.Join(dir, "enc"), filepath.Join(dir, "missing")
	writeKey(t, k1, "")
	writeKey(t, k2, "")
	writeKey(t, enc, "pp")
	long := strings.Repeat("a", 65)
	kh1, kh2 := filepath.Join(dir, "kh1"), filepath.Join(dir, "kh2")
	cfg := filepath.Join(dir, "config")
	// Host * sets an IdentityFile, so ssh -G never falls back to the real ~/.ssh/id_* defaults.
	write(t, cfg, strings.Join([]string{
		"Host web", "  HostName 10.0.0.5", "  Port 2200", "  IdentityFile " + missing, "  IdentityFile " + k1, "  IdentityFile " + k2,
		"Host bare",
		"Host locked", "  HostName 10.0.0.6", "  IdentityFile " + enc,
		"Host jump", "  ProxyJump web",
		"Host piped", "  ProxyCommand nc %h %p",
		"Host " + long, "  HostName 10.0.0.7",
		"Host taken", "  HostName 10.0.0.9",
		"Host aliased", "  HostName 10.0.0.8", "  HostKeyAlias pinned-name",
		"Host *", "  User zed", "  IdentityFile " + missing, "  UserKnownHostsFile " + kh1 + " " + kh2,
		"",
	}, "\n"))
	ctx := context.Background()
	resolve := func(alias string) Resolved {
		t.Helper()
		r, err := Resolve(ctx, bin, cfg, alias)
		if err != nil {
			t.Fatalf("%s: %v", alias, err)
		}
		return r
	}
	exists := func(n string) bool { return n == "taken" }
	for _, want := range []Candidate{
		{Alias: "web", Host: "10.0.0.5", Port: 2200, User: "zed", Auth: "key", KeyPath: k1, Status: "ready"},
		{Alias: "bare", Host: "bare", Port: 22, User: "zed", Auth: "agent", Status: "ready"},
		{Alias: "locked", Host: "10.0.0.6", Port: 22, User: "zed", Auth: "key", KeyPath: enc, NeedsPassphrase: true, Status: "ready"},
		{Alias: "jump", Host: "jump", Port: 22, User: "zed", Auth: "agent", Status: "skipped", Reason: "needs ProxyJump"},
		{Alias: "piped", Host: "piped", Port: 22, User: "zed", Auth: "agent", Status: "skipped", Reason: "needs ProxyCommand"},
		{Alias: long, Host: "10.0.0.7", Port: 22, User: "zed", Auth: "agent", Status: "skipped", Reason: "name: use 1-64 characters of A-Z a-z 0-9 . _ -"},
		{Alias: "taken", Host: "10.0.0.9", Port: 22, User: "zed", Auth: "agent", Status: "exists", Reason: "Already in vault"},
	} {
		if got := Check(resolve(want.Alias), exists); got != want {
			t.Errorf("%s:\n got %+v\nwant %+v", want.Alias, got, want)
		}
	}
	web := resolve("web")
	if n, p := web.KnownHostsName(); n != "10.0.0.5" || p != 2200 || !slices.Equal(web.KnownHostsFiles, []string{kh1, kh2}) {
		t.Fatalf("web known_hosts lookup: %s %d %v", n, p, web.KnownHostsFiles)
	}
	if n, p := resolve("aliased").KnownHostsName(); n != "pinned-name" || p != 22 {
		t.Fatalf("aliased: %s %d", n, p)
	}
	// The error carries ssh's own message, for the hub's stderr.
	if _, err := Resolve(ctx, bin, filepath.Join(dir, "nope"), "web"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("a missing -F file must fail with ssh's message, got %v", err)
	}
}

func TestResolveTimesOut(t *testing.T) {
	bin := sshBin(t)
	dir := t.TempDir()
	cfg, pidFile := filepath.Join(dir, "config"), filepath.Join(dir, "pid")
	// The Match exec child outlives ssh and holds its stderr open.
	write(t, cfg, "Match exec \"echo $$ > "+pidFile+"; exec sleep 30\"\n  User x\nHost web\n")
	start := time.Now()
	if _, err := Resolve(context.Background(), bin, cfg, "web"); err == nil || time.Since(start) > 7*time.Second {
		t.Fatalf("got %v after %v, want an error within ~6 s", err, time.Since(start))
	}
	if runtime.GOOS == "windows" {
		return
	}
	b, err := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		t.Fatalf("pid file: %q %v", b, err)
	}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		p, _ := os.FindProcess(pid)
		if errors.Is(p.Signal(syscall.Signal(0)), os.ErrProcessDone) {
			return
		}
		if time.Now().After(deadline) {
			_ = p.Kill()
			t.Fatalf("the Match exec child %d outlived Resolve", pid)
		}
	}
}

func TestCheckSkipsNonPrivateKeyFiles(t *testing.T) {
	dir := t.TempDir()
	k, pubOnly := filepath.Join(dir, "k"), filepath.Join(dir, "agent.pub")
	writeKey(t, k, "")
	pk, _, _ := ed25519.GenerateKey(nil)
	sk, _ := ssh.NewPublicKey(pk)
	write(t, pubOnly, string(ssh.MarshalAuthorizedKey(sk))) // an agent-held key's .pub, as 1Password or Secretive set up
	none := func(string) bool { return false }
	r := Resolved{Alias: "h", HostName: "10.0.0.1", Port: 22, User: "u", IdentityFiles: []string{pubOnly}}
	if c := Check(r, none); c.Auth != "agent" || c.KeyPath != "" {
		t.Fatalf("pub only: %+v", c)
	}
	r.IdentityFiles = []string{pubOnly, k}
	if c := Check(r, none); c.Auth != "key" || c.KeyPath != k {
		t.Fatalf("pub then key: %+v", c)
	}
}

func TestCheckExpandsIdentityFileTokens(t *testing.T) {
	home := fakeHome(t)
	dir := t.TempDir()
	u, err := user.Current()
	if err != nil {
		t.Skip("no local user:", err)
	}
	writeKey(t, filepath.Join(dir, "10.0.0.5_key"), "")
	writeKey(t, filepath.Join(home, ".ssh", "deploy@web_2200"), "")
	writeKey(t, filepath.Join(home, ".ssh", "10.0.0.5_"+u.Username+"_%"), "")
	writeKey(t, filepath.Join(dir, "%Z"), "") // a literal file named like an unknown token is never used
	none := func(string) bool { return false }
	for _, tc := range []struct {
		files []string
		want  string
	}{
		{[]string{dir + "/%h_key"}, dir + "/10.0.0.5_key"},
		{[]string{"~/.ssh/%r@%n_%p"}, "~/.ssh/deploy@web_2200"},
		{[]string{"%d/.ssh/%h_%u_%%"}, home + "/.ssh/10.0.0.5_" + u.Username + "_%"},
		{[]string{dir + "/%Z", dir + "/%h_key"}, dir + "/10.0.0.5_key"},
		{[]string{dir + "/%h_key%"}, ""},
	} {
		r := Resolved{Alias: "web", HostName: "10.0.0.5", Port: 2200, User: "deploy", IdentityFiles: tc.files}
		if c := Check(r, none); c.KeyPath != tc.want || (c.Auth == "key") != (tc.want != "") {
			t.Errorf("%v: got %s %q, want %q", tc.files, c.Auth, c.KeyPath, tc.want)
		}
	}
}

func TestResolveKnownHostsPathWithSpace(t *testing.T) {
	bin := sshBin(t)
	dir := t.TempDir()
	kh := filepath.Join(dir, "with space", "kh")
	write(t, kh, "")
	cfg := filepath.Join(dir, "config")
	write(t, cfg, "Host web\n  UserKnownHostsFile \""+kh+"\"\n")
	r, err := Resolve(context.Background(), bin, cfg, "web")
	if err != nil || !slices.Equal(r.KnownHostsFiles, []string{kh}) {
		t.Fatalf("got %q %v, want [%s]", r.KnownHostsFiles, err, kh)
	}
}
