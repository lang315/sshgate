# Slice 2b-1: Import Hosts from `~/.ssh/config` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the hosts the author already reaches with `ssh` to the vault in one step, pinned from `known_hosts`, from a sheet in the desktop app.

**Architecture:** A new package `internal/sshconfig` lists the aliases in the config (with `Include`) and resolves each one with `ssh -G`, which does not connect. `sshx.KnownHostKey` picks the pin from the `known_hosts` files `ssh -G` names. The hub gets two UI-door methods, `import.scan` and `import.apply`; apply takes alias names only, recomputes everything itself, and writes all new servers in one `config.Update`. The renderer adds an Import sheet next to the host editor. The UI-door protocol goes to version 3.

**Tech Stack:** Go 1.26 (`golang.org/x/crypto/ssh`, `ssh/knownhosts`, `os/exec`), Electron, React 19, TypeScript, Vitest, `@playwright/test` (Electron mode). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-26-slice2b-ssh-config-import-design.md` (binding). Slice 1 and 2a specs still hold where this one does not change them. Standing decisions: `docs/superpowers/ROADMAP.md`.

## Global Constraints

- The renderer sends only alias names. `import.apply` re-runs the scan for those aliases in the hub and writes what the hub computed; host, port, user, key path, and host key never come from the renderer.
- One write: all imported servers and their pins go into the vault in a single `config.Update`. Either all of them land or none do.
- No network: import never dials. It reads files and runs `ssh -G`.
- `ssh` is found with `exec.LookPath("ssh")`, never through a shell. `ssh -G` runs with a 5-second timeout: `ssh -G [-F configPath] -- <alias>`.
- A host with `ProxyJump` or `ProxyCommand` (other than `none`) is skipped: "needs ProxyJump" / "needs ProxyCommand".
- Pins come only from a `known_hosts` match: ed25519, then ecdsa (any size), then rsa. A `@revoked` key is never pinned. `@cert-authority` lines are ignored. The lookup name is `hostkeyalias` when set (port ignored, as OpenSSH does), else `hostname` and `port`.
- Imported servers are new servers with `aiVisible: false` and no secrets. Import only adds; it never updates an existing server.
- Both methods need an unlocked vault (`writeKey`: `ErrLocked`, or "create a vault first"), are UI-door only, request-only, and count as UI activity (register them with `req`).
- Audit: one `kind: "config"` record per imported server, `action: "import"`, with `server`, `host`, `port`, `fingerprint`, `algo` (empty when not pinned). No secret, no config text.
- Copy (verbatim): button "Import from SSH config"; note "Host keys come from your known_hosts. With `StrictHostKeyChecking accept-new`, OpenSSH accepted them without asking you; compare them with the server if unsure."; statuses "Ready", "Already in vault", "Skipped"; "not in known_hosts"; "Key has a passphrase: add it in the editor after import."; errors "OpenSSH client (ssh) not found", "No ~/.ssh/config", "ssh -G failed".
- Protocol: `hello` → `protocol: 3` (`ProtocolVersion` in `internal/hub/idle.go`, `PROTOCOL_VERSION` in `desktop/src/shared/protocol.ts`), changed in the same commit.
- Go: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...` before every Go commit. Desktop: `cd desktop && npm run typecheck && npm test` before every desktop commit. Go tests that need `ssh` skip themselves when `exec.LookPath("ssh")` fails; they never read the real `~/.ssh`.
- Match existing style: terse Go, few comments, no new abstractions. Commit and push only to local branch `sshgate-main` (remote `sshgate`, branch `main`); no PRs. Commit messages: Conventional Commits, ending with a blank line and exactly:
  `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`
  `Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2`

## Plan decisions

Where the spec leaves a detail open, or names something that cannot compile as written, this plan decides:

1. The spec's `Candidate(r, alias, exists)` function is `sshconfig.Check(r Resolved, exists func(string) bool) Candidate`: a Go package cannot have a type and a function both named `Candidate`, and `Resolved` carries the alias.
2. `Options.SSHBin` is dropped. Only tests would set it, and the "no ssh" test blanks `PATH` instead. `Options.SSHConfigPath` stays (the `--sshConfig` knob).
3. `import.apply` also skips a name that is not an alias in the config, with reason "not in the SSH config". Resolving arbitrary names is not what import is for, and it costs one `slices.Contains`.
4. An `Include` chain deeper than 16 is an error from `Aliases` (and so from `import.scan`). OpenSSH fails the same config the same way.
5. `import.apply` with nothing Ready writes nothing (no revision bump, no audit record).
6. The Import button needs no disabled state: the Hosts tab, where it lives, only exists while the vault is unlocked. The hub still refuses when locked.
7. When `import.apply` reports skipped aliases, the sheet stays open, shows "Not imported: alias (reason); …" and rescans. With no skips it closes.
8. `~` in `IdentityFile`, `UserKnownHostsFile`, and `Include` is expanded with `os.UserHomeDir()`. `KeyPath` is stored as `ssh -G` prints it (`~/…` for the defaults); the hub already expands `~/` when dialling (`mcpserver.expandPath`).

## File map

- Create `internal/sshconfig/sshconfig.go`, `internal/sshconfig/sshconfig_test.go`: aliases, `ssh -G`, candidate rules.
- Modify `internal/sshx/knownhosts.go`, `internal/sshx/knownhosts_test.go`: `KnownHostKey`.
- Modify `internal/sshx/sshtest/sshtest.go`: `PublicKey()`. Modify `internal/sshx/sshtest/sshtestd/main.go` and its test: `-write-known-hosts`.
- Create `internal/hub/import.go`, `internal/hub/import_test.go`. Modify `internal/hub/hub.go` (Options), `internal/hub/uidoor.go`, `internal/hub/idle.go`, `internal/hub/mcpdoor_test.go`, `internal/broker/audit.go` (comment), `cmd/sshgate/hub.go`.
- Modify `desktop/src/shared/protocol.ts`, `desktop/src/renderer/transport.ts`, `desktop/src/main/main.ts`, `desktop/src/renderer/HostList.tsx`, `desktop/src/renderer/App.tsx`, `desktop/src/renderer/styles.css`. Create `desktop/src/renderer/ImportSheet.tsx`, `desktop/test/ImportSheet.test.ts`.
- Modify `desktop/e2e/launch.ts`. Create `desktop/e2e/import.spec.ts`.
- Modify `CLAUDE.md`, `README.md`, `docs/superpowers/ROADMAP.md`, the spec's status line.

---

### Task 1: `internal/sshconfig`

**Files:**
- Create: `internal/sshconfig/sshconfig.go`
- Test: `internal/sshconfig/sshconfig_test.go`

**Interfaces:**
- Consumes: `config.ServerInput{...}.Validate() error` (existing, `internal/config/server_input.go`).
- Produces:
  - `func Aliases(path string) ([]string, error)`
  - `type Resolved struct { Alias, HostName, User, ProxyJump, ProxyCommand, HostKeyAlias string; Port int; IdentityFiles, KnownHostsFiles []string }`
  - `func Resolve(ctx context.Context, sshBin, configPath, alias string) (Resolved, error)`
  - `func (r Resolved) KnownHostsName() (string, int)`
  - `type Candidate struct` with JSON fields `alias, host, port, user, auth, keyPath, needsPassphrase, hostKey, hostKeyAlgo, status, reason`; `Status` is `"ready"`, `"exists"`, or `"skipped"`.
  - `func Check(r Resolved, exists func(string) bool) Candidate` (leaves `HostKey`/`HostKeyAlgo` empty; the hub fills them).

