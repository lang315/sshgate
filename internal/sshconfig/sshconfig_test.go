package sshconfig

import (
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

func TestAliasesRelativeIncludeIsUnderDotSSH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	cfg := filepath.Join(t.TempDir(), "config")
	// The Match exec child outlives ssh and holds its stderr open.
	write(t, cfg, "Match exec \"sleep 30\"\n  User x\nHost web\n")
	start := time.Now()
	if _, err := Resolve(context.Background(), bin, cfg, "web"); err == nil || time.Since(start) > 7*time.Second {
		t.Fatalf("got %v after %v, want an error within ~6 s", err, time.Since(start))
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