- [ ] **Step 1: Write the failing tests**

`internal/sshconfig/sshconfig_test.go`:

```go
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
	if _, err := Resolve(ctx, bin, filepath.Join(dir, "nope"), "web"); err == nil {
		t.Fatal("a missing -F file must fail")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sshconfig/`
Expected: build failure, `undefined: Aliases` (and the other names).

- [ ] **Step 3: Write the implementation**

`internal/sshconfig/sshconfig.go`:

```go
// Package sshconfig reads the hosts an OpenSSH client config names and
// resolves each one with `ssh -G`, which applies Host *, Match and %-tokens
// exactly as ssh does, without connecting.
package sshconfig

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/lang315/sshgate/internal/config"
)

// maxDepth is OpenSSH's own Include limit.
const maxDepth = 16

// Aliases lists the names on Host lines in path and every file it Includes,
// first appearance first. A name with * or ? or a leading ! is a pattern, not
// a host. A missing path is an empty list.
func Aliases(path string) ([]string, error) {
	var out []string
	err := collect(path, 0, map[string]bool{}, &out)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

func collect(path string, depth int, seen map[string]bool, out *[]string) error {
	if depth > maxDepth {
		return fmt.Errorf("%s: Include nested too deep", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		kw, args := fields(line)
		switch strings.ToLower(kw) {
		case "host":
			for _, a := range args {
				if !seen[a] && !strings.ContainsAny(a, "*?") && !strings.HasPrefix(a, "!") {
					seen[a] = true
					*out = append(*out, a)
				}
			}
		case "include":
			for _, a := range args {
				p := expandHome(a)
				if !filepath.IsAbs(p) {
					home, _ := os.UserHomeDir()
					p = filepath.Join(home, ".ssh", p)
				}
				matches, err := filepath.Glob(p)
				if err != nil {
					return err
				}
				for _, m := range matches {
					if err := collect(m, depth+1, seen, out); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// fields splits a config line into its keyword and arguments. The keyword
// ends at whitespace or '='; a double-quoted argument may hold spaces; an
// unquoted argument starting with # begins a comment.
func fields(line string) (string, []string) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return "", nil
	}
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return line, nil
	}
	kw, rest := line[:i], strings.TrimLeft(line[i:], " \t")
	rest = strings.TrimLeft(strings.TrimPrefix(rest, "="), " \t")
	var args []string
	for rest != "" {
		var a string
		switch j := strings.IndexAny(rest, " \t"); {
		case rest[0] == '"':
			if k := strings.IndexByte(rest[1:], '"'); k >= 0 {
				a, rest = rest[1:k+1], rest[k+2:]
			} else {
				a, rest = rest[1:], ""
			}
		case rest[0] == '#':
			return kw, args
		case j < 0:
			a, rest = rest, ""
		default:
			a, rest = rest[:j], rest[j:]
		}
		args = append(args, a)
		rest = strings.TrimLeft(rest, " \t")
	}
	return kw, args
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// Resolved is what `ssh -G` says ssh would use for one alias.
type Resolved struct {
	Alias, HostName, User, ProxyJump, ProxyCommand, HostKeyAlias string
	Port                                                         int
	IdentityFiles, KnownHostsFiles                               []string
}

// Resolve runs `ssh -G`, which prints the final options for alias and exits
// without connecting. configPath "" lets ssh read its default config.
func Resolve(ctx context.Context, sshBin, configPath, alias string) (Resolved, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"-G"}
	if configPath != "" {
		args = append(args, "-F", configPath)
	}
	out, err := exec.CommandContext(ctx, sshBin, append(args, "--", alias)...).Output()
	if err != nil {
		return Resolved{}, err
	}
	r := Resolved{Alias: alias}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch strings.ToLower(k) {
		case "hostname":
			r.HostName = v
		case "port":
			r.Port, _ = strconv.Atoi(v)
		case "user":
			r.User = v
		case "identityfile":
			r.IdentityFiles = append(r.IdentityFiles, v)
		case "proxyjump":
			r.ProxyJump = v
		case "proxycommand":
			r.ProxyCommand = v
		case "hostkeyalias":
			r.HostKeyAlias = v
		case "userknownhostsfile":
			for _, f := range strings.Fields(v) {
				r.KnownHostsFiles = append(r.KnownHostsFiles, expandHome(f))
			}
		}
	}
	return r, nil
}

func set(v string) bool { return v != "" && v != "none" }

// KnownHostsName is the name and port ssh looks up in known_hosts: a
// HostKeyAlias replaces the host name, and then the port is not used.
func (r Resolved) KnownHostsName() (string, int) {
	if set(r.HostKeyAlias) {
		return r.HostKeyAlias, 22
	}
	return r.HostName, r.Port
}

// Candidate is one alias as the import sheet shows it. Status is ready,
// exists (a vault server has this name), or skipped (Reason says why).
type Candidate struct {
	Alias           string `json:"alias"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	User            string `json:"user"`
	Auth            string `json:"auth"`
	KeyPath         string `json:"keyPath,omitempty"`
	NeedsPassphrase bool   `json:"needsPassphrase,omitempty"`
	HostKey         string `json:"hostKey,omitempty"`
	HostKeyAlgo     string `json:"hostKeyAlgo,omitempty"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
}

// Check turns a resolved alias into a candidate. The first IdentityFile that
// exists gives key auth; ssh -G lists its own defaults when none is set, so
// no default list is needed here. No existing file means agent auth.
func Check(r Resolved, exists func(string) bool) Candidate {
	c := Candidate{Alias: r.Alias, Host: r.HostName, Port: r.Port, User: r.User, Auth: "agent", Status: "ready"}
	for _, f := range r.IdentityFiles {
		if _, err := os.Stat(expandHome(f)); err == nil {
			c.Auth, c.KeyPath, c.NeedsPassphrase = "key", f, encrypted(expandHome(f))
			break
		}
	}
	in := config.ServerInput{Name: c.Alias, Host: c.Host, Port: c.Port, User: c.User, Auth: c.Auth, KeyPath: c.KeyPath}
	switch err := in.Validate(); {
	case set(r.ProxyJump):
		c.Status, c.Reason = "skipped", "needs ProxyJump"
	case set(r.ProxyCommand):
		c.Status, c.Reason = "skipped", "needs ProxyCommand"
	case err != nil:
		c.Status, c.Reason = "skipped", err.Error()
	case exists(c.Alias):
		c.Status, c.Reason = "exists", "Already in vault"
	}
	return c
}

func encrypted(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	_, err = ssh.ParseRawPrivateKey(b)
	var pm *ssh.PassphraseMissingError
	return errors.As(err, &pm)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/sshconfig/ -v`
Expected: all five tests PASS (`TestResolveAndCheck` SKIPs only where no `ssh` is on `PATH`).

If `TestResolveAndCheck` fails because the local `ssh -G` output differs from what `Resolve` parses, run `ssh -G -F <that config> -- web` by hand and fix the parser, not the test.

- [ ] **Step 5: Vet and commit**

```bash
go vet ./... && GOOS=windows go vet ./... && go test -race ./...
git add internal/sshconfig
git commit -m "feat(sshconfig): list ~/.ssh/config aliases and resolve them with ssh -G

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 2: `sshx.KnownHostKey` and `sshtestd -write-known-hosts`

**Files:**
- Modify: `internal/sshx/knownhosts.go`
- Test: `internal/sshx/knownhosts_test.go`
- Modify: `internal/sshx/sshtest/sshtest.go` (add a `hostKey` field and `PublicKey()`)
- Modify: `internal/sshx/sshtest/sshtestd/main.go` and its test file in the same directory

**Interfaces:**
- Produces: `func KnownHostKey(files []string, host string, port int) (algo, fingerprint string, ok bool)` in package `sshx`; `func (s *sshtest.Server) PublicKey() ssh.PublicKey`; `sshtestd -write-known-hosts=<path>`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/sshx/knownhosts_test.go` (add the imports it needs: `crypto/ecdsa`, `crypto/ed25519`, `crypto/elliptic`, `crypto/rand`, `crypto/rsa`, `os`, `path/filepath`, `golang.org/x/crypto/ssh/knownhosts`):

```go
func pub(t *testing.T, k any, err error) ssh.PublicKey {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	p, err := ssh.NewPublicKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestKnownHostKey(t *testing.T) {
	edPub, _, err := ed25519.GenerateKey(nil)
	ed := pub(t, edPub, err)
	ed2Pub, _, err := ed25519.GenerateKey(nil)
	ed2 := pub(t, ed2Pub, err)
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ec := pub(t, &ecKey.PublicKey, err)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	rs := pub(t, &rsaKey.PublicKey, err)

	line := func(addr string, k ssh.PublicKey) string {
		return knownhosts.Line([]string{knownhosts.Normalize(addr)}, k) + "\n"
	}
	kh := filepath.Join(t.TempDir(), "known_hosts")
	body := line("multi:22", rs) + line("multi:22", ec) + line("multi:22", ed) +
		line("noed:22", rs) + line("noed:22", ec) +
		line("h:2222", ed) +
		line("gone:22", ed2) + line("gone:22", rs) +
		line("dead:22", ed2) +
		"@revoked * " + string(ssh.MarshalAuthorizedKey(ed2))
	if err := os.WriteFile(kh, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "none")
	for _, tc := range []struct {
		host     string
		port     int
		algo, fp string
	}{
		{"multi", 22, ssh.KeyAlgoED25519, Fingerprint(ed)},
		{"noed", 22, ec.Type(), Fingerprint(ec)},
		{"h", 2222, ssh.KeyAlgoED25519, Fingerprint(ed)},
		{"h", 22, "", ""},
		{"gone", 22, ssh.KeyAlgoRSA, Fingerprint(rs)}, // the revoked ed25519 key is passed over
		{"dead", 22, "", ""},
		{"absent", 22, "", ""},
	} {
		algo, fp, ok := KnownHostKey([]string{missing, kh}, tc.host, tc.port)
		if algo != tc.algo || fp != tc.fp || ok != (tc.fp != "") {
			t.Errorf("%s:%d: got %q %q %v, want %q %q", tc.host, tc.port, algo, fp, ok, tc.algo, tc.fp)
		}
	}
	if _, _, ok := KnownHostKey([]string{missing}, "multi", 22); ok {
		t.Fatal("no readable file must give no key")
	}
}
```

In `internal/sshx/sshtest/sshtestd/main_test.go`, make `TestSSHTestdWritesReadyStore` also pass `-write-known-hosts` and check the file names the store's pinned key (add imports `strconv` and `github.com/lang315/sshgate/internal/sshx`):

```go
	kh := filepath.Join(t.TempDir(), "known_hosts")
	cmd := exec.Command(bin, "-write-store="+store, "-password=pw", "-write-known-hosts="+kh)
```

and, after the existing `f.FindServer("box")` check:

```go
	port, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PORT=")))
	if algo, fp, ok := sshx.KnownHostKey([]string{kh}, "127.0.0.1", port); !ok || fp != s.HostKey || algo != "ssh-ed25519" {
		t.Fatalf("known_hosts: %q %q %v, want %q", algo, fp, ok, s.HostKey)
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sshx/ -run TestKnownHostKey && go test ./internal/sshx/sshtest/sshtestd/`
Expected: build failure, `undefined: KnownHostKey`; then sshtestd exits with `flag provided but not defined: -write-known-hosts`.

- [ ] **Step 3: Write the implementation**

Append to `internal/sshx/knownhosts.go` (add imports `crypto/ed25519`, `os`, `strings`):

```go
// KnownHostKey picks the key the known_hosts files record for host:port,
// for pinning at import: ed25519, then ecdsa, then rsa. A revoked key is
// never returned. Unreadable files are skipped.
func KnownHostKey(files []string, host string, port int) (algo, fingerprint string, ok bool) {
	var have []string
	for _, f := range files {
		if _, err := os.Stat(f); err == nil {
			have = append(have, f)
		}
	}
	if len(have) == 0 {
		return "", "", false
	}
	cb, err := knownhosts.New(have...)
	if err != nil {
		return "", "", false
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	remote := &net.TCPAddr{IP: net.IPv4zero, Port: port}
	// A throwaway key never matches, so the KeyError lists every key on record.
	probe, _, _ := ed25519.GenerateKey(nil)
	pk, _ := ssh.NewPublicKey(probe)
	var ke *knownhosts.KeyError
	if !errors.As(cb(addr, remote, pk), &ke) {
		return "", "", false
	}
	var pick ssh.PublicKey
	for _, k := range ke.Want {
		r := keyRank(k.Key.Type())
		if r < 0 || pick != nil && r >= keyRank(pick.Type()) {
			continue
		}
		if cb(addr, remote, k.Key) == nil { // not revoked
			pick = k.Key
		}
	}
	if pick == nil {
		return "", "", false
	}
	return pick.Type(), Fingerprint(pick), true
}

func keyRank(t string) int {
	switch {
	case t == ssh.KeyAlgoED25519:
		return 0
	case strings.HasPrefix(t, "ecdsa-sha2-"):
		return 1
	case t == ssh.KeyAlgoRSA:
		return 2
	}
	return -1
}
```

In `internal/sshx/sshtest/sshtest.go`, add a `hostKey ssh.PublicKey` field to `Server` (next to `cfg`), set it in `rotate` inside the existing locked section (`s.hostKey = signer.PublicKey()`), and add:

```go
// PublicKey returns the current host key. Safe to call concurrently with
// RotateHostKey.
func (s *Server) PublicKey() ssh.PublicKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostKey
}
```

In `internal/sshx/sshtest/sshtestd/main.go` (add imports `net`, `strconv`, `golang.org/x/crypto/ssh/knownhosts`), extend the doc comment with "With -write-known-hosts it writes its host key as a known_hosts line.", add the flag, and write the file before printing `PORT=`:

```go
	kh := flag.String("write-known-hosts", "", "write this server's host key as a known_hosts line to this path")
```

```go
	if *kh != "" {
		line := knownhosts.Line([]string{knownhosts.Normalize(net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port)))}, srv.PublicKey())
		if err := os.WriteFile(*kh, []byte(line+"\n"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/sshx/ -run 'TestKnownHost' -v && go test ./internal/sshx/sshtest/...`
Expected: PASS.

- [ ] **Step 5: Vet and commit**

```bash
go vet ./... && GOOS=windows go vet ./... && go test -race ./...
git add internal/sshx
git commit -m "feat(sshx): pick a host key to pin from known_hosts

sshtestd -write-known-hosts writes its own key as a known_hosts line.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 3: Hub `import.scan` / `import.apply`, `--sshConfig`, protocol 3

**Files:**
- Create: `internal/hub/import.go`
- Test: `internal/hub/import_test.go`
- Modify: `internal/hub/hub.go` (`Options.SSHConfigPath`), `internal/hub/uidoor.go` (two methods), `internal/hub/idle.go` (`ProtocolVersion = 3`), `internal/hub/mcpdoor_test.go` (forbidden list), `internal/broker/audit.go` (Action comment), `cmd/sshgate/hub.go` (`--sshConfig`)
- Modify: `desktop/src/shared/protocol.ts` (`PROTOCOL_VERSION = 3`, methods, types)

**Interfaces:**
- Consumes: `sshconfig.Aliases`, `sshconfig.Resolve`, `sshconfig.Check`, `Resolved.KnownHostsName`, `sshconfig.Candidate` (Task 1); `sshx.KnownHostKey` (Task 2); existing `h.writeKey`, `h.Reload`, `h.auditConfig`, `config.Update`, `config.ApplyServer`, `File.FindServer`.
- Produces:
  - UI-door `import.scan` (no params) → `{candidates: Candidate[], note?: "No ~/.ssh/config"}`.
  - UI-door `import.apply {aliases: string[]}` → `{imported: string[], skipped: {alias, reason}[]}`.
  - `hub.Options.SSHConfigPath string` ("" = `~/.ssh/config`, and `ssh -G` without `-F`).
  - `sshgate hub --sshConfig=<path>`.
  - TypeScript in `protocol.ts`: `ImportCandidate`, `ImportScan`, `ImportResult`; `'import.scan'` and `'import.apply'` in `REQUEST_METHODS`; `PROTOCOL_VERSION = 3`.

- [ ] **Step 1: Write the failing tests**

`internal/hub/import_test.go`:

```go
package hub

import (
	"context"
	"crypto/ed25519"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/lang315/sshgate/internal/sshconfig"
)

type importFixture struct {
	h              *Hub
	c              *rpc.Client
	store, key, fp string
}

// importHub is an unlocked vault whose SSH config has web (key auth, its key
// in known_hosts) and jump (behind a ProxyJump).
func importHub(t *testing.T) importFixture {
	t.Helper()
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no OpenSSH client on PATH")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "id_web") // only has to exist; import never reads a key to dial
	pubKey, _, _ := ed25519.GenerateKey(nil)
	hk, _ := ssh.NewPublicKey(pubKey)
	kh := filepath.Join(dir, "known_hosts")
	cfg := filepath.Join(dir, "config")
	for p, body := range map[string]string{
		key: "not a key",
		kh:  knownhosts.Line([]string{knownhosts.Normalize("10.0.0.5:2200")}, hk) + "\n",
		cfg: strings.Join([]string{
			"Host web", "  HostName 10.0.0.5", "  Port 2200", "  User deploy", "  IdentityFile " + key,
			"Host jump", "  ProxyJump web",
			"Host *", "  IdentityFile " + filepath.Join(dir, "missing"), "  UserKnownHostsFile " + kh, "",
		}, "\n"),
	} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h, store := newHubAt(t, nil, nil)
	h.o.SSHConfigPath = cfg
	c, _ := startUI(t, h)
	if err := c.Call(context.Background(), "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	return importFixture{h, c, store, key, ssh.FingerprintSHA256(hk)}
}

type scanResult struct {
	Candidates []sshconfig.Candidate `json:"candidates"`
	Note       string                `json:"note"`
}

type applyResult struct {
	Imported []string `json:"imported"`
	Skipped  []struct {
		Alias  string `json:"alias"`
		Reason string `json:"reason"`
	} `json:"skipped"`
}

func TestImportScanAndApply(t *testing.T) {
	fx := importHub(t)
	ctx := context.Background()
	var scan scanResult
	if err := fx.c.Call(ctx, "import.scan", nil, &scan); err != nil {
		t.Fatal(err)
	}
	web := sshconfig.Candidate{Alias: "web", Host: "10.0.0.5", Port: 2200, User: "deploy", Auth: "key", KeyPath: fx.key,
		HostKey: fx.fp, HostKeyAlgo: "ssh-ed25519", Status: "ready"}
	if len(scan.Candidates) != 2 || scan.Candidates[0] != web ||
		scan.Candidates[1].Status != "skipped" || scan.Candidates[1].Reason != "needs ProxyJump" {
		t.Fatalf("scan %+v", scan)
	}

	before, _ := config.Load(fx.store)
	// Only aliases count: a planted host or pin in the params is ignored.
	params := map[string]any{"aliases": []string{"web", "jump", "nothere", "web"}, "host": "evil.example", "hostKey": "SHA256:planted"}
	var res applyResult
	if err := fx.c.Call(ctx, "import.apply", params, &res); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Imported, []string{"web"}) || len(res.Skipped) != 2 ||
		res.Skipped[0].Alias != "nothere" || res.Skipped[0].Reason != "not in the SSH config" ||
		res.Skipped[1].Alias != "jump" || res.Skipped[1].Reason != "needs ProxyJump" {
		t.Fatalf("apply %+v", res)
	}
	after, _ := config.Load(fx.store)
	if after.Revision != before.Revision+1 {
		t.Fatalf("revision %d → %d, want exactly one write", before.Revision, after.Revision)
	}
	want := config.Server{Name: "web", Host: "10.0.0.5", Port: 2200, User: "deploy", Auth: "key", KeyPath: fx.key,
		HostKey: fx.fp, HostKeyAlgo: "ssh-ed25519"}
	if s, ok := after.FindServer("web"); !ok || s != want {
		t.Fatalf("stored %+v, want %+v", s, want)
	}
	if _, ok := after.FindServer("jump"); ok {
		t.Fatal("jump was imported")
	}

	var imports []map[string]any
	_, recs := readAudit(t, fx.store)
	for _, r := range recs {
		if r["action"] == "import" {
			imports = append(imports, r)
		}
	}
	if len(imports) != 1 || imports[0]["kind"] != "config" || imports[0]["server"] != "web" || imports[0]["host"] != "10.0.0.5" ||
		imports[0]["port"] != float64(2200) || imports[0]["fingerprint"] != fx.fp || imports[0]["algo"] != "ssh-ed25519" {
		t.Fatalf("audit %v", imports)
	}

	// Again: web is in the vault now, so nothing is written.
	if err := fx.c.Call(ctx, "import.apply", map[string]any{"aliases": []string{"web"}}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 0 || len(res.Skipped) != 1 || res.Skipped[0].Reason != "Already in vault" {
		t.Fatalf("second apply %+v", res)
	}
	if again, _ := config.Load(fx.store); again.Revision != after.Revision {
		t.Fatal("an import with nothing ready wrote the vault")
	}
	if err := fx.c.Call(ctx, "import.scan", nil, &scan); err != nil || scan.Candidates[0].Status != "exists" {
		t.Fatalf("rescan %v %+v", err, scan)
	}
}

func TestImportNeedsUnlockedVault(t *testing.T) {
	h, _ := newHubAt(t, nil, nil)
	h.o.SSHConfigPath = filepath.Join(t.TempDir(), "config")
	c, _ := startUI(t, h)
	ctx := context.Background()
	call := func(want string) {
		t.Helper()
		for _, m := range []string{"import.scan", "import.apply"} {
			if err := c.Call(ctx, m, map[string]any{"aliases": []string{"web"}}, nil); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: want %q, got %v", m, want, err)
			}
		}
	}
	call("create a vault first")
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, "lock", nil, nil); err != nil {
		t.Fatal(err)
	}
	call(ErrLocked.Error())
}

func TestImportScanWithoutConfig(t *testing.T) {
	h, _ := newHubAt(t, nil, nil)
	h.o.SSHConfigPath = filepath.Join(t.TempDir(), "none")
	c, _ := startUI(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	var scan scanResult
	if err := c.Call(ctx, "import.scan", nil, &scan); err != nil || scan.Note != "No ~/.ssh/config" || len(scan.Candidates) != 0 {
		t.Fatalf("%v %+v", err, scan)
	}
}

func TestImportWithoutSSHClient(t *testing.T) {
	h, _ := newHubAt(t, nil, nil)
	cfg := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(cfg, []byte("Host web\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.o.SSHConfigPath = cfg
	c, _ := startUI(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	if err := c.Call(ctx, "import.scan", nil, nil); err == nil || !strings.Contains(err.Error(), "OpenSSH client (ssh) not found") {
		t.Fatalf("want no-ssh error, got %v", err)
	}
}
```

In `internal/hub/mcpdoor_test.go`, `TestMCPDoorRejectsUIOnlyMethods`: add `"import.scan", "import.apply"` to the method list.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/hub/ -run 'TestImport|TestMCPDoorRejectsUIOnlyMethods'`
Expected: build failure, `h.o.SSHConfigPath undefined`.

- [ ] **Step 3: Write the implementation**

`internal/hub/hub.go`, in `Options`, after `KnownHostsPath`:

```go
	SSHConfigPath  string                         // "" means ~/.ssh/config, read by ssh -G without -F; hub --sshConfig
```

`internal/hub/import.go`:

```go
package hub

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshconfig"
	"github.com/lang315/sshgate/internal/sshx"
)

var errNoSSH = errors.New("OpenSSH client (ssh) not found")

type importScan struct {
	Candidates []sshconfig.Candidate `json:"candidates"`
	Note       string                `json:"note,omitempty"`
}

type importSkip struct {
	Alias  string `json:"alias"`
	Reason string `json:"reason"`
}

func (h *Hub) sshConfigPath() string {
	if h.o.SSHConfigPath != "" {
		return h.o.SSHConfigPath
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "config")
}

func (h *Hub) serverExists(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.deps.File == nil {
		return false
	}
	_, ok := h.deps.File.FindServer(name)
	return ok
}

// candidates resolves each alias with ssh -G and looks a ready one's key up
// in the known_hosts files ssh itself would read. Nothing here dials.
func (h *Hub) candidates(ctx context.Context, aliases []string) ([]sshconfig.Candidate, error) {
	// LookPath, never a shell: a shell function or alias named ssh is not the client.
	bin, err := exec.LookPath("ssh")
	if err != nil {
		return nil, errNoSSH
	}
	out := []sshconfig.Candidate{}
	for _, a := range aliases {
		r, err := sshconfig.Resolve(ctx, bin, h.o.SSHConfigPath, a)
		if err != nil {
			out = append(out, sshconfig.Candidate{Alias: a, Status: "skipped", Reason: "ssh -G failed"})
			continue
		}
		c := sshconfig.Check(r, h.serverExists)
		if c.Status == "ready" {
			name, port := r.KnownHostsName()
			c.HostKeyAlgo, c.HostKey, _ = sshx.KnownHostKey(r.KnownHostsFiles, name, port)
		}
		out = append(out, c)
	}
	return out, nil
}

// ImportScan lists every alias in the SSH config as an import candidate. It
// needs an unlocked vault, like every host write.
func (h *Hub) ImportScan(ctx context.Context) (importScan, error) {
	key, err := h.writeKey()
	if err != nil {
		return importScan{}, err
	}
	clear(key)
	path := h.sshConfigPath()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return importScan{Candidates: []sshconfig.Candidate{}, Note: "No ~/.ssh/config"}, nil
	}
	aliases, err := sshconfig.Aliases(path)
	if err != nil {
		return importScan{}, err
	}
	cs, err := h.candidates(ctx, aliases)
	return importScan{Candidates: cs}, err
}

// ImportApply adds the named aliases that are ready now. The renderer sends
// names only; host, user, key path and pin are recomputed here. Every server
// lands in one write, or none does.
func (h *Hub) ImportApply(ctx context.Context, names []string) (imported []string, skipped []importSkip, err error) {
	imported, skipped = []string{}, []importSkip{}
	key, err := h.writeKey()
	if err != nil {
		return nil, nil, err
	}
	defer clear(key)
	all, err := sshconfig.Aliases(h.sshConfigPath())
	if err != nil {
		return nil, nil, err
	}
	var want []string
	for _, n := range names {
		switch {
		case !slices.Contains(all, n):
			skipped = append(skipped, importSkip{n, "not in the SSH config"})
		case !slices.Contains(want, n):
			want = append(want, n)
		}
	}
	cs, err := h.candidates(ctx, want)
	if err != nil {
		return nil, nil, err
	}
	var ready []sshconfig.Candidate
	for _, c := range cs {
		if c.Status == "ready" {
			ready = append(ready, c)
		} else {
			skipped = append(skipped, importSkip{c.Alias, c.Reason})
		}
	}
	if len(ready) == 0 {
		return imported, skipped, nil
	}
	var done []sshconfig.Candidate
	var late []importSkip
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		done, late = nil, nil
		for _, c := range ready {
			if _, dup := f.FindServer(c.Alias); dup { // added since the scan
				late = append(late, importSkip{c.Alias, "Already in vault"})
				continue
			}
			in := config.ServerInput{Name: c.Alias, Host: c.Host, Port: c.Port, User: c.User, Auth: c.Auth, KeyPath: c.KeyPath}
			if _, _, err := config.ApplyServer(f, "", in, key); err != nil {
				return err
			}
			s := &f.Servers[len(f.Servers)-1]
			s.HostKey, s.HostKeyAlgo = c.HostKey, c.HostKeyAlgo
			done = append(done, c)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	skipped = append(skipped, late...)
	reloadErr := h.Reload()
	for _, c := range done {
		imported = append(imported, c.Alias)
		h.auditConfig(broker.ConfigRecord{Action: "import", Server: c.Alias, Host: c.Host, Port: c.Port, Fingerprint: c.HostKey, Algo: c.HostKeyAlgo})
	}
	return imported, skipped, reloadErr // the write itself succeeded
}
```

`internal/hub/uidoor.go`, after the `servers.forgetHostKey` handler:

```go
	req("import.scan", func(ctx context.Context, _ json.RawMessage) (any, error) {
		return h.ImportScan(ctx)
	})
	req("import.apply", func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Aliases []string `json:"aliases"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
		}
		imported, skipped, err := h.ImportApply(ctx, p.Aliases)
		if err != nil {
			return nil, err
		}
		return map[string]any{"imported": imported, "skipped": skipped}, nil
	})
```

`internal/hub/idle.go`: `const ProtocolVersion = 3`. If any Go test asserts the protocol number, update it to 3.

`internal/broker/audit.go`, `ConfigRecord.Action` comment: `// trust, forgetHostKey, delete, vaultCreate, save, import`.

`cmd/sshgate/hub.go`, before `hub.New`:

```go
	// Dev/test knob, like --store: ssh finds ~ from the account database, not $HOME.
	var sshConfig string
	if p := m["sshConfig"]; p != nil {
		sshConfig = *p
	}
```

and pass `SSHConfigPath: sshConfig` in `hub.Options{...}`.

`desktop/src/shared/protocol.ts`: add after `TermOpenResult`:

```ts
// Import from ~/.ssh/config. The renderer sends only alias names back; the
// hub recomputes host, user, key path and pin itself.
export interface ImportCandidate {
  alias: string; host: string; port: number; user: string; auth: string; keyPath?: string
  needsPassphrase?: boolean; hostKey?: string; hostKeyAlgo?: string
  status: 'ready' | 'exists' | 'skipped'; reason?: string
}
export interface ImportScan { candidates: ImportCandidate[]; note?: string }
export interface ImportResult { imported: string[]; skipped: { alias: string; reason: string }[] }
```

append `'import.scan', 'import.apply'` to `REQUEST_METHODS`, and set `export const PROTOCOL_VERSION = 3`.

`desktop/test/fixtures/fakeHub.mjs` line 28 answers `hello` with a hardcoded `2`: change it to `3` (keep `99` for `badproto`).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/hub/ -run 'TestImport|TestMCPDoorRejectsUIOnlyMethods' -v && (cd desktop && npm run typecheck && npm test)`
Expected: PASS. If a desktop test pins the old method list or protocol number, update it to match.

- [ ] **Step 5: Vet and commit**

```bash
go vet ./... && GOOS=windows go vet ./... && go test -race ./...
git add internal/hub internal/broker/audit.go cmd/sshgate/hub.go desktop/src/shared/protocol.ts desktop/test
git commit -m "feat(hub): import.scan and import.apply from ~/.ssh/config, protocol 3

Apply takes alias names only and recomputes everything else, then writes
all new servers and their known_hosts pins in one config.Update.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 4: Desktop Import sheet

**Files:**
- Create: `desktop/src/renderer/ImportSheet.tsx`
- Test: `desktop/test/ImportSheet.test.ts`
- Modify: `desktop/src/renderer/transport.ts`, `desktop/src/main/main.ts`, `desktop/src/renderer/HostList.tsx`, `desktop/src/renderer/App.tsx`, `desktop/src/renderer/styles.css`

**Interfaces:**
- Consumes: `ImportCandidate`, `ImportScan`, `ImportResult`, `'import.scan'`, `'import.apply'` from `protocol.ts` (Task 3).
- Produces: `hub.importScan(): Promise<ImportScan>`, `hub.importApply(aliases: string[]): Promise<ImportResult>`; `ImportSheet` (dialog named "Import from SSH config"); `ImportRows`; `HostList` prop `onImport: () => void`; env `SSHGATE_SSH_CONFIG` → `hub --sshConfig=`.

- [ ] **Step 1: Write the failing test**

`desktop/test/ImportSheet.test.ts`:

```ts
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { ImportRows, ImportSheet } from '../src/renderer/ImportSheet'
import type { ImportCandidate } from '../src/shared/protocol'

const ready: ImportCandidate = {
  alias: 'web', host: '10.0.0.5', port: 2200, user: 'deploy', auth: 'key', keyPath: '~/.ssh/id_web',
  hostKey: 'SHA256:abc', hostKeyAlgo: 'ssh-ed25519', status: 'ready',
}
const rows = (c: ImportCandidate, checked: string[] = []) =>
  renderToStaticMarkup(createElement(ImportRows, { candidates: [c], checked: new Set(checked), onToggle: () => {} }))

describe('ImportRows', () => {
  it('checks a ready row and shows its address, key path and pin', () => {
    const html = rows(ready, ['web'])
    expect(html).toContain('aria-label="Import web"')
    expect(html).toMatch(/<input[^>]*checked=""/)
    expect(html).toContain('deploy@10.0.0.5:2200 · ~/.ssh/id_web')
    expect(html).toContain('ssh-ed25519 SHA256:abc')
  })
  it('shows a skipped row with its reason and no checkbox', () => {
    const html = rows({ alias: 'jump', host: 'jump', port: 22, user: 'u', auth: 'agent', status: 'skipped', reason: 'needs ProxyJump' })
    expect(html).toContain('needs ProxyJump')
    expect(html).toContain('Skipped')
    expect(html).not.toContain('<input')
  })
  it('marks an alias already in the vault, with no checkbox', () => {
    const html = rows({ ...ready, hostKey: undefined, hostKeyAlgo: undefined, status: 'exists', reason: 'Already in vault' })
    expect(html).toContain('Already in vault')
    expect(html).not.toContain('<input')
    expect(html).not.toContain('not in known_hosts')
  })
  it('says when a key is not in known_hosts or needs a passphrase', () => {
    const html = rows({ ...ready, hostKey: undefined, hostKeyAlgo: undefined, needsPassphrase: true })
    expect(html).toContain('not in known_hosts')
    expect(html).toContain('Key has a passphrase: add it in the editor after import.')
  })
})

describe('ImportSheet', () => {
  it('shows the known_hosts note and a disabled Import button before the scan returns', () => {
    const html = renderToStaticMarkup(createElement(ImportSheet, {
      scan: () => new Promise(() => {}), apply: async () => ({ imported: [], skipped: [] }),
      onImported: async () => {}, onClose: () => {},
    }))
    expect(html).toContain('aria-label="Import from SSH config"')
    expect(html).toContain('StrictHostKeyChecking accept-new')
    expect(html).toMatch(/<button type="submit"[^>]*disabled=""/)
  })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd desktop && npx vitest run test/ImportSheet.test.ts`
Expected: FAIL, cannot resolve `../src/renderer/ImportSheet`.

- [ ] **Step 3: Write the implementation**

`desktop/src/renderer/ImportSheet.tsx`:

```tsx
import { useEffect, useState, type FormEvent, type KeyboardEvent } from 'react'
import type { ImportCandidate, ImportResult, ImportScan } from '../shared/protocol'
import { CloseIcon } from './icons'

const LABEL = { ready: 'Ready', exists: 'Already in vault', skipped: 'Skipped' } as const

// The list alone, so it renders without a hub.
export function ImportRows({ candidates, checked, onToggle }: {
  candidates: ImportCandidate[]; checked: ReadonlySet<string>; onToggle: (alias: string) => void
}) {
  return (
    <ul className="importlist">
      {candidates.map((c) => (
        <li key={c.alias} className="importrow">
          <div className="importrow-main">
            {c.status === 'ready' && (
              <input type="checkbox" aria-label={`Import ${c.alias}`} checked={checked.has(c.alias)} onChange={() => onToggle(c.alias)} />
            )}
            <span className="importrow-name">{c.alias}</span>
            <span className={'chip ' + (c.status === 'ready' ? 'ok' : 'wait')}>{LABEL[c.status]}</span>
          </div>
          {c.status === 'skipped'
            ? <span className="muted">{c.reason}</span>
            : <span className="mono muted">{`${c.user}@${c.host}:${c.port} · ${c.auth === 'key' ? c.keyPath : 'agent'}`}</span>}
          {c.status === 'ready' && <code className="fp">{c.hostKey ? `${c.hostKeyAlgo} ${c.hostKey}` : 'not in known_hosts'}</code>}
          {c.status === 'ready' && c.needsPassphrase && <span className="muted">Key has a passphrase: add it in the editor after import.</span>}
        </li>
      ))}
    </ul>
  )
}

// Ready rows start checked. The hub gets alias names only.
export function ImportSheet({ scan, apply, onImported, onClose }: {
  scan: () => Promise<ImportScan>; apply: (aliases: string[]) => Promise<ImportResult>
  onImported: () => Promise<void>; onClose: () => void
}) {
  const [list, setList] = useState<ImportScan>()
  const [checked, setChecked] = useState<Set<string>>(() => new Set())
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(true)
  const load = async () => {
    setBusy(true)
    try {
      const s = await scan()
      setList(s)
      setChecked(new Set(s.candidates.filter((c) => c.status === 'ready').map((c) => c.alias)))
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  useEffect(() => { load() }, [])
  const toggle = (alias: string) => setChecked((s) => {
    const n = new Set(s)
    if (!n.delete(alias)) n.add(alias)
    return n
  })
  const submit = async (e: FormEvent) => {
    e.preventDefault(); setBusy(true); setError(undefined)
    try {
      const r = await apply([...checked])
      await onImported()
      if (r.skipped.length === 0) { onClose(); return }
      setError('Not imported: ' + r.skipped.map((s) => `${s.alias} (${s.reason})`).join('; '))
      await load()
    } catch (err) { setError((err as Error).message) } finally { setBusy(false) }
  }
  const escape = (e: KeyboardEvent) => { if (e.key === 'Escape' && !busy) { e.preventDefault(); onClose() } }
  const n = checked.size
  return (
    <div className="sheet-layer">
      <form className="sheet" role="dialog" aria-label="Import from SSH config" onSubmit={submit} onKeyDown={escape}>
        <header className="sheet-head">
          <h3>Import from SSH config</h3>
          <button type="button" className="icon" aria-label="Close import" title="Close" onClick={onClose}><CloseIcon /></button>
        </header>
        <div className="sheet-body">
          <p className="muted">Host keys come from your known_hosts. With <code>StrictHostKeyChecking accept-new</code>, OpenSSH accepted them without asking you; compare them with the server if unsure.</p>
          {list?.note && <p className="empty">{list.note}</p>}
          {list ? <ImportRows candidates={list.candidates} checked={checked} onToggle={toggle} />
            : busy && <p className="muted">Reading your SSH config…</p>}
        </div>
        <footer className="sheet-foot">
          {error && <p className="error">{error}</p>}
          <span className="spacer" />
          <button type="button" className="btn" autoFocus onClick={onClose}>Close</button>
          <button type="submit" className="btn primary" disabled={busy || n === 0}>{`Import ${n} ${n === 1 ? 'host' : 'hosts'}`}</button>
        </footer>
      </form>
    </div>
  )
}
```

`desktop/src/renderer/styles.css`, after the `.sheet-foot` rules:

```css
.importlist { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
.importrow { display: flex; flex-direction: column; gap: 6px; padding: 10px 12px; border: 1px solid var(--line); border-radius: 8px; }
.importrow-main { display: flex; align-items: center; gap: 8px; }
.importrow-name { flex: 1; font-weight: 600; }
.importrow .fp { flex: none; }
.chip.ok { background: var(--accent-tint); color: var(--accent); }
```

`desktop/src/renderer/transport.ts`: import `ImportResult, ImportScan` in the type import, and add to `hub` after `forgetHostKey`:

```ts
  importScan: () => call<ImportScan>('import.scan'),
  importApply: (aliases: string[]) => call<ImportResult>('import.apply', { aliases }),
```

`desktop/src/main/main.ts`, in `hubArgs()` after the `SSHGATE_STORE` line:

```ts
  if (process.env.SSHGATE_SSH_CONFIG) args.push(`--sshConfig=${process.env.SSHGATE_SSH_CONFIG}`)
```

`desktop/src/renderer/HostList.tsx`: add `onImport: () => void` to the props (type and destructuring), and after the New host button:

```tsx
        <button type="button" className="btn" onClick={onImport}>Import from SSH config</button>
```

`desktop/src/renderer/App.tsx`: import `ImportSheet`; add `const [importing, setImporting] = useState(false)` next to `editing`. One sheet at a time: in `hostList`, `onNew={() => { setImporting(false); setEditing({}) }}`, `onEdit={async (name) => { setImporting(false); await reloadServers(); setEditing({ name }) }}`, and `onImport={() => { setEditing(undefined); setImporting(true) }}`. After the `HostEditor` block inside `<main className="work">`:

```tsx
            {ready && importing && (
              <ImportSheet scan={hub.importScan} apply={hub.importApply} onImported={reloadServers}
                onClose={() => setImporting(false)} />
            )}
```

`reloadServers` returns the promise chain from `hub.servers()`; if its type is not `Promise<void>`, pass `onImported={async () => { await reloadServers() }}`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd desktop && npm run typecheck && npm test`
Expected: PASS, including the 5 new `ImportSheet` tests.

- [ ] **Step 5: Commit**

```bash
git add desktop/src desktop/test/ImportSheet.test.ts
git commit -m "feat(desktop): Import from SSH config sheet

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 5: Playwright `import.spec.ts`

**Files:**
- Modify: `desktop/e2e/launch.ts`
- Create: `desktop/e2e/import.spec.ts`

**Interfaces:**
- Consumes: `sshtestd -write-known-hosts` (Task 2), `hub --sshConfig` via `SSHGATE_SSH_CONFIG` (Tasks 3–4), the sheet (Task 4).
- Produces: `launch(extraEnv, { sshConfig?: (port: number, tmp: string) => string })`.

- [ ] **Step 1: Extend `launch`**

In `desktop/e2e/launch.ts`, change the signature and body:

```ts
// launch builds sshgate and sshtestd into <tmp>/bin, starts sshtestd, and
// launches the app against <tmp>/store/servers.json. By default sshtestd
// writes a ready vault there (password "pw", server "box"), as smoke.spec.ts
// does; emptyStore leaves no store, so the app opens at Create vault. port is
// sshtestd's. extraEnv is added to the app's environment. sshConfig, given
// sshtestd's port, returns an SSH config written to <tmp>/ssh_config for the
// hub (SSHGATE_SSH_CONFIG); sshtestd then writes its host key to
// <tmp>/known_hosts.
export async function launch(extraEnv: Record<string, string> = {},
  opts: { emptyStore?: boolean; sshConfig?: (port: number, tmp: string) => string } = {}): Promise<Launched> {
```

After `const args = ...`:

```ts
  if (opts.sshConfig) args.push(`-write-known-hosts=${path.join(tmp, 'known_hosts')}`)
```

After the port is read:

```ts
  const env = { ...extraEnv }
  if (opts.sshConfig) {
    const cfg = path.join(tmp, 'ssh_config')
    fs.writeFileSync(cfg, opts.sshConfig(port, tmp), { mode: 0o600 })
    env.SSHGATE_SSH_CONFIG = cfg
  }
```

and in `electron.launch`, replace `...extraEnv` with `...env`.

- [ ] **Step 2: Write the e2e test**

`desktop/e2e/import.spec.ts`:

```ts
import { test, expect } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import * as path from 'node:path'
import { launch, unlock, waitOpen, type Launched } from './launch'

// Spec 2b-1 §Testing: import pins from known_hosts, so the first connect has no Trust prompt.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')

let l: Launched
test.beforeAll(async () => {
  l = await launch({}, {
    sshConfig: (port, tmp) => {
      const key = path.join(tmp, 'id_ed25519')
      execFileSync('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', key])
      // Host * sets an IdentityFile and a known_hosts file, so nothing reads the real ~/.ssh.
      return [
        'Host work', '  HostName 127.0.0.1', `  Port ${port}`, '  User test', `  IdentityFile ${key}`,
        'Host viaproxy', '  HostName 127.0.0.1', '  ProxyCommand nc %h %p',
        'Host *', `  IdentityFile ${path.join(tmp, 'missing')}`, `  UserKnownHostsFile ${path.join(tmp, 'known_hosts')}`, '',
      ].join('\n')
    },
  })
})
test.afterAll(async () => { await l?.close() })

test('import a host from the SSH config, pinned from known_hosts', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  await win.locator('.tabbar .hometab').click()
  const hosts = win.locator('nav.hosts')
  await hosts.getByRole('button', { name: 'Import from SSH config' }).click()

  const sheet = win.getByRole('dialog', { name: 'Import from SSH config' })
  await expect(sheet).toContainText('StrictHostKeyChecking accept-new')
  await expect(sheet.getByRole('checkbox', { name: 'Import work' })).toBeChecked()
  await expect(sheet).toContainText('SHA256:')
  await expect(sheet).toContainText('needs ProxyCommand')
  await sheet.getByRole('button', { name: 'Import 1 host' }).click()
  await expect(sheet).toBeHidden()

  // Pinned at import: no "New key" chip, and connecting asks nothing.
  const card = hosts.locator('.hostcard', { hasText: 'work' })
  await expect(card).toBeVisible()
  await expect(card).not.toContainText('New key')
  await hosts.getByRole('button', { name: 'work', exact: true }).click()
  await waitOpen(win)
  await expect(win.getByRole('dialog', { name: 'Unknown host key' })).toHaveCount(0)
  await win.locator('.xterm').click()
  await win.keyboard.type('echo import-ok')
  await expect(win.locator('.xterm-rows')).toContainText('echo import-ok')
})
```

- [ ] **Step 3: Run the e2e suite**

Run: `cd desktop && npm run e2e`
Expected: every spec passes (`import.spec.ts` included), none newly skipped. `npm run e2e` rebuilds the renderer first; a stale build fails this test.

- [ ] **Step 4: Commit**

```bash
git add desktop/e2e
git commit -m "test(desktop): e2e import from an SSH config with a known_hosts pin

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 6: Docs

**Files:**
- Modify: `CLAUDE.md`, `README.md`, `docs/superpowers/ROADMAP.md`, `docs/superpowers/specs/2026-09-26-slice2b-ssh-config-import-design.md`

- [ ] **Step 1: `CLAUDE.md`**

- In the Commands block, extend the `sshtestd` line: after `-password=<pw>` add ` [-write-known-hosts=<path>]`, and after "writes a ready vault" add "; `-write-known-hosts` writes its host key as a known_hosts line".
- In **Hub and bridge**, in the UI-door method list: add `import.scan`, `import.apply` after `servers.forgetHostKey`, and change "protocol 2" to "protocol 3".
- After the **Host writes** bullet, add:

```markdown
- Import (`import.go`, `internal/sshconfig`): `import.scan` lists every alias in `~/.ssh/config` (`Options.SSHConfigPath`, `hub --sshConfig`; `Include` followed, patterns dropped) and resolves each with `ssh -G` (found with `exec.LookPath`, 5 s timeout, never dials): the first existing `IdentityFile` gives key auth, else agent; `ProxyJump`/`ProxyCommand` hosts are skipped; the pin comes only from the `known_hosts` files `ssh -G` names (`sshx.KnownHostKey`: ed25519, then ecdsa, then rsa; never a revoked key). `import.apply {aliases}` takes names only, recomputes every field, and adds the ready ones as new servers (`aiVisible` false, no secrets) in one `config.Update`, with one `import` config audit record each. Both need an unlocked vault.
```

- In **`desktop/`**, first bullet: after "`SSHGATE_STORE` becomes `--store=`;" add " `SSHGATE_SSH_CONFIG` becomes `--sshConfig=`;".
- In the **Hosts and host keys** bullet: add `ImportSheet.tsx` to the file list, and append "The Hosts tab's Import from SSH config opens `ImportSheet`, which checks Ready rows by default and sends only alias names."

- [ ] **Step 2: `README.md`**

After step 3 ("**Add a host.** …") add a paragraph:

```markdown
   Already use `ssh`? Click **Import from SSH config** instead. It lists the hosts in `~/.ssh/config` and pins each host key from `~/.ssh/known_hosts`, so the first connect asks nothing. Hosts behind `ProxyJump` or `ProxyCommand` are skipped. Passwords are not in `ssh_config`: add them in the editor afterwards.
```

- [ ] **Step 3: `docs/superpowers/ROADMAP.md`**

- Updated line: `Updated: 2026-09-26 (renamed ssh-mcp → sshgate, own public repo lang315/sshgate; slice 2a closed, `sshgate web` removed; 2b split, 2b-1 import implemented)`.
- Replace the slice 2b row with two rows:

```markdown
| 2b-1 | `~/.ssh/config` import with `known_hosts` pins | Implemented 2026-09-26; exit gate pending (author imports their real config) | `specs/2026-09-26-slice2b-ssh-config-import-design.md` | 2a | A real config to import | The author's hosts import pinned and open with no Trust prompt |
| 2b-2 | ProxyJump (one hop first) | Not specced | — | 2b-1 | The author has a real host behind a bastion | Bastion host connects and runs an approved AI command |
```

- In the 2c and 5 rows, set Status to `Not needed now (2026-09-26)`.
- In **Findings carried forward**, at the start of the "Slice 2 review → 2b" entry's heading line, change `→ 2b` to `→ 2b-2`, and add as its first sub-bullet: `- Answered by 2b-1: \`Host *\`, \`Include\`, alias as hostname, keep-or-refuse \`ProxyJump\`, name validation and dedupe, \`known_hosts\` fingerprints. The jump-chain findings below stay for 2b-2.`

- [ ] **Step 4: Spec status**

In the spec, change `Status: Draft, awaiting review.` to `Status: Approved 2026-09-26; implemented (plan \`plans/2026-09-26-slice2b-ssh-config-import.md\`).`

- [ ] **Step 5: Commit and push**

```bash
git add CLAUDE.md README.md docs/superpowers
git commit -m "docs: slice 2b-1 import from ~/.ssh/config

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
git push sshgate sshgate-main:main
```

Then watch CI for that push (`gh run list -R lang315/sshgate -b main -L 1`) until both jobs finish; both must pass.

The spec's exit gate is manual and belongs to the author: import the real `~/.ssh/config` in the app, check that `fviainboxes-server`, `fviainboxes-db` and `buildpc` arrive pinned and open with no Trust prompt, and that `orb` is listed as skipped ("needs ProxyCommand"). The ROADMAP row changes to Done only after that.
