# Desktop Slice 2a: Host Management and Host-Key Prompt Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the vault and add, edit, and delete hosts inside the desktop app, confirm every host key by its fingerprint before it is pinned, and route every vault write through one locked, MAC-checked `config.Update`.

**Architecture:** `internal/config` gains `Update` (the only store writer), `ServerInput`/`ApplyServer` (validation and write-only secret semantics), and a guarded `RecordHostKey`. `internal/sshx` gains a strict host-key mode with typed errors, algorithm pinning, `Registry.Close`, and a `known_hosts` hint. The hub gets UI-door methods `vault.create`, `servers.save`, `servers.delete`, `servers.forgetHostKey`, returns host-key outcomes from `term.open` as results, binds AI approvals to `user@host:port` and the pin, and audits config changes. The renderer adds a Create vault screen, a host editor, a queued host-key prompt, and a mismatch dialog. The UI-door protocol goes to version 2.

**Tech Stack:** Go 1.26 (`golang.org/x/crypto/ssh`, `ssh/knownhosts`), Electron 44, React 19, TypeScript 5, Vitest 5, `@playwright/test` (Electron mode). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-25-desktop-slice2a-design.md` (binding). Slice 1 spec `docs/superpowers/specs/2026-09-24-desktop-app-design.md` still holds where 2a does not change it. Standing decisions: `docs/superpowers/ROADMAP.md`.

## Global Constraints

- `config.Update(path, masterKey, fn)` is the only way to write the store: package `sync.Mutex`, then `withFlock` (unix; mutex only elsewhere), then `Load`; for a store with a KDF check `masterKey` and verify the MAC; run `fn`; `Save`. A KDF store without a key is refused.
- Every UI-door write needs an unlocked vault; each runs through `config.Update`, then `h.Reload()`. New methods are request-only and count as UI activity. Error codes: `-32602` for invalid params, `-32000` with a message for everything else.
- `vault.create {password}`: password at least 8 characters; refused if a KDF exists or any kept server has an `Enc*` field; every kept server's `aiVisible` becomes false; `Save`, `Reload`, and setting `deps.MasterKey` in one `h.mu` critical section; no second Argon2 run.
- `servers.save {original?, server}`: `server` is `config.ServerInput` (`name, host, port, user, auth, keyPath, aiVisible` plus `*string` `password, suPassword, sudoPassword, keyPassphrase`). Secret `nil` keeps, `""` clears, any other value sets. A kept secret whose AAD changes (name, user, auth) is re-encrypted. A change of host or port clears `hostKey`, `hostKeyAlgo`, and every secret not re-supplied. `hostKey` is not an input.
- Validation messages are field-specific: name 1–64 characters of `A-Z a-z 0-9 . _ -`; host and user non-empty with no whitespace or control characters; port 1–65535; auth `password`, `key`, or `agent`, where `key` requires `keyPath`.
- After a save: `Registry.Close(original or name)` only if the dial config changed (host, port, user, auth, keyPath, any secret, the pin; a rename always closes), never for an `aiVisible`-only change; then deny that server's pending AI requests with reason `"server changed"`. `servers.delete {name}`: `Registry.Close(name)` and deny its pending requests. `servers.forgetHostKey {name}`: clear `hostKey` and `hostKeyAlgo`, then `Registry.Close(name)`.
- `status` adds `hasVault` and `storePath`. `servers` adds `keyPath`, `hostKeyAlgo`, `hasPassword`, `hasSuPassword`, `hasSudoPassword`, `hasKeyPassphrase`; still no secret.
- No hub path learns a host key. Strict mode: empty pin → `*HostKeyUnknownError{Fingerprint, KeyType}`, different key → `*HostKeyMismatchError{Pinned, Presented}` wrapping `ErrHostKeyMismatch`. `--host` mode keeps TOFU; the web UI's Test connection keeps TOFU.
- `term.open` host-key outcomes are results, never errors: `{status: "open"}`, `{status: "hostKeyUnknown", server, host, port, user, fingerprint, keyType, knownHosts}`, `{status: "hostKeyMismatch", server, host, port, user, pinned, presented}`. `knownHosts` is `"match"`, `"different"`, or `"absent"`, read from `~/.ssh/known_hosts`, never pinned. A trusted retry never replies `hostKeyMismatch`.
- AI exec: `user@host:port` and the pin are recorded before `broker.Submit`; after approval all four must match or the exec fails with `"server changed"` and is audited. The approval card shows `user@host:port`.
- Config audit records: `trust {server, host, port, fingerprint, algo}`, `forgetHostKey {server, oldFingerprint}`, `delete {server}`, `vaultCreate {keptServers}`, `save {server, changed}`. No secret value ever.
- Protocol: `hello` → `protocol: 2` (`ProtocolVersion` in `internal/hub/idle.go`, `PROTOCOL_VERSION` in `desktop/src/shared/protocol.ts`), changed in the same commit.
- Renderer copy (verbatim): "Changing host or port forgets the host key and saved passwords unless you re-enter them."; "Saving will close N open tabs"; footer "Vault file: … — copy it to back up"; secret placeholder "saved". Host-key prompt: Cancel is the default with focus; Trust is mouse-only and disabled for 500 ms, restarting whenever the dialog's content changes; prompts from several tabs queue. The mismatch dialog has only Close and "Open host editor" and cannot re-pin.
- Go: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...` before every Go commit. Desktop: `cd desktop && npm run typecheck && npm test` before every desktop commit. Go tests use `internal/sshx/sshtest`, never Docker.
- Match existing style: terse Go, few comments, no new abstractions. Commit messages: Conventional Commits, ending with a blank line and exactly:
  `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`
  `Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2`

## Plan decisions

Where the spec leaves a detail open, this plan decides it:

1. `config.Update` on a missing file starts from `File{Version: 1, Servers: []}` and creates the directory. `vault.create` on an empty store needs this. Any other read error still fails.
2. `config.Update` writes nothing when `fn` returns an error. `RecordHostKey` returns the new `config.ErrPinSkipped` when it would change nothing, so the hub can fail the open as spec step 2 requires.
3. `RecordHostKey(path, name, host, port, fingerprint, algo, masterKey)` carries the endpoint guard and the algorithm. The hub's trust and the web UI's TOFU both use it; the web passes `algo ""`, because its learner only sees the fingerprint.
4. Hub writes (`servers.*` and the trust record) need a vault key: no KDF gives "create a vault first", a locked vault gives `ErrLocked`. `ApplyServer` also refuses to encrypt with a nil key. Without this, a KDF-less store would get secrets "encrypted" under HKDF of an empty key.
5. Strict mode is an explicit `DialConfig.StrictHostKey`, set in the file branch of `Deps.Resolve`, which only the hub uses. It cannot be inferred from a nil `OnLearnHostKey`, because the `--host` branch already has no learner and relies on the manager's own TOFU. `HostKeyCallback` treats a nil learner as strict.
6. `HostKeyUnknownError` and `HostKeyMismatchError` also carry `KeyType` and the presented `ssh.PublicKey`. The `knownHosts` hint needs the key, and the post-trust `hostKeyUnknown` reply needs the presented key's fingerprint and type.
7. `trustHostKey` is `{fingerprint, keyType}`, where `keyType` is copied from the `hostKeyUnknown` reply. The hub dials with `HostKey = fingerprint` and `HostKeyAlgo = keyType`, then records both. The manager's `ConfigHash` then equals that of the stored config, so the next `Registry.Get` reuses the connection instead of closing the tab that was just trusted.
8. `HostKeyAlgo` is part of `ConfigHash`; `StrictHostKey` is not, since one process never mixes the two modes. Algorithm families: `ssh-rsa` → `rsa-sha2-512, rsa-sha2-256, ssh-rsa`; any other type → itself.
9. `Registry.Get` never fills an empty pin from the cached manager when the config is strict. Strict managers never learn a key, but a pin cleared outside the hub (by the web UI) must still lead to a prompt.
10. `ApplyServer` keeps a secret's ciphertext unchanged when its AAD is unchanged, so an `aiVisible` toggle leaves the stored server byte-identical apart from the flag. It also clears `keyPath` unless auth is `key`.
11. "Dial config changed" means `config.Server` inequality ignoring `AIVisible`. That covers the name, endpoint, user, auth, keyPath, every ciphertext, and the pin.
12. Denying a server's pending requests uses `Broker.Pending()` plus `Decide(Denied, "server changed")`, so the broker API does not change.
13. The approval binding is `broker.Request.Target = user@host:port` (built with `net.JoinHostPort`). The pin is compared inside `Exec` and not sent to the UI. The error is `ErrServerChanged` ("server changed").
14. Config audit uses a separate `broker.ConfigRecord` (`kind: "config"`) written by `Audit.WriteConfig`; the existing exec records are unchanged. `changed` also lists `hostKey` before→after, because the rationale is "with which pin". A secret's name is listed when the save re-supplied it or dropped it.
15. `hub.Options.KnownHostsPath` is a test seam; empty means `~/.ssh/known_hosts`. A missing or unreadable file gives `"absent"`, and a `@revoked` entry gives `"different"`.
16. The spec's "spy on `config.Update`" is a byte comparison of the store before and after, plus an assertion that every resolved hub config is strict with no learner. There is no production test hook.
17. `servers.forgetHostKey` on an unpinned server succeeds (idempotent); an unknown name gives "not found".
18. The web UI's first-run also goes through `config.Update`, keeping its current behaviour of starting with an empty server list. `internal/web/flock_*.go` become unused and are deleted.
19. The Go and TypeScript protocol bumps land in one commit (Task 8). From Task 5 until Task 10 the renderer treats a `{status: "hostKeyUnknown"}` result as a successful open. Do not fix this early: the smoke and idle e2e tests still pass because `sshtestd -write-store` pre-pins `box`.
20. Renderer details: delete confirmation uses `window.confirm`; the host editor is a modal; the Trust delay reuses `ListChanges` and `ALLOW_DELAY_MS` from `approvals.ts`; "Open host editor" opens the editor with Forget focused. The app refetches `servers` once when the editor opens and after a trusted open. These are one-shot fetches, not polling.
21. In `hosts.spec.ts` step 4, the port is edited to a dead port and then back, because `sshtestd` listens on one port. The pin went with the old endpoint, so connecting prompts again, and step 5 then forgets that new pin.
22. `vault.create` checks for an existing KDF on a fresh `config.Load` before deriving the key, not inside `Update`'s `fn`. `Update` authenticates the key before `fn` runs, so a check there is unreachable and an existing vault would fail only as "wrong master key". A concurrent create still fails in `Update` that way.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/config/update.go` (new) | `Update`: the only store writer |
| `internal/config/server_input.go` (new) | `ServerInput`, `Validate`, `ApplyServer` (create/update/rename, write-only secrets, endpoint change) |
| `internal/config/store.go` (modify) | `Server.HostKeyAlgo` |
| `internal/config/hostkey_store.go` (modify) | `RecordHostKey` over `Update` with the endpoint guard; `ErrPinSkipped` |
| `internal/config/flock_*.go` (modify) | comments only |
| `internal/web/session.go`, `testconn.go` (modify) | `saveLocked` and first-run through `Update`; new `RecordHostKey` call |
| `internal/web/flock_unix.go`, `flock_other.go` (delete) | no longer used |
| `internal/sshx/hostkey.go` (modify) | strict mode, typed errors, `hostKeyAlgorithms` |
| `internal/sshx/knownhosts.go` (new) | `KnownHostsHint` |
| `internal/sshx/manager.go`, `registry.go` (modify) | `StrictHostKey`, `HostKeyAlgo`, `Registry.Close`, strict pin rule |
| `internal/mcpserver/resolve.go` (modify) | file branch strict, no learner, carries the algorithm |
| `internal/hub/hosts.go` (new) | `CreateVault`, `SaveServer`, `DeleteServer`, `ForgetHostKey`, `recordTrust`, write-key and pending-denial helpers, config audit |
| `internal/hub/hub.go` (modify) | no learner, `reloadLocked`, `Options.KnownHostsPath`, approval endpoint binding |
| `internal/hub/uidoor.go` (modify) | `status` fields, `servers` fields, new methods |
| `internal/hub/term.go` (modify) | `term.open` results, trusted retry, known_hosts hint |
| `internal/hub/idle.go` (modify) | `ProtocolVersion = 2` |
| `internal/broker/broker.go`, `audit.go` (modify) | `Request.Target`; `ConfigRecord`, `WriteConfig` |
| `desktop/src/shared/protocol.ts` (modify) | v2 types and method whitelist |
| `desktop/src/renderer/transport.ts` (modify) | host methods, `termOpen` result and trust param |
| `desktop/src/renderer/shell.ts` (modify) | `create-vault` screen |
| `desktop/src/renderer/hostForm.ts` (new) | pure editor logic: draft, secret states, warnings |
| `desktop/src/renderer/CreateVault.tsx`, `HostEditor.tsx` (new) | Create vault screen, host editor |
| `desktop/src/renderer/hostkeys.ts`, `HostKeyDialog.tsx` (new) | host-key prompt queue, prompt and mismatch dialogs |
| `desktop/src/renderer/HostList.tsx`, `App.tsx`, `TermView.tsx`, `TerminalTabs.tsx`, `terminals.ts`, `ApprovalPanel.tsx`, `styles.css` (modify) | wiring |
| `desktop/test/*.test.ts`, `desktop/test/fixtures/fakeHub.mjs` (modify/new) | unit tests |
| `desktop/e2e/launch.ts` (modify), `desktop/e2e/hosts.spec.ts` (new) | e2e |
| `cmd/ssh-mcp/e2e_test.go` (modify) | new `RecordHostKey` signature |
| `CLAUDE.md`, `README.md`, `docs/superpowers/ROADMAP.md` (modify) | docs |

---

### Task 1: `config.Update`, `ServerInput`, guarded `RecordHostKey`, web writes through `Update`

**Files:**
- Create: `internal/config/update.go`, `internal/config/update_test.go`, `internal/config/server_input.go`, `internal/config/server_input_test.go`, `internal/web/update_test.go`
- Modify: `internal/config/store.go:15-28` (Server), `internal/config/hostkey_store.go`, `internal/config/hostkey_store_test.go`, `internal/config/flock_unix.go:5`, `internal/config/flock_other.go:5`, `internal/web/session.go:82-94,166-187`, `internal/web/testconn.go:56-57`, `cmd/ssh-mcp/e2e_test.go:88`
- Delete: `internal/web/flock_unix.go`, `internal/web/flock_other.go`

**Interfaces:**
- Consumes: existing `Load`, `Save`, `KDF.Verify`, `File.VerifyMAC`, `Encrypt`, `Decrypt`, `aadFor`, `withFlock`, `forbiddenRune`.
- Produces:
  - `func Update(path string, masterKey []byte, fn func(*File) error) error`
  - `type ServerInput struct { Name, Host string; Port int; User, Auth, KeyPath string; AIVisible bool; Password, SuPassword, SudoPassword, KeyPassphrase *string }` (JSON tags `name host port user auth keyPath aiVisible password suPassword sudoPassword keyPassphrase`)
  - `func (in ServerInput) Validate() error`; messages start with `name:`, `host:`, `port:`, `user:`, `auth:`, `keyPath:`
  - `func ApplyServer(f *File, original string, in ServerInput, masterKey []byte) (before, after Server, err error)`
  - `Server.HostKeyAlgo string` (`json:"hostKeyAlgo,omitempty"`)
  - `var ErrPinSkipped error`; `func RecordHostKey(path, name, host string, port int, fingerprint, algo string, masterKey []byte) error`

- [ ] **Step 1: Write the failing `Update` tests**

Create `internal/config/update_test.go`:

```go
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func vaultAt(t *testing.T) (string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "servers.json")
	k, mk, err := NewKDF("pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, &File{Version: 1, KDF: &k, Servers: []Server{}}, mk); err != nil {
		t.Fatal(err)
	}
	return path, mk
}

func TestUpdateSerializesConcurrentWriters(t *testing.T) {
	path, mk := vaultAt(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := Update(path, mk, func(f *File) error {
				f.Servers = append(f.Servers, Server{Name: fmt.Sprintf("s%d", i), Host: "h", Port: 22, User: "u", Auth: "agent"})
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Servers) != 20 || f.Revision != 21 {
		t.Fatalf("lost writes: %d servers, revision %d", len(f.Servers), f.Revision)
	}
	if err := f.VerifyMAC(mk); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRefusesEncryptedStoreWithoutItsKey(t *testing.T) {
	path, _ := vaultAt(t)
	_, wrong, _ := NewKDF("other")
	before, _ := os.ReadFile(path)
	for _, key := range [][]byte{nil, wrong} {
		called := false
		err := Update(path, key, func(*File) error { called = true; return nil })
		if err == nil || called {
			t.Fatalf("key given=%v: err %v, fn called %v", key != nil, err, called)
		}
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("a refused update changed the file")
	}
}

func TestUpdateVerifiesMACBeforeWriting(t *testing.T) {
	path, mk := vaultAt(t)
	raw, _ := os.ReadFile(path)
	tampered := bytes.Replace(raw, []byte(`"servers": []`),
		[]byte(`"servers": [{"name":"evil","host":"x","port":22,"user":"u","auth":"agent","aiVisible":true}]`), 1)
	if bytes.Equal(raw, tampered) {
		t.Fatal("tamper did not apply")
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	err := Update(path, mk, func(*File) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "MAC") {
		t.Fatalf("want a MAC error, got %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, tampered) {
		t.Fatal("a tampered store was re-signed")
	}
}

func TestUpdateStartsEmptyAndWritesNothingOnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "servers.json")
	if err := Update(path, nil, func(*File) error { return errors.New("no") }); err == nil {
		t.Fatal("fn error not returned")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a failed update created the store: %v", err)
	}
	err := Update(path, nil, func(f *File) error {
		f.Servers = append(f.Servers, Server{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil || f.Version != 1 || f.Revision != 1 || len(f.Servers) != 1 {
		t.Fatalf("%v %+v", err, f)
	}
}
```

- [ ] **Step 2: Write the failing `ServerInput`/`ApplyServer` tests**

Create `internal/config/server_input_test.go`:

```go
package config

import (
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

func withSecrets(in ServerInput, pw, su, sudo, kp *string) ServerInput {
	in.Password, in.SuPassword, in.SudoPassword, in.KeyPassphrase = pw, su, sudo, kp
	return in
}

var secretFields = [4]string{"encPassword", "encSuPassword", "encSudoPassword", "encKeyPassphrase"}

// plain decrypts s's four secrets ("" when unset).
func plain(t *testing.T, f *File, s Server, mk []byte) [4]string {
	t.Helper()
	var out [4]string
	for i, blob := range [4]string{s.EncPassword, s.EncSuPassword, s.EncSudoPassword, s.EncKeyPassphrase} {
		if blob == "" {
			continue
		}
		pt, err := Decrypt(mk, s.Name+"/"+secretFields[i], aadFor(f, s, secretFields[i]), blob)
		if err != nil {
			t.Fatalf("%s: %v", secretFields[i], err)
		}
		out[i] = pt
	}
	return out
}

func TestServerInputValidate(t *testing.T) {
	ok := ServerInput{Name: "box-1.a_b", Host: "h.example", Port: 22, User: "u", Auth: "password"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		edit func(*ServerInput)
		want string
	}{
		{func(s *ServerInput) { s.Name = "" }, "name:"},
		{func(s *ServerInput) { s.Name = "a b" }, "name:"},
		{func(s *ServerInput) { s.Name = strings.Repeat("x", 65) }, "name:"},
		{func(s *ServerInput) { s.Host = "" }, "host:"},
		{func(s *ServerInput) { s.Host = "h h" }, "host:"},
		{func(s *ServerInput) { s.Host = "h\x00" }, "host:"},
		{func(s *ServerInput) { s.Host = "h​" }, "host:"},
		{func(s *ServerInput) { s.Port = 0 }, "port:"},
		{func(s *ServerInput) { s.Port = 65536 }, "port:"},
		{func(s *ServerInput) { s.User = "" }, "user:"},
		{func(s *ServerInput) { s.User = "u\t" }, "user:"},
		{func(s *ServerInput) { s.Auth = "kerberos" }, "auth:"},
		{func(s *ServerInput) { s.Auth = "key" }, "keyPath:"},
	} {
		in := ok
		c.edit(&in)
		if err := in.Validate(); err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%+v: want %q..., got %v", in, c.want, err)
		}
	}
}

func TestApplyServerSecretSemantics(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	f := &File{Version: 1}
	base := ServerInput{Name: "box", Host: "h", Port: 22, User: "u", Auth: "password"}
	if _, _, err := ApplyServer(f, "", withSecrets(base, ptr("p1"), ptr("s1"), ptr("d1"), ptr("k1")), mk); err != nil {
		t.Fatal(err)
	}
	if got := plain(t, f, f.Servers[0], mk); got != [4]string{"p1", "s1", "d1", "k1"} {
		t.Fatalf("create: %v", got)
	}
	// nil keeps the stored value; an unchanged AAD keeps the very same ciphertext.
	kept := f.Servers[0]
	if _, _, err := ApplyServer(f, "box", base, mk); err != nil {
		t.Fatal(err)
	}
	if f.Servers[0] != kept {
		t.Fatalf("nil secrets changed the server:\n%+v\n%+v", kept, f.Servers[0])
	}
	// A value sets; "" clears.
	if _, _, err := ApplyServer(f, "box", withSecrets(base, ptr("p2"), ptr(""), ptr("d2"), ptr("")), mk); err != nil {
		t.Fatal(err)
	}
	if got := plain(t, f, f.Servers[0], mk); got != [4]string{"p2", "", "d2", ""} {
		t.Fatalf("set/clear: %v", got)
	}
}

func TestApplyServerRenameAndUserChangeReencrypt(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	f := &File{Version: 1}
	base := ServerInput{Name: "a", Host: "h", Port: 22, User: "u", Auth: "password"}
	if _, _, err := ApplyServer(f, "", withSecrets(base, ptr("p1"), ptr("s1"), nil, nil), mk); err != nil {
		t.Fatal(err)
	}
	renamed := base
	renamed.Name, renamed.User = "b", "root"
	if _, _, err := ApplyServer(f, "a", renamed, mk); err != nil {
		t.Fatal(err)
	}
	if len(f.Servers) != 1 || f.Servers[0].Name != "b" {
		t.Fatalf("rename: %+v", f.Servers)
	}
	if got := plain(t, f, f.Servers[0], mk); got != [4]string{"p1", "s1", "", ""} {
		t.Fatalf("secrets after rename: %v", got)
	}
}

func TestApplyServerNewEndpointDropsPinAndUnsuppliedSecrets(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	base := ServerInput{Name: "a", Host: "h", Port: 22, User: "u", Auth: "password"}
	for name, move := range map[string]func(*ServerInput){
		"port": func(s *ServerInput) { s.Port = 2222 },
		"host": func(s *ServerInput) { s.Host = "h2" },
	} {
		f := &File{Version: 1}
		if _, _, err := ApplyServer(f, "", withSecrets(base, ptr("p1"), ptr("s1"), ptr("d1"), nil), mk); err != nil {
			t.Fatal(err)
		}
		f.Servers[0].HostKey, f.Servers[0].HostKeyAlgo = "SHA256:x", "ssh-ed25519"
		in := withSecrets(base, nil, ptr("s2"), nil, nil)
		move(&in)
		if _, _, err := ApplyServer(f, "a", in, mk); err != nil {
			t.Fatal(err)
		}
		s := f.Servers[0]
		if s.HostKey != "" || s.HostKeyAlgo != "" {
			t.Fatalf("%s: pin kept: %+v", name, s)
		}
		if got := plain(t, f, s, mk); got != [4]string{"", "s2", "", ""} {
			t.Fatalf("%s: secrets %v", name, got)
		}
	}
}

func TestApplyServerAIVisibleOnlyKeepsEverythingElse(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	f := &File{Version: 1}
	base := ServerInput{Name: "a", Host: "h", Port: 22, User: "u", Auth: "password"}
	if _, _, err := ApplyServer(f, "", withSecrets(base, ptr("p1"), nil, nil, nil), mk); err != nil {
		t.Fatal(err)
	}
	f.Servers[0].HostKey, f.Servers[0].HostKeyAlgo = "SHA256:x", "ssh-ed25519"
	want := f.Servers[0]
	want.AIVisible = true
	in := base
	in.AIVisible = true
	_, after, err := ApplyServer(f, "a", in, mk)
	if err != nil {
		t.Fatal(err)
	}
	if after != want || f.Servers[0] != want {
		t.Fatalf("got %+v, want %+v", after, want)
	}
}

func TestApplyServerNamesKeyPathAndKeylessSecrets(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	f := &File{Version: 1}
	a := ServerInput{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent", KeyPath: "/stale"}
	b := a
	b.Name = "b"
	if _, s, err := ApplyServer(f, "", a, mk); err != nil || s.KeyPath != "" {
		t.Fatalf("keyPath must be dropped unless auth is key: %v %+v", err, s)
	}
	if _, _, err := ApplyServer(f, "", b, mk); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyServer(f, "", a, mk); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate create: %v", err)
	}
	if _, _, err := ApplyServer(f, "a", b, mk); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("rename onto another server: %v", err)
	}
	if _, _, err := ApplyServer(f, "nope", a, mk); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing original: %v", err)
	}
	if _, _, err := ApplyServer(f, "a", withSecrets(a, ptr("x"), nil, nil, nil), nil); err == nil {
		t.Fatal("a secret was stored without a vault key")
	}
	if len(f.Servers) != 2 || f.Servers[0].EncPassword != "" {
		t.Fatalf("a failed apply changed the file: %+v", f.Servers)
	}
}
```

- [ ] **Step 3: Rewrite the `RecordHostKey` tests for the new signature**

Replace the whole of `internal/config/hostkey_store_test.go` with:

```go
package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecordHostKeyPinsWhenEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "servers.json")
	k, mk, _ := NewKDF("masterpw1")
	f := &File{Version: 1, KDF: &k, Servers: []Server{{Name: "s", Host: "h", Port: 22, User: "u", Auth: "password"}}}
	if err := Save(p, f, mk); err != nil {
		t.Fatal(err)
	}
	if err := RecordHostKey(p, "s", "h", 22, "SHA256:abc", "ssh-ed25519", mk); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(p)
	s, _ := got.FindServer("s")
	if s.HostKey != "SHA256:abc" || s.HostKeyAlgo != "ssh-ed25519" {
		t.Fatalf("got %q %q", s.HostKey, s.HostKeyAlgo)
	}
	if err := got.VerifyMAC(mk); err != nil {
		t.Fatal(err)
	}
}

// A pinned server is never re-pinned, and a key dialled at one endpoint is
// never recorded for a server that now points somewhere else.
func TestRecordHostKeySkipsPinnedMovedOrMissingServer(t *testing.T) {
	p := filepath.Join(t.TempDir(), "servers.json")
	k, mk, _ := NewKDF("masterpw1")
	f := &File{Version: 1, KDF: &k, Servers: []Server{
		{Name: "pinned", Host: "h", Port: 22, User: "u", Auth: "password", HostKey: "SHA256:original"},
		{Name: "open", Host: "h", Port: 22, User: "u", Auth: "password"},
	}}
	if err := Save(p, f, mk); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	for _, c := range []struct {
		name, host string
		port       int
	}{{"pinned", "h", 22}, {"open", "h2", 22}, {"open", "h", 2222}, {"gone", "h", 22}} {
		if err := RecordHostKey(p, c.name, c.host, c.port, "SHA256:attacker", "", mk); !errors.Is(err, ErrPinSkipped) {
			t.Fatalf("%+v: want ErrPinSkipped, got %v", c, err)
		}
	}
	if after, _ := os.ReadFile(p); !bytes.Equal(before, after) {
		t.Fatal("a skipped record changed the file")
	}
}
```

- [ ] **Step 4: Run the config tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestUpdate|TestServerInput|TestApplyServer|TestRecordHostKey' -v`
Expected: FAIL to compile, e.g. `undefined: Update`, `undefined: ServerInput`, `too many arguments in call to RecordHostKey`.

- [ ] **Step 5: Add `HostKeyAlgo` to `config.Server`**

In `internal/config/store.go`, replace the `HostKey` line of `type Server` so the struct reads:

```go
type Server struct {
	Name             string `json:"name"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	User             string `json:"user"`
	Auth             string `json:"auth"`
	KeyPath          string `json:"keyPath,omitempty"`
	HostKey          string `json:"hostKey,omitempty"`
	HostKeyAlgo      string `json:"hostKeyAlgo,omitempty"` // pinned key's type, e.g. ssh-ed25519
	AIVisible        bool   `json:"aiVisible,omitempty"`
	EncPassword      string `json:"encPassword,omitempty"`
	EncSuPassword    string `json:"encSuPassword,omitempty"`
	EncSudoPassword  string `json:"encSudoPassword,omitempty"`
	EncKeyPassphrase string `json:"encKeyPassphrase,omitempty"`
}
```

`omitempty` keeps the MAC of existing files unchanged (the empty field is not marshalled).

- [ ] **Step 6: Write `config.Update`**

Create `internal/config/update.go`:

```go
package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// updateMu serializes writers in this process; withFlock serializes
// processes (a no-op off unix, where this mutex is all there is).
var updateMu sync.Mutex

// Update is the only writer of the store. It reloads the file (a missing one
// starts empty), checks the key and MAC of an encrypted store, applies fn,
// and saves. Nothing is written if any step fails.
func Update(path string, masterKey []byte, fn func(*File) error) error {
	updateMu.Lock()
	defer updateMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return withFlock(path, func() error {
		f, err := Load(path)
		if os.IsNotExist(err) {
			f, err = &File{Version: 1, Servers: []Server{}}, nil
		}
		if err != nil {
			return err
		}
		if f.KDF != nil {
			if masterKey == nil {
				return errors.New("store is encrypted; unlock it before saving")
			}
			if !f.KDF.Verify(masterKey) {
				return errors.New("wrong master key for this store")
			}
			if err := f.VerifyMAC(masterKey); err != nil {
				return err
			}
		}
		if err := fn(f); err != nil {
			return err
		}
		return Save(path, f, masterKey)
	})
}
```

In `internal/config/flock_unix.go` and `internal/config/flock_other.go`, replace the line
`// ponytail: duplicated from internal/web to avoid a web→config layering issue; consolidate if a third user appears`
with
`// Only Update calls withFlock; every store writer goes through Update.`

- [ ] **Step 7: Write `ServerInput` and `ApplyServer`**

Create `internal/config/server_input.go`:

```go
package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ServerInput is a server as the app edits it. Secrets are write-only: nil
// keeps the stored value, "" clears it, anything else sets it. The host key
// is never an input.
type ServerInput struct {
	Name          string  `json:"name"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	User          string  `json:"user"`
	Auth          string  `json:"auth"`
	KeyPath       string  `json:"keyPath"`
	AIVisible     bool    `json:"aiVisible"`
	Password      *string `json:"password"`
	SuPassword    *string `json:"suPassword"`
	SudoPassword  *string `json:"sudoPassword"`
	KeyPassphrase *string `json:"keyPassphrase"`
}

var serverNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// plainToken: non-empty, no whitespace, no control or format runes.
func plainToken(s string) bool {
	_, _, bad := forbiddenRune(s)
	return s != "" && !bad && !strings.ContainsFunc(s, unicode.IsSpace)
}

// Validate names the first bad field.
func (in ServerInput) Validate() error {
	switch {
	case !serverNameRe.MatchString(in.Name):
		return errors.New("name: use 1-64 characters of A-Z a-z 0-9 . _ -")
	case !plainToken(in.Host):
		return errors.New("host: required, with no spaces or control characters")
	case in.Port < 1 || in.Port > 65535:
		return errors.New("port: must be 1-65535")
	case !plainToken(in.User):
		return errors.New("user: required, with no spaces or control characters")
	case in.Auth != "password" && in.Auth != "key" && in.Auth != "agent":
		return errors.New("auth: must be password, key, or agent")
	case in.Auth == "key" && in.KeyPath == "":
		return errors.New("keyPath: required for key auth")
	}
	return nil
}

// ApplyServer creates (original == "") or updates server original from in
// and returns it before and after. A kept secret is re-encrypted only when
// its AAD changes (name, user, auth). A new host or port drops the pin and
// every secret in does not re-supply: the AAD binds secrets to the endpoint,
// and carrying them over would hand them to whoever answers there.
func ApplyServer(f *File, original string, in ServerInput, masterKey []byte) (before, after Server, err error) {
	idx := -1
	if original != "" {
		for i := range f.Servers {
			if f.Servers[i].Name == original {
				idx = i
			}
		}
		if idx < 0 {
			return before, after, fmt.Errorf("server %q not found", original)
		}
		before = f.Servers[idx]
	}
	if in.Name != original {
		if _, dup := f.FindServer(in.Name); dup {
			return before, after, fmt.Errorf("a server named %q already exists", in.Name)
		}
	}
	if in.Auth != "key" {
		in.KeyPath = ""
	}
	after = Server{Name: in.Name, Host: in.Host, Port: in.Port, User: in.User, Auth: in.Auth, KeyPath: in.KeyPath, AIVisible: in.AIVisible}
	moved := idx >= 0 && (before.Host != in.Host || before.Port != in.Port)
	if idx >= 0 && !moved {
		after.HostKey, after.HostKeyAlgo = before.HostKey, before.HostKeyAlgo
	}
	for _, s := range []struct {
		field string
		in    *string
		old   string
		dst   *string
	}{
		{"encPassword", in.Password, before.EncPassword, &after.EncPassword},
		{"encSuPassword", in.SuPassword, before.EncSuPassword, &after.EncSuPassword},
		{"encSudoPassword", in.SudoPassword, before.EncSudoPassword, &after.EncSudoPassword},
		{"encKeyPassphrase", in.KeyPassphrase, before.EncKeyPassphrase, &after.EncKeyPassphrase},
	} {
		switch {
		case s.in != nil && *s.in == "", s.in == nil && (s.old == "" || moved):
			// cleared, nothing stored, or the endpoint changed: stays empty
		case s.in == nil && aadFor(f, before, s.field) == aadFor(f, after, s.field):
			*s.dst = s.old
		default:
			if masterKey == nil {
				return before, after, errors.New("saving a password needs an unlocked vault")
			}
			pt := ""
			if s.in != nil {
				pt = *s.in
			} else if pt, err = Decrypt(masterKey, before.Name+"/"+s.field, aadFor(f, before, s.field), s.old); err != nil {
				return before, after, fmt.Errorf("re-encrypting %s: %w", s.field, err)
			}
			if *s.dst, err = Encrypt(masterKey, after.Name+"/"+s.field, aadFor(f, after, s.field), pt); err != nil {
				return before, after, err
			}
		}
	}
	if idx >= 0 {
		f.Servers[idx] = after
	} else {
		f.Servers = append(f.Servers, after)
	}
	return before, after, nil
}
```

- [ ] **Step 8: Rewrite `RecordHostKey` over `Update`**

Replace the whole of `internal/config/hostkey_store.go` with:

```go
package config

import "errors"

// ErrPinSkipped means RecordHostKey changed nothing: the server is gone, is
// already pinned, or no longer points at the endpoint that was dialled.
var ErrPinSkipped = errors.New("host key not recorded: the server changed or is already pinned")

// RecordHostKey pins fingerprint (and its key type, "" if unknown) for
// server name, but only while it has no pin and still has host and port, so
// an edit that lands mid-dial never gets the old endpoint's key.
func RecordHostKey(path, name, host string, port int, fingerprint, algo string, masterKey []byte) error {
	return Update(path, masterKey, func(f *File) error {
		for i := range f.Servers {
			s := &f.Servers[i]
			if s.Name == name && s.HostKey == "" && s.Host == host && s.Port == port {
				s.HostKey, s.HostKeyAlgo = fingerprint, algo
				return nil
			}
		}
		return ErrPinSkipped
	})
}
```

- [ ] **Step 9: Run the config tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS for every test, including `TestUpdateSerializesConcurrentWriters`, `TestUpdateVerifiesMACBeforeWriting`, `TestApplyServerAIVisibleOnlyKeepsEverythingElse`, `TestRecordHostKeySkipsPinnedMovedOrMissingServer`.

- [ ] **Step 10: Write the failing web test**

The old `saveLocked` reloaded a tampered file and re-signed it with a fresh MAC. Create `internal/web/update_test.go`:

```go
package web

import (
	"encoding/json"
	"os"
	"testing"
)

// Web writes go through config.Update: a store whose MAC no longer verifies
// is refused, never re-signed.
func TestWebWriteRefusesTamperedStore(t *testing.T) {
	app, csrf := initApp(t)
	raw, err := os.ReadFile(app.Path)
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]any
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	f["servers"] = []any{map[string]any{"name": "evil", "host": "x", "port": 22, "user": "u", "auth": "agent", "aiVisible": true}}
	tampered, _ := json.Marshal(f)
	if err := os.WriteFile(app.Path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	w := doWrite(t, app, csrf, "POST", "/api/servers", `{"name":"ok","host":"h","port":22,"user":"u","auth":"agent"}`)
	if w.Code == 201 {
		t.Fatal("write to a tampered store succeeded")
	}
	if after, _ := os.ReadFile(app.Path); string(after) != string(tampered) {
		t.Fatal("tampered store was rewritten")
	}
}
```

Run: `go test ./internal/web/ -run TestWebWriteRefusesTamperedStore -v`
Expected: FAIL with `write to a tampered store succeeded`.

- [ ] **Step 11: Switch the web writers to `config.Update`**

In `internal/web/session.go`, replace these lines of `handleFirstRun`:

```go
	f := &config.File{Version: 1, KDF: &k, Servers: []config.Server{}}
	if err := config.Save(a.Path, f, mk); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a.file = f
```

with:

```go
	var f *config.File
	err = config.Update(a.Path, mk, func(cur *config.File) error {
		if cur.KDF != nil {
			return fmt.Errorf("already initialized")
		}
		cur.KDF, cur.Servers = &k, []config.Server{}
		f = cur
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a.file = f
```

Replace `saveLocked` and its comment (lines 166-187) with:

```go
// saveLocked runs mutate through config.Update, which reloads the file,
// checks its MAC, and serializes with every other writer, the hub included.
// It needs an unlocked session.
func (a *App) saveLocked(mutate func(*config.File) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess == nil {
		return fmt.Errorf("locked")
	}
	var saved *config.File
	err := config.Update(a.Path, a.sess.MasterKey, func(f *config.File) error {
		if err := mutate(f); err != nil {
			return err
		}
		saved = f
		return nil
	})
	if err == nil {
		a.file = saved
	}
	return err
}
```

In `internal/web/testconn.go`, change the learner line to:

```go
		_ = config.RecordHostKey(a.Path, s.Name, s.Host, s.Port, fp, "", sess.MasterKey)
```

Delete the now-unused helpers:

```bash
git rm internal/web/flock_unix.go internal/web/flock_other.go
```

In `cmd/ssh-mcp/e2e_test.go`, change line 88 to:

```go
		OnLearnHostKey: func(fp string) { _ = config.RecordHostKey(store, "box", srv.Host, srv.Port, fp, "", mk) }})
```

- [ ] **Step 12: Run the web tests and the full suite**

Run: `go test ./internal/web/ -v`
Expected: PASS, including `TestWebWriteRefusesTamperedStore` and `TestTestConnectionPinShowsInList`.

Run: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...`
Expected: all `ok`; no vet output.

- [ ] **Step 13: Commit**

```bash
git add internal/config internal/web cmd/ssh-mcp/e2e_test.go
git commit -m "$(cat <<'EOF'
feat(config): Update as the only store writer; ServerInput and guarded RecordHostKey

Every write reloads, checks key and MAC, and serializes under a mutex plus
flock. ApplyServer holds the write-only secret rules and drops the pin and
unsupplied secrets on a host or port change. The web UI writes through
Update, so it can no longer re-sign a tampered store.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
)"
```

---

### Task 2: Strict host keys, algorithm pinning, `Registry.Close`, no learner on hub paths

**Files:**
- Modify: `internal/sshx/hostkey.go` (whole file), `internal/sshx/manager.go:24-31` (DialConfig), `internal/sshx/manager.go:98-129` (ensure), `internal/sshx/registry.go`, `internal/mcpserver/resolve.go:111-126`, `internal/hub/hub.go:5-18,327-366`, `internal/hub/term_test.go:67-69` (`fakeServerHub` pins its server)
- Test: `internal/sshx/hostkey_test.go`, `internal/sshx/strict_test.go` (new), `internal/mcpserver/resolve_test.go`, `internal/hub/hub_test.go` (replace `TestResolveLearnHostKeyNeedsKeyForEncryptedVault`)

**Interfaces:**
- Consumes: `config.Server.HostKeyAlgo` (Task 1).
- Produces:
  - `type HostKeyUnknownError struct { Fingerprint, KeyType string; Key ssh.PublicKey }`
  - `type HostKeyMismatchError struct { Pinned, Presented, KeyType string; Key ssh.PublicKey }` with `Unwrap() error { return ErrHostKeyMismatch }`
  - `HostKeyCallback(pinned string, insecure bool, onLearn func(fp string))`: a nil `onLearn` is strict
  - `DialConfig.HostKeyAlgo string`, `DialConfig.StrictHostKey bool`
  - `func (r *Registry) Close(name string)`
  - `Deps.Resolve` file branch: `StrictHostKey: true`, `HostKeyAlgo: s.HostKeyAlgo`, `OnLearnHostKey == nil`

Existing tests this breaks, fixed in this task: `fakeServerHub`'s "fk" has no pin and relied on TOFU, so `TestTermOpenWriteInOrderAndClose`, `TestTermOpenRefusedWhileLocked`, and `TestTermCloseAll` would fail. `TestResolveLearnHostKeyNeedsKeyForEncryptedVault` tests the learner that is removed. `TestTOFULearns` and the registry TOFU tests stay: they are non-strict.

- [ ] **Step 1: Write the failing sshx tests**

In `internal/sshx/hostkey_test.go`, set the import block to:

```go
import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)
```

Replace `TestPinnedMismatchRejected` with:

```go
func TestPinnedMismatchRejected(t *testing.T) {
	pk := testKey(t)
	err := HostKeyCallback("sha256:WRONG", false, nil)("h:22", &net.TCPAddr{}, pk)
	var m *HostKeyMismatchError
	if !errors.As(err, &m) || !errors.Is(err, ErrHostKeyMismatch) || m.Pinned != "sha256:WRONG" ||
		m.Presented != Fingerprint(pk) || m.KeyType != ssh.KeyAlgoED25519 || m.Key == nil {
		t.Fatalf("got %v", err)
	}
}
```

Append:

```go
func TestStrictRefusesUnpinnedKey(t *testing.T) {
	pk := testKey(t)
	err := HostKeyCallback("", false, nil)("h:22", &net.TCPAddr{}, pk)
	var u *HostKeyUnknownError
	if !errors.As(err, &u) || u.Fingerprint != Fingerprint(pk) || u.KeyType != ssh.KeyAlgoED25519 || u.Key == nil {
		t.Fatalf("got %v", err)
	}
}

func TestHostKeyAlgorithms(t *testing.T) {
	if got := hostKeyAlgorithms(""); got != nil {
		t.Fatalf("no pinned algo must negotiate as before, got %v", got)
	}
	if got := hostKeyAlgorithms(ssh.KeyAlgoED25519); len(got) != 1 || got[0] != ssh.KeyAlgoED25519 {
		t.Fatalf("ed25519: %v", got)
	}
	got := hostKeyAlgorithms(ssh.KeyAlgoRSA)
	if len(got) != 3 || got[0] != ssh.KeyAlgoRSASHA512 || got[1] != ssh.KeyAlgoRSASHA256 || got[2] != ssh.KeyAlgoRSA {
		t.Fatalf("rsa family: %v", got)
	}
}
```

Create `internal/sshx/strict_test.go`:

```go
package sshx

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/sshx/sshtest"
	"golang.org/x/crypto/ssh"
)

func TestStrictDialNeverLearns(t *testing.T) {
	srv := sshtest.Start(t)
	learned := false
	m := NewManager(DialConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", Auth: "password", TimeoutMs: 30000,
		StrictHostKey: true, OnLearnHostKey: func(string) { learned = true }})
	t.Cleanup(m.Close)
	_, err := m.OpenSession()
	var u *HostKeyUnknownError
	if !errors.As(err, &u) || u.Fingerprint != srv.Fingerprint() {
		t.Fatalf("got %v", err)
	}
	if learned || m.currentConfig().HostKey != "" {
		t.Fatal("a strict dial learned a key")
	}
}

func TestPinnedAlgoLimitsNegotiation(t *testing.T) {
	srv := sshtest.Start(t)
	cfg := DialConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", Auth: "password", TimeoutMs: 30000,
		StrictHostKey: true, HostKey: srv.Fingerprint(), HostKeyAlgo: ssh.KeyAlgoED25519}
	m := NewManager(cfg)
	t.Cleanup(m.Close)
	sess, err := m.OpenSession()
	if err != nil {
		t.Fatal(err)
	}
	sess.Close()
	// sshtest has only an ed25519 key, so pinning another family leaves no common algorithm.
	cfg.HostKeyAlgo = ssh.KeyAlgoECDSA256
	m2 := NewManager(cfg)
	t.Cleanup(m2.Close)
	if _, err := m2.OpenSession(); err == nil || !strings.Contains(err.Error(), "no common algorithm") {
		t.Fatalf("want a negotiation failure, got %v", err)
	}
}

// A strict caller's empty pin means "unpinned": never filled from the cache.
func TestRegistryStrictIgnoresCachedPin(t *testing.T) {
	r := NewRegistry()
	pinned := DialConfig{Host: "h", Port: 22, User: "u", Auth: "agent", StrictHostKey: true, HostKey: "SHA256:x"}
	m1 := r.Get("a", pinned)
	unpinned := pinned
	unpinned.HostKey = ""
	if r.Get("a", unpinned) == m1 {
		t.Fatal("a strict caller with no pin reused a pinned manager")
	}
}

func TestRegistryCloseEndsTerminals(t *testing.T) {
	srv := sshtest.Start(t)
	r := NewRegistry()
	t.Cleanup(r.CloseAll)
	cfg := DialConfig{Host: srv.Host, Port: srv.Port, User: "u", Password: "p", Auth: "password", TimeoutMs: 30000,
		StrictHostKey: true, HostKey: srv.Fingerprint()}
	m1 := r.Get("a", cfg)
	exited := make(chan struct{})
	term, err := m1.OpenTerm(24, 80, func([]byte) {}, func(int, string) { close(exited) })
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	r.Close("a")
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal did not end when its manager was closed")
	}
	if r.Get("a", cfg) == m1 {
		t.Fatal("a closed manager was reused")
	}
}
```

- [ ] **Step 2: Write the failing resolve and hub tests**

Append to `internal/mcpserver/resolve_test.go`:

```go
// The file branch serves only the hub: strict, no learner, algorithm pinned.
func TestResolveNamedIsStrictWithNoLearner(t *testing.T) {
	f := &config.File{Version: 1, Servers: []config.Server{{Name: "p", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:x", HostKeyAlgo: "ssh-ed25519"}}}
	dc, err := (&Deps{File: f}).Resolve("p")
	if err != nil {
		t.Fatal(err)
	}
	if !dc.StrictHostKey || dc.OnLearnHostKey != nil || dc.HostKeyAlgo != "ssh-ed25519" {
		t.Fatalf("got %+v", dc)
	}
	cli, err := (&Deps{CLI: &config.CLIConfig{Host: "h", Port: 22, User: "u", HasHost: true}}).Resolve("")
	if err != nil || cli.StrictHostKey {
		t.Fatalf("--host mode must keep TOFU: %v %+v", err, cli)
	}
}
```

In `internal/hub/hub_test.go`, delete `TestResolveLearnHostKeyNeedsKeyForEncryptedVault` (lines 600-632) and append:

```go
// No hub path can learn a host key: every resolved config is strict with no
// learner, and an AI exec on an unpinned server leaves the store untouched.
func TestHubNeverLearnsHostKeys(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	for _, name := range []string{"vis", "nokey", "hid"} {
		dc, err := h.Resolve(name)
		if err != nil {
			t.Fatal(err)
		}
		if !dc.StrictHostKey || dc.OnLearnHostKey != nil {
			t.Fatalf("%s: not strict: %+v", name, dc)
		}
	}
	before, _ := os.ReadFile(path)
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "nokey", Command: "ls"}); !errors.Is(err, ErrNoHostKey) {
		t.Fatalf("want ErrNoHostKey, got %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) || len(fe.calls) != 0 {
		t.Fatal("an AI exec on an unpinned server wrote the store or dialled")
	}
}
```

Run: `go test ./internal/sshx/ ./internal/mcpserver/ ./internal/hub/ -run 'TestPinnedMismatch|TestStrict|TestHostKeyAlgorithms|TestPinnedAlgo|TestRegistryStrict|TestRegistryClose|TestResolveNamedIsStrict|TestHubNeverLearns' -v`
Expected: FAIL to compile: `undefined: HostKeyMismatchError`, `unknown field StrictHostKey in struct literal`, `r.Close undefined`.

- [ ] **Step 3: Rewrite `hostkey.go`**

Replace the whole of `internal/sshx/hostkey.go` with:

```go
package sshx

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
)

// ErrHostKeyMismatch is wrapped by *HostKeyMismatchError.
var ErrHostKeyMismatch = errors.New("host key mismatch")

// HostKeyUnknownError is a strict dial's refusal of a host with no pin. Key
// is the key the server presented.
type HostKeyUnknownError struct {
	Fingerprint, KeyType string
	Key                  ssh.PublicKey
}

func (e *HostKeyUnknownError) Error() string {
	return fmt.Sprintf("host key not pinned: server presented %s %s", e.KeyType, e.Fingerprint)
}

// HostKeyMismatchError: the server presented a key other than the pinned one.
type HostKeyMismatchError struct {
	Pinned, Presented, KeyType string
	Key                        ssh.PublicKey
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("%v: got %s, pinned %s", ErrHostKeyMismatch, e.Presented, e.Pinned)
}

func (e *HostKeyMismatchError) Unwrap() error { return ErrHostKeyMismatch }

func Fingerprint(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}

// HostKeyCallback checks the presented key against pinned. With no pin it
// learns the key through onLearn (TOFU: --host mode and the web UI only) or,
// when onLearn is nil, refuses with *HostKeyUnknownError (strict).
func HostKeyCallback(pinned string, insecure bool, onLearn func(fp string)) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if insecure {
			return nil
		}
		fp := Fingerprint(key)
		switch {
		case pinned == "" && onLearn == nil:
			return &HostKeyUnknownError{Fingerprint: fp, KeyType: key.Type(), Key: key}
		case pinned == "":
			onLearn(fp)
		case fp != pinned:
			return &HostKeyMismatchError{Pinned: pinned, Presented: fp, KeyType: key.Type(), Key: key}
		}
		return nil
	}
}

// hostKeyAlgorithms limits negotiation to a pinned key's family, so a server
// that adds a key of another type still presents the pinned one.
func hostKeyAlgorithms(algo string) []string {
	switch algo {
	case "":
		return nil
	case ssh.KeyAlgoRSA:
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{algo}
}
```

- [ ] **Step 4: Add the fields and wire them into the dial**

In `internal/sshx/manager.go`, replace `type DialConfig` with:

```go
type DialConfig struct {
	Host, User, Password, PrivateKey, Passphrase string
	SuPassword, SudoPassword, HostKey, Auth      string
	HostKeyAlgo                                  string // the pinned key's type; limits negotiation to its family
	Port, TimeoutMs                              int
	Insecure                                     bool
	StrictHostKey                                bool // hub paths: no TOFU, an unpinned host fails with *HostKeyUnknownError
	OnLearnHostKey                               func(fp string)
}
```

In `ensure`, replace the `cc := &ssh.ClientConfig{...}` literal with:

```go
	learn := m.learn
	if m.cfg.StrictHostKey {
		learn = nil
	}
	cc := &ssh.ClientConfig{
		User:              m.cfg.User,
		Auth:              auth,
		HostKeyCallback:   HostKeyCallback(*m.pin.Load(), m.cfg.Insecure, learn),
		HostKeyAlgorithms: hostKeyAlgorithms(m.cfg.HostKeyAlgo),
		Timeout:           30 * time.Second,
	}
```

In `internal/sshx/registry.go`, replace `ConfigHash`, the `Get` comment and condition, and add `Close`, so the file reads:

```go
package sshx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

func ConfigHash(c DialConfig) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%s|%s|%s|%s|%s|%s|%s|%s|%v", c.Host, c.Port, c.User, c.Auth,
		c.Password, c.PrivateKey, c.Passphrase, c.SuPassword, c.SudoPassword, c.HostKey, c.HostKeyAlgo, c.Insecure)
	return hex.EncodeToString(h.Sum(nil))
}

type Registry struct {
	mu sync.Mutex
	m  map[string]*Manager
}

func NewRegistry() *Registry { return &Registry{m: map[string]*Manager{}} }

// Get compares against the manager's current config, pin included, so a
// manager that just learned the pin cfg now carries is kept. An empty pin in
// a non-strict cfg means the caller has none to offer (--host mode): the
// manager's learned pin still governs its redials. A strict (hub) caller's
// empty pin means "unpinned" and is never filled from the cache.
func (r *Registry) Get(name string, cfg DialConfig) *Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	if mgr, ok := r.m[name]; ok {
		cur := mgr.currentConfig()
		want := cfg
		if want.HostKey == "" && !want.StrictHostKey {
			want.HostKey = cur.HostKey
		}
		if ConfigHash(cur) == ConfigHash(want) {
			return mgr
		}
		mgr.Close()
	}
	mgr := NewManager(cfg)
	r.m[name] = mgr
	return mgr
}

// Close closes and forgets name's manager; its terminals see EOF.
func (r *Registry) Close(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if mgr, ok := r.m[name]; ok {
		mgr.Close()
		delete(r.m, name)
	}
}

func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, mgr := range r.m {
		mgr.Close()
	}
	r.m = map[string]*Manager{}
}
```

- [ ] **Step 5: Make the hub's resolve strict and drop both learners**

In `internal/mcpserver/resolve.go`, replace the tail of `Resolve` (from `dc := sshx.DialConfig{` of the file branch to the end of the function) with:

```go
	// The file branch serves only the hub: strict, with no learner, so a
	// host key is pinned only by the user's Trust in the app.
	dc := sshx.DialConfig{
		Host: s.Host, Port: s.Port, User: s.User, Password: pw, Auth: s.Auth,
		SuPassword: su, SudoPassword: sudo, Passphrase: passphrase, HostKey: s.HostKey, HostKeyAlgo: s.HostKeyAlgo,
		Insecure: d.Insecure, StrictHostKey: true, TimeoutMs: 60000,
	}
	if s.KeyPath != "" {
		data, err := os.ReadFile(expandPath(s.KeyPath))
		if err != nil {
			return sshx.DialConfig{}, fmt.Errorf("reading key file %q: %w", s.KeyPath, err)
		}
		dc.PrivateKey = string(data)
	}
	return dc, nil
}
```

In `internal/hub/hub.go`, remove `"bytes"` from the import block, then replace the comment above `Resolve` and the whole `resolveLocked` function with:

```go
// Resolve turns a stored server into a strict DialConfig (no learner: see
// Deps.Resolve). It runs under h.mu because it reads File and MasterKey; it
// is short (decrypt plus an optional key-file read).
func (h *Hub) Resolve(name string) (sshx.DialConfig, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.resolveLocked(name)
}
```

and

```go
func (h *Hub) resolveLocked(name string) (sshx.DialConfig, error) {
	return h.deps.Resolve(name)
}
```

(Keep `resolveForTerm` between them unchanged.)

In `internal/hub/term_test.go` `fakeServerHub`, pin the fake server's key, since hub dials are now strict:

```go
	f := &config.File{Version: 1, Servers: []config.Server{
		{Name: "fk", Host: srv.Host, Port: srv.Port, User: "u", Auth: "key", KeyPath: keyPath, HostKey: srv.Fingerprint()},
	}}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/sshx/ ./internal/mcpserver/ ./internal/hub/ -v -run 'TestPinnedMismatch|TestStrict|TestHostKeyAlgorithms|TestPinnedAlgo|TestRegistry|TestRedial|TestTOFU|TestResolve|TestHubNeverLearns|TestTerm'`
Expected: PASS.

Run: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...`
Expected: all `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/sshx internal/mcpserver internal/hub
git commit -m "$(cat <<'EOF'
feat(sshx): strict host keys with typed errors, algorithm pinning, Registry.Close

Hub dials are strict: an unpinned host fails with HostKeyUnknownError and a
different key with HostKeyMismatchError, and no hub path carries a learner.
A pinned key type limits HostKeyAlgorithms to its family. --host mode and
the web UI keep TOFU.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
)"
```

---

### Task 3: `vault.create` and `status.hasVault`/`storePath`

**Files:**
- Create: `internal/hub/hosts.go`, `internal/hub/hosts_test.go`
- Modify: `internal/hub/hub.go:240-264` (split `Reload`), `internal/hub/uidoor.go` (`hasVault`, `status`, `vault.create`), `internal/hub/mcpdoor_test.go:48` (probe list)

**Interfaces:**
- Consumes: `config.Update` (Task 1), `config.NewKDF`.
- Produces:
  - `func (h *Hub) CreateVault(pw string) error`
  - `func (h *Hub) reloadLocked() error` (h.mu held)
  - `func (h *Hub) hasVault() bool`
  - UI door: `vault.create {password}` → `{}`; `status` → `{locked, hasStore, hasVault, storePath, pending}`
  - Test helper `newHubAt(t, f *config.File, mk []byte) (*Hub, string)`: writes `f` (unless nil), audit log next to the store, `IdleLock: -1`

- [ ] **Step 1: Write the failing tests**

Create `internal/hub/hosts_test.go`:

```go
package hub

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/rpc"
)

// newHubAt writes f (unless nil) and starts a hub on it with an audit log
// next to the store and no idle lock.
func newHubAt(t *testing.T, f *config.File, mk []byte) (*Hub, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	if f != nil {
		if err := config.Save(path, f, mk); err != nil {
			t.Fatal(err)
		}
	}
	audit, err := broker.OpenAudit(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { audit.Close() })
	h, err := New(Options{StorePath: path, Audit: audit, IdleLock: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	t.Cleanup(h.Registry().CloseAll)
	return h, path
}

func TestVaultCreateOnEmptyStore(t *testing.T) {
	h, path := newHubAt(t, nil, nil)
	c, _ := startUI(t, h)
	ctx := context.Background()
	var st struct {
		Locked    bool   `json:"locked"`
		HasVault  bool   `json:"hasVault"`
		StorePath string `json:"storePath"`
	}
	if err := c.Call(ctx, "status", nil, &st); err != nil || st.HasVault || st.StorePath != path {
		t.Fatalf("status before: %v %+v", err, st)
	}
	var re *rpc.Error
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "short"}, nil); !errors.As(err, &re) || re.Code != -32602 {
		t.Fatalf("short password: want -32602, got %v", err)
	}
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	// Unlocked by the create itself: no unlock call, no second Argon2 run.
	if err := c.Call(ctx, "status", nil, &st); err != nil || !st.HasVault || st.Locked {
		t.Fatalf("status after: %v %+v", err, st)
	}
	f, err := config.Load(path)
	if err != nil || f.KDF == nil {
		t.Fatalf("no vault on disk: %v %+v", err, f)
	}
	h.mu.Lock()
	mk := bytes.Clone(h.deps.MasterKey)
	h.mu.Unlock()
	if !f.KDF.Verify(mk) || f.VerifyMAC(mk) != nil {
		t.Fatal("the hub's key does not open the new vault")
	}
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second create: %v", err)
	}
}

// A KDF-less file was never MAC'd: its servers are kept, their AI flags are not.
func TestVaultCreateKeepsServersButNotAIVisible(t *testing.T) {
	h, path := newHubAt(t, &config.File{Version: 1, Servers: []config.Server{
		{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true, HostKey: "SHA256:abc"}}}, nil)
	if err := h.CreateVault("longenough"); err != nil {
		t.Fatal(err)
	}
	if got := h.ServersForMCP(); len(got) != 0 {
		t.Fatalf("AI still sees %v", got)
	}
	f, _ := config.Load(path)
	if len(f.Servers) != 1 || f.Servers[0].Name != "a" || f.Servers[0].AIVisible || f.Servers[0].HostKey != "SHA256:abc" {
		t.Fatalf("kept servers: %+v", f.Servers)
	}
}

func TestVaultCreateRefusesEncryptedFieldsWithoutKDF(t *testing.T) {
	h, path := newHubAt(t, &config.File{Version: 1, Servers: []config.Server{
		{Name: "a", Host: "h", Port: 22, User: "u", Auth: "password", EncPassword: "bogus"}}}, nil)
	before, _ := os.ReadFile(path)
	if err := h.CreateVault("longenough"); err == nil || !strings.Contains(err.Error(), "encrypted fields") {
		t.Fatalf("got %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) || h.deps.MasterKey != nil {
		t.Fatal("a refused create changed the file or the hub")
	}
}
```

In `internal/hub/mcpdoor_test.go` `TestMCPDoorRejectsUIOnlyMethods`, change the probe list to:

```go
	for _, m := range []string{"unlock", "lock", "decide", "denyAll", "pending", "term.open", "servers", "vault.create"} {
```

Run: `go test ./internal/hub/ -run 'TestVaultCreate|TestMCPDoorRejectsUIOnlyMethods' -v`
Expected: FAIL to compile: `h.CreateVault undefined`.

- [ ] **Step 2: Split `Reload`**

In `internal/hub/hub.go`, replace `Reload` with:

```go
// Reload re-reads the store when its on-disk Revision differs. The master
// key is kept: the KDF params do not change on an ordinary save. If the MAC
// no longer verifies (e.g. the master password changed elsewhere), the old
// file stays in effect and the error is returned.
func (h *Hub) Reload() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reloadLocked()
}

func (h *Hub) reloadLocked() error {
	f, err := config.Load(h.o.StorePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if h.deps.File != nil && f.Revision == h.deps.File.Revision {
		return nil
	}
	if h.deps.MasterKey != nil {
		if err := f.VerifyMAC(h.deps.MasterKey); err != nil {
			return err
		}
	}
	h.deps.File = f
	return nil
}
```

- [ ] **Step 3: Write `CreateVault`**

Create `internal/hub/hosts.go`:

```go
package hub

import (
	"errors"
	"time"

	"github.com/lang315/ssh-mcp/internal/config"
)

// CreateVault gives a store with no master password (or no file yet) one.
// Kept servers lose aiVisible: a KDF-less file was never MAC'd, so its flags
// are unauthenticated. The key is derived once, here, and installed in the
// same h.mu section that saves and reloads, so nothing sees a vault that
// exists but is locked. Lock order h.mu → config.Update is safe: no path
// takes h.mu from inside an Update.
func (h *Hub) CreateVault(pw string) error {
	// A fresh load, not deps.File. It must come first: Update checks the key
	// before fn runs, so an existing vault would only fail as "wrong master
	// key" (and a race still fails that way, closed).
	if f, err := config.Load(h.o.StorePath); err == nil && f.KDF != nil {
		return errors.New("a vault already exists")
	}
	k, mk, err := config.NewKDF(pw)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	err = config.Update(h.o.StorePath, mk, func(f *config.File) error {
		for i := range f.Servers {
			s := &f.Servers[i]
			if s.EncPassword != "" || s.EncSuPassword != "" || s.EncSudoPassword != "" || s.EncKeyPassphrase != "" {
				return errors.New("the store has encrypted fields but no master password; it is corrupt or was tampered with")
			}
			s.AIVisible = false
		}
		f.KDF = &k
		return nil
	})
	if err != nil {
		clear(mk)
		return err
	}
	clear(h.deps.MasterKey)
	h.deps.MasterKey = mk
	h.lastActivity = time.Now()
	return h.reloadLocked()
}
```

- [ ] **Step 4: Wire `status` and `vault.create` into the UI door**

In `internal/hub/uidoor.go`, add below `hasStore`:

```go
// hasVault reports whether the store has a master password (a KDF), under h.mu.
func (h *Hub) hasVault() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.deps.File != nil && h.deps.File.KDF != nil
}
```

Replace the `status` handler's return line with:

```go
		return map[string]any{"locked": h.Locked(), "hasStore": h.hasStore(), "hasVault": h.hasVault(),
			"storePath": h.o.StorePath, "pending": len(h.Broker().Pending())}, nil
```

Add after the `lock` handler:

```go
	req("vault.create", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Password string `json:"password"`
		}
		json.Unmarshal(raw, &p)
		if len(p.Password) < 8 {
			return nil, &rpc.Error{Code: -32602, Message: "password must be at least 8 characters"}
		}
		return empty, h.CreateVault(p.Password)
	})
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/hub/ -run 'TestVaultCreate|TestMCPDoorRejectsUIOnlyMethods|TestUIDoor|TestReload' -v`
Expected: PASS.

Run: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...`
Expected: all `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/hub
git commit -m "$(cat <<'EOF'
feat(hub): vault.create and status hasVault/storePath

The vault is created, reloaded, and unlocked in one h.mu section with the
key derived once. Servers from a KDF-less file are kept with aiVisible off;
a KDF-less file carrying ciphertexts is refused as tampered.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
)"
```

---

### Task 4: `servers.save`, `servers.delete`, `servers.forgetHostKey`, richer `servers`

**Files:**
- Modify: `internal/hub/hosts.go` (whole file), `internal/hub/uidoor.go` (`uiServer`, `serversForUI`, three methods, `config` import), `internal/hub/mcpdoor_test.go:48`
- Create: `internal/hub/servers_test.go`

**Interfaces:**
- Consumes: `config.ServerInput`, `config.ApplyServer`, `config.Update` (Task 1); `Registry.Close` (Task 2); `CreateVault`, `reloadLocked` (Task 3); existing `serverNotFound`, `ErrLocked`, `Broker.Pending`, `Broker.Decide`.
- Produces:
  - `var errNoVault = errors.New("create a vault first")`
  - `func (h *Hub) writeKey() ([]byte, error)` (a copy; `ErrLocked` / `errNoVault`)
  - `func (h *Hub) denyPending(name string)`; `func dialChanged(a, b config.Server) bool`
  - `func (h *Hub) SaveServer(original string, in config.ServerInput) error`
  - `func (h *Hub) DeleteServer(name string) error`; `func (h *Hub) ForgetHostKey(name string) error`
  - `uiServer` gains `KeyPath`, `HostKeyAlgo`, `HasPassword`, `HasSuPassword`, `HasSudoPassword`, `HasKeyPassphrase` (JSON `keyPath hostKeyAlgo hasPassword hasSuPassword hasSudoPassword hasKeyPassphrase`)
  - UI door: `servers.save {original?, server}`, `servers.delete {name}`, `servers.forgetHostKey {name}` → `{}`
  - Test helpers (used again in Tasks 5 and 7): `waitNote(t, notes, method, id)`, `noNote(t, notes, method, id, d)`, `inputFor(t, h, name) config.ServerInput`, `uiServerNamed(t, c, name) (uiServer, bool)`, `save(c, original, in) error`

The next `term.open` after a forget or delete answering `hostKeyUnknown` needs Task 5's result format, so it is tested there. This task tests that the connection closes.

- [ ] **Step 1: Write the failing tests**

Create `internal/hub/servers_test.go`:

```go
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/rpc"
)

// waitNote waits for notification method for terminal id, skipping others.
func waitNote(t *testing.T, notes chan note, method, id string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case n := <-notes:
			var p struct {
				ID string `json:"id"`
			}
			json.Unmarshal(n.params, &p)
			if n.method == method && p.ID == id {
				return
			}
		case <-deadline:
			t.Fatalf("no %s for %s", method, id)
		}
	}
}

// noNote fails if notification method arrives for id within d.
func noNote(t *testing.T, notes chan note, method, id string, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case n := <-notes:
			var p struct {
				ID string `json:"id"`
			}
			json.Unmarshal(n.params, &p)
			if n.method == method && p.ID == id {
				t.Fatalf("unexpected %s for %s: %s", method, id, n.params)
			}
		case <-deadline:
			return
		}
	}
}

// inputFor returns server name as a ServerInput with every secret nil (kept).
func inputFor(t *testing.T, h *Hub, name string) config.ServerInput {
	t.Helper()
	for _, s := range h.serversForUI() {
		if s.Name == name {
			return config.ServerInput{Name: s.Name, Host: s.Host, Port: s.Port, User: s.User, Auth: s.Auth, KeyPath: s.KeyPath, AIVisible: s.AIVisible}
		}
	}
	t.Fatalf("no server %q", name)
	return config.ServerInput{}
}

func uiServerNamed(t *testing.T, c *rpc.Client, name string) (uiServer, bool) {
	t.Helper()
	var list []uiServer
	if err := c.Call(context.Background(), "servers", nil, &list); err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.Name == name {
			return s, true
		}
	}
	return uiServer{}, false
}

func save(c *rpc.Client, original string, in config.ServerInput) error {
	return c.Call(context.Background(), "servers.save", map[string]any{"original": original, "server": in}, nil)
}

func TestServersSaveValidation(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _ := startUI(t, h)
	ok := config.ServerInput{Name: "n", Host: "h", Port: 22, User: "u", Auth: "agent"}
	for _, tc := range []struct {
		edit func(*config.ServerInput)
		want string
	}{
		{func(s *config.ServerInput) { s.Name = "bad name" }, "name:"},
		{func(s *config.ServerInput) { s.Host = "" }, "host:"},
		{func(s *config.ServerInput) { s.Port = 70000 }, "port:"},
		{func(s *config.ServerInput) { s.User = "a b" }, "user:"},
		{func(s *config.ServerInput) { s.Auth = "x" }, "auth:"},
		{func(s *config.ServerInput) { s.Auth = "key" }, "keyPath:"},
	} {
		in := ok
		tc.edit(&in)
		var re *rpc.Error
		if err := save(c, "", in); !errors.As(err, &re) || re.Code != -32602 || !strings.HasPrefix(re.Message, tc.want) {
			t.Errorf("%s: got %v", tc.want, err)
		}
	}
}

func TestServersWritesNeedUnlockedVault(t *testing.T) {
	locked, _, _ := newEncHub(t, &fakeExec{})
	noVault, _ := newHub(t, &fakeExec{})
	in := config.ServerInput{Name: "x", Host: "h", Port: 22, User: "u", Auth: "agent"}
	for h, want := range map[*Hub]error{locked: ErrLocked, noVault: errNoVault} {
		if err := h.SaveServer("", in); !errors.Is(err, want) {
			t.Errorf("save: want %v, got %v", want, err)
		}
		if err := h.DeleteServer("vis"); !errors.Is(err, want) {
			t.Errorf("delete: want %v, got %v", want, err)
		}
		if err := h.ForgetHostKey("vis"); !errors.Is(err, want) {
			t.Errorf("forget: want %v, got %v", want, err)
		}
	}
}

func TestServersSaveSecretSemanticsAndRename(t *testing.T) {
	h, _, _ := newEncHub(t, &fakeExec{})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	c, _ := startUI(t, h)
	p := func(s string) *string { return &s }
	secrets := func(name string) [4]string {
		t.Helper()
		dc, err := h.Resolve(name)
		if err != nil {
			t.Fatal(err)
		}
		return [4]string{dc.Password, dc.SuPassword, dc.SudoPassword, dc.Passphrase}
	}
	base := config.ServerInput{Name: "n1", Host: "h", Port: 22, User: "u", Auth: "password"}
	in := base
	in.Password, in.SuPassword, in.SudoPassword, in.KeyPassphrase = p("p1"), p("s1"), p("d1"), p("k1")
	if err := save(c, "", in); err != nil {
		t.Fatal(err)
	}
	if got := secrets("n1"); got != [4]string{"p1", "s1", "d1", "k1"} {
		t.Fatalf("create: %v", got)
	}
	if err := save(c, "n1", base); err != nil { // all nil: kept
		t.Fatal(err)
	}
	if got := secrets("n1"); got != [4]string{"p1", "s1", "d1", "k1"} {
		t.Fatalf("nil must keep: %v", got)
	}
	in.Password, in.SuPassword, in.SudoPassword, in.KeyPassphrase = p(""), p("s2"), p(""), p("k2")
	if err := save(c, "n1", in); err != nil {
		t.Fatal(err)
	}
	if got := secrets("n1"); got != [4]string{"", "s2", "", "k2"} {
		t.Fatalf("clear/set: %v", got)
	}
	s, _ := uiServerNamed(t, c, "n1")
	if s.HasPassword || !s.HasSuPassword || s.HasSudoPassword || !s.HasKeyPassphrase {
		t.Fatalf("has* flags: %+v", s)
	}
	renamed := base
	renamed.Name = "n2"
	if err := save(c, "n1", renamed); err != nil {
		t.Fatal(err)
	}
	if got := secrets("n2"); got != [4]string{"", "s2", "", "k2"} {
		t.Fatalf("rename must re-encrypt: %v", got)
	}
	if _, ok := uiServerNamed(t, c, "n1"); ok {
		t.Fatal("old name still listed")
	}
}

func TestServersSaveNewPortDropsPinAndUnsuppliedSecrets(t *testing.T) {
	h, _, _ := newEncHub(t, &fakeExec{})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	c, _ := startUI(t, h)
	in := inputFor(t, h, "enc")
	su := "su-new"
	in.Port, in.SuPassword = 2222, &su
	if err := save(c, "enc", in); err != nil {
		t.Fatal(err)
	}
	s, _ := uiServerNamed(t, c, "enc")
	if s.HostKey != "" || s.HostKeyAlgo != "" || s.HasPassword || !s.HasSuPassword {
		t.Fatalf("got %+v", s)
	}
}

func TestServersSaveClosesTabsOnlyOnDialChange(t *testing.T) {
	h := fakeServerHub(t, "pw")
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	c, _, notes := startTermDoor(t, h)
	if err := c.Call(context.Background(), "term.open", map[string]any{"id": "t1", "server": "fk", "rows": 24, "cols": 80}, nil); err != nil {
		t.Fatal(err)
	}
	in := inputFor(t, h, "fk")
	in.AIVisible = true
	if err := save(c, "fk", in); err != nil {
		t.Fatal(err)
	}
	noNote(t, notes, "term.exit", "t1", 300*time.Millisecond)
	in.User = "u2"
	if err := save(c, "fk", in); err != nil {
		t.Fatal(err)
	}
	waitNote(t, notes, "term.exit", "t1")
}

func TestServersForgetAndDeleteCloseTheConnection(t *testing.T) {
	for _, method := range []string{"servers.forgetHostKey", "servers.delete"} {
		h := fakeServerHub(t, "pw")
		if err := h.Unlock("pw"); err != nil {
			t.Fatal(err)
		}
		c, _, notes := startTermDoor(t, h)
		ctx := context.Background()
		if err := c.Call(ctx, "term.open", map[string]any{"id": "t1", "server": "fk", "rows": 24, "cols": 80}, nil); err != nil {
			t.Fatal(err)
		}
		if err := c.Call(ctx, method, map[string]string{"name": "fk"}, nil); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		waitNote(t, notes, "term.exit", "t1")
		s, listed := uiServerNamed(t, c, "fk")
		switch method {
		case "servers.forgetHostKey":
			if !listed || s.HostKey != "" {
				t.Fatalf("forget: %v %+v", listed, s)
			}
			if err := c.Call(ctx, method, map[string]string{"name": "fk"}, nil); err != nil {
				t.Fatalf("forgetting an unpinned server must succeed: %v", err)
			}
		case "servers.delete":
			if listed {
				t.Fatal("deleted server still listed")
			}
			if err := c.Call(ctx, method, map[string]string{"name": "fk"}, nil); err == nil || !strings.Contains(err.Error(), "not found") {
				t.Fatalf("second delete: %v", err)
			}
		}
	}
}

func TestServersSaveDeniesPendingRequests(t *testing.T) {
	h, _ := newEncryptedHub(t, Options{IdleLock: -1, ApprovalExpiry: time.Minute})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); errc <- err }()
	waitPending(t, h.Broker(), 1)
	if err := h.SaveServer("vis", inputFor(t, h, "vis")); err != nil {
		t.Fatal(err)
	}
	var de *DeniedError
	if err := <-errc; !errors.As(err, &de) || de.Reason != "server changed" {
		t.Fatalf("got %v", err)
	}
}
```

In `internal/hub/mcpdoor_test.go`, extend the probe list:

```go
	for _, m := range []string{"unlock", "lock", "decide", "denyAll", "pending", "term.open", "servers",
		"vault.create", "servers.save", "servers.delete", "servers.forgetHostKey"} {
```

Run: `go test ./internal/hub/ -run 'TestServers|TestMCPDoorRejectsUIOnlyMethods' -v`
Expected: FAIL to compile: `h.SaveServer undefined`, `s.KeyPath undefined (type uiServer has no field or method KeyPath)`.

- [ ] **Step 2: Write the hub methods**

Replace the whole of `internal/hub/hosts.go` with:

```go
package hub

import (
	"bytes"
	"errors"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
)

// errNoVault: every write from the app needs a vault, the only thing that
// gives a key to encrypt with and to MAC under.
var errNoVault = errors.New("create a vault first")

// CreateVault gives a store with no master password (or no file yet) one.
// Kept servers lose aiVisible: a KDF-less file was never MAC'd, so its flags
// are unauthenticated. The key is derived once, here, and installed in the
// same h.mu section that saves and reloads, so nothing sees a vault that
// exists but is locked. Lock order h.mu → config.Update is safe: no path
// takes h.mu from inside an Update.
func (h *Hub) CreateVault(pw string) error {
	// A fresh load, not deps.File. It must come first: Update checks the key
	// before fn runs, so an existing vault would only fail as "wrong master
	// key" (and a race still fails that way, closed).
	if f, err := config.Load(h.o.StorePath); err == nil && f.KDF != nil {
		return errors.New("a vault already exists")
	}
	k, mk, err := config.NewKDF(pw)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	err = config.Update(h.o.StorePath, mk, func(f *config.File) error {
		for i := range f.Servers {
			s := &f.Servers[i]
			if s.EncPassword != "" || s.EncSuPassword != "" || s.EncSudoPassword != "" || s.EncKeyPassphrase != "" {
				return errors.New("the store has encrypted fields but no master password; it is corrupt or was tampered with")
			}
			s.AIVisible = false
		}
		f.KDF = &k
		return nil
	})
	if err != nil {
		clear(mk)
		return err
	}
	clear(h.deps.MasterKey)
	h.deps.MasterKey = mk
	h.lastActivity = time.Now()
	return h.reloadLocked()
}

// writeKey returns a copy of the master key for a store write.
func (h *Hub) writeKey() ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.deps.MasterKey != nil:
		return bytes.Clone(h.deps.MasterKey), nil
	case h.deps.File != nil && h.deps.File.KDF != nil:
		return nil, ErrLocked
	}
	return nil, errNoVault
}

// denyPending denies name's pending AI requests: each was for the server as
// it was when submitted.
func (h *Hub) denyPending(name string) {
	for _, r := range h.broker.Pending() {
		if r.Server == name {
			_ = h.broker.Decide(r.ID, broker.Decision{Outcome: broker.Denied, Reason: "server changed"})
		}
	}
}

// dialChanged: every field but AIVisible feeds the dial config or the name
// the connection is registered under.
func dialChanged(a, b config.Server) bool {
	a.AIVisible = b.AIVisible
	return a != b
}

// SaveServer creates (original == "") or updates a server. Its connection is
// closed only if something that feeds the dial changed, never for an
// aiVisible toggle; its pending AI requests are denied either way.
func (h *Hub) SaveServer(original string, in config.ServerInput) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	var before, after config.Server
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		var err error
		before, after, err = config.ApplyServer(f, original, in, key)
		return err
	})
	if err != nil {
		return err
	}
	_ = h.Reload()
	name := original
	if name == "" {
		name = in.Name
	}
	if dialChanged(before, after) {
		h.reg.Close(name)
	}
	h.denyPending(name)
	return nil
}

func (h *Hub) DeleteServer(name string) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		for i, s := range f.Servers {
			if s.Name == name {
				f.Servers = append(f.Servers[:i], f.Servers[i+1:]...)
				return nil
			}
		}
		return serverNotFound(name)
	})
	if err != nil {
		return err
	}
	_ = h.Reload()
	h.reg.Close(name)
	h.denyPending(name)
	return nil
}

// ForgetHostKey clears a server's pin and closes its connection, so the next
// open asks the user again. An unpinned server is a no-op success.
func (h *Hub) ForgetHostKey(name string) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == name {
				f.Servers[i].HostKey, f.Servers[i].HostKeyAlgo = "", ""
				return nil
			}
		}
		return serverNotFound(name)
	})
	if err != nil {
		return err
	}
	_ = h.Reload()
	h.reg.Close(name)
	return nil
}
```

- [ ] **Step 3: Extend the UI door**

In `internal/hub/uidoor.go`, add `"github.com/lang315/ssh-mcp/internal/config"` to the imports, then replace `uiServer` and `serversForUI` with:

```go
// uiServer is the UI door's server listing shape: every stored server
// (visible or hidden), with connection metadata but never a secret field;
// has* only says whether one is stored.
type uiServer struct {
	Name             string `json:"name"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	User             string `json:"user"`
	Auth             string `json:"auth"`
	KeyPath          string `json:"keyPath"`
	HostKey          string `json:"hostKey"`
	HostKeyAlgo      string `json:"hostKeyAlgo"`
	AIVisible        bool   `json:"aiVisible"`
	Locked           bool   `json:"locked"`
	HasPassword      bool   `json:"hasPassword"`
	HasSuPassword    bool   `json:"hasSuPassword"`
	HasSudoPassword  bool   `json:"hasSudoPassword"`
	HasKeyPassphrase bool   `json:"hasKeyPassphrase"`
}
```

```go
// serversForUI lists every stored server, unlike ServersForMCP which hides
// non-AIVisible ones. No secret field is ever included.
func (h *Hub) serversForUI() []uiServer {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []uiServer{}
	if h.deps.File == nil {
		return out
	}
	for _, s := range h.deps.File.Servers {
		out = append(out, uiServer{
			Name: s.Name, Host: s.Host, Port: s.Port, User: s.User, Auth: s.Auth, KeyPath: s.KeyPath,
			HostKey: s.HostKey, HostKeyAlgo: s.HostKeyAlgo, AIVisible: s.AIVisible, Locked: h.deps.IsLocked(s.Name),
			HasPassword: s.EncPassword != "", HasSuPassword: s.EncSuPassword != "",
			HasSudoPassword: s.EncSudoPassword != "", HasKeyPassphrase: s.EncKeyPassphrase != "",
		})
	}
	return out
}
```

Add after the `vault.create` handler:

```go
	req("servers.save", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Original string             `json:"original"`
			Server   config.ServerInput `json:"server"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
		}
		if err := p.Server.Validate(); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: err.Error()}
		}
		return empty, h.SaveServer(p.Original, p.Server)
	})
	req("servers.delete", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		json.Unmarshal(raw, &p)
		return empty, h.DeleteServer(p.Name)
	})
	req("servers.forgetHostKey", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		json.Unmarshal(raw, &p)
		return empty, h.ForgetHostKey(p.Name)
	})
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/hub/ -run 'TestServers|TestMCPDoorRejectsUIOnlyMethods|TestUIDoor|TestTerm|TestVaultCreate' -v`
Expected: PASS.

Run: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...`
Expected: all `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/hub
git commit -m "$(cat <<'EOF'
feat(hub): servers.save, servers.delete, servers.forgetHostKey

Writes need an unlocked vault and go through config.Update. A save closes
the server's connection only when the dial config changed, never for an
aiVisible toggle, and denies its pending AI requests ("server changed").
servers lists keyPath, hostKeyAlgo, and has* flags, never a secret.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
)"
```

---

### Task 5: `term.open` host-key results, trusted retry, `known_hosts` hint

**Files:**
- Create: `internal/sshx/knownhosts.go`, `internal/sshx/knownhosts_test.go`, `internal/hub/trust_test.go`
- Modify: `internal/hub/term.go` (imports, `term.open` handler, three new funcs), `internal/hub/hosts.go` (imports, `recordTrust`), `internal/hub/hub.go:53-60` (`Options.KnownHostsPath`), `internal/hub/term_test.go:122-131` (validation table)

**Interfaces:**
- Consumes: `HostKeyUnknownError`, `HostKeyMismatchError`, `DialConfig.HostKeyAlgo`, `Registry.Close` (Task 2); `config.RecordHostKey`, `config.ErrPinSkipped` (Task 1); `writeKey` (Task 4); test helpers `newHubAt` (Task 3), `waitNote`, `noNote`, `save` (Task 4), `startTermDoor`, `note` (existing).
- Produces:
  - `func KnownHostsHint(file, host string, port int, key ssh.PublicKey) string` → `"match" | "different" | "absent"`
  - `Options.KnownHostsPath string`
  - `func (h *Hub) recordTrust(name string, dc sshx.DialConfig) error`
  - `term.open` param `trustHostKey: {fingerprint, keyType}`; result `{status: "open", id}` | `{status: "hostKeyUnknown", server, host, port, user, fingerprint, keyType, knownHosts}` | `{status: "hostKeyMismatch", server, host, port, user, pinned, presented}`
  - Test helpers (used again in Task 7): `type openResult`, `trustHub(t) (*Hub, *sshtest.Server, *rpc.Client, chan note)`, `openTerm(t, c, id string, trust map[string]string) openResult` (server `"box"`)

From here until Task 10 the renderer treats a non-`open` result as success (Plan decision 19). Leave it.

- [ ] **Step 1: Write the failing `KnownHostsHint` test**

Create `internal/sshx/knownhosts_test.go`:

```go
package sshx

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestKnownHostsHint(t *testing.T) {
	k1, k2 := testKey(t), testKey(t)
	file := filepath.Join(t.TempDir(), "known_hosts")
	lines := knownhosts.Line([]string{"plain.example:2222"}, k1) + "\n" +
		knownhosts.Line([]string{knownhosts.HashHostname("hashed.example")}, k1) + "\n"
	if err := os.WriteFile(file, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		host string
		port int
		key  ssh.PublicKey
		want string
	}{
		{"plain.example", 2222, k1, "match"},
		{"plain.example", 2222, k2, "different"},
		{"plain.example", 22, k1, "absent"},
		{"hashed.example", 22, k1, "match"},
		{"hashed.example", 22, k2, "different"},
		{"other.example", 22, k1, "absent"},
	} {
		if got := KnownHostsHint(file, c.host, c.port, c.key); got != c.want {
			t.Errorf("%s:%d: got %q, want %q", c.host, c.port, got, c.want)
		}
	}
	if got := KnownHostsHint(filepath.Join(t.TempDir(), "missing"), "plain.example", 2222, k1); got != "absent" {
		t.Errorf("missing file: got %q", got)
	}
}
```

Run: `go test ./internal/sshx/ -run TestKnownHostsHint -v`
Expected: FAIL to compile: `undefined: KnownHostsHint`.

- [ ] **Step 2: Write `KnownHostsHint`**

Create `internal/sshx/knownhosts.go`:

```go
package sshx

import (
	"errors"
	"net"
	"strconv"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// KnownHostsHint says whether an OpenSSH known_hosts file already trusts key
// for host:port: "match", "different" (it lists another key, or revoked this
// one), or "absent" (no entry, or no readable file). It is a hint for the
// human only: the file is outside the vault's MAC and is never pinned from.
func KnownHostsHint(file, host string, port int, key ssh.PublicKey) string {
	cb, err := knownhosts.New(file)
	if err != nil {
		return "absent"
	}
	var ke *knownhosts.KeyError
	var re *knownhosts.RevokedError
	switch err := cb(net.JoinHostPort(host, strconv.Itoa(port)), &net.TCPAddr{IP: net.IPv4zero, Port: port}, key); {
	case err == nil:
		return "match"
	case errors.As(err, &ke) && len(ke.Want) > 0, errors.As(err, &re):
		return "different"
	}
	return "absent"
}
```

Run: `go test ./internal/sshx/ -run TestKnownHostsHint -v`
Expected: PASS.

- [ ] **Step 3: Write the failing trust-flow tests**

Create `internal/hub/trust_test.go`:

```go
package hub

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/lang315/ssh-mcp/internal/sshx/sshtest"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type openResult struct {
	Status      string `json:"status"`
	ID          string `json:"id"`
	Server      string `json:"server"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Fingerprint string `json:"fingerprint"`
	KeyType     string `json:"keyType"`
	KnownHosts  string `json:"knownHosts"`
	Pinned      string `json:"pinned"`
	Presented   string `json:"presented"`
}

// trustHub is an unlocked vault with one unpinned password server "box" on
// an in-process sshd, a UI door, and an empty temp known_hosts.
func trustHub(t *testing.T) (*Hub, *sshtest.Server, *rpc.Client, chan note) {
	t.Helper()
	srv := sshtest.Start(t)
	k, mk, err := config.NewKDF("pw")
	if err != nil {
		t.Fatal(err)
	}
	h, _ := newHubAt(t, &config.File{Version: 1, KDF: &k, Servers: []config.Server{
		{Name: "box", Host: srv.Host, Port: srv.Port, User: "u", Auth: "password"}}}, mk)
	h.o.KnownHostsPath = filepath.Join(t.TempDir(), "known_hosts")
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	c, _, notes := startTermDoor(t, h)
	return h, srv, c, notes
}

func openTerm(t *testing.T, c *rpc.Client, id string, trust map[string]string) openResult {
	t.Helper()
	p := map[string]any{"id": id, "server": "box", "rows": 24, "cols": 80}
	if trust != nil {
		p["trustHostKey"] = trust
	}
	var r openResult
	if err := c.Call(context.Background(), "term.open", p, &r); err != nil {
		t.Fatalf("term.open %s: %v", id, err)
	}
	return r
}

func trusting(r openResult) map[string]string {
	return map[string]string{"fingerprint": r.Fingerprint, "keyType": r.KeyType}
}

func TestTrustPinsExactlyTheConfirmedKey(t *testing.T) {
	h, srv, c, notes := trustHub(t)
	before, _ := os.ReadFile(h.o.StorePath)
	r := openTerm(t, c, "t1", nil)
	if r.Status != "hostKeyUnknown" || r.Fingerprint != srv.Fingerprint() || r.KeyType != "ssh-ed25519" ||
		r.Server != "box" || r.Host != srv.Host || r.Port != srv.Port || r.User != "u" || r.KnownHosts != "absent" {
		t.Fatalf("first open: %+v", r)
	}
	if after, _ := os.ReadFile(h.o.StorePath); !bytes.Equal(before, after) {
		t.Fatal("an unconfirmed key was recorded")
	}

	// A wrong fingerprint asks again with the real one and records nothing.
	w := openTerm(t, c, "t1", map[string]string{"fingerprint": "SHA256:wrong", "keyType": "ssh-ed25519"})
	if w.Status != "hostKeyUnknown" || w.Fingerprint != srv.Fingerprint() {
		t.Fatalf("wrong trust: %+v", w)
	}
	if after, _ := os.ReadFile(h.o.StorePath); !bytes.Equal(before, after) {
		t.Fatal("a wrong trust was recorded")
	}

	// The right fingerprint, same id: opens and pins key and algorithm.
	if o := openTerm(t, c, "t1", trusting(r)); o.Status != "open" || o.ID != "t1" {
		t.Fatalf("trusted open: %+v", o)
	}
	f, _ := config.Load(h.o.StorePath)
	if s, _ := f.FindServer("box"); s.HostKey != srv.Fingerprint() || s.HostKeyAlgo != "ssh-ed25519" {
		t.Fatalf("pin: %+v", s)
	}
	// A second tab reuses the trusted connection; the first stays open.
	if o := openTerm(t, c, "t2", nil); o.Status != "open" {
		t.Fatalf("second tab: %+v", o)
	}
	noNote(t, notes, "term.exit", "t1", 300*time.Millisecond)
}

func TestRotatedKeyIsAMismatch(t *testing.T) {
	h, srv, c, _ := trustHub(t)
	openTerm(t, c, "t1", trusting(openTerm(t, c, "t1", nil)))
	old := srv.Fingerprint()
	srv.RotateHostKey(t)
	h.Registry().Close("box") // drop the live connection so the next open redials
	r := openTerm(t, c, "t2", nil)
	if r.Status != "hostKeyMismatch" || r.Pinned != old || r.Presented != srv.Fingerprint() ||
		r.Host != srv.Host || r.Port != srv.Port || r.User != "u" {
		t.Fatalf("got %+v", r)
	}
}

func TestForgetOrDeleteThenOpenPromptsAgain(t *testing.T) {
	for _, method := range []string{"servers.forgetHostKey", "servers.delete"} {
		_, srv, c, notes := trustHub(t)
		openTerm(t, c, "t1", trusting(openTerm(t, c, "t1", nil)))
		if err := c.Call(context.Background(), method, map[string]string{"name": "box"}, nil); err != nil {
			t.Fatal(err)
		}
		waitNote(t, notes, "term.exit", "t1")
		if method == "servers.delete" { // same endpoint, added again: its pin went with it
			if err := save(c, "", config.ServerInput{Name: "box", Host: srv.Host, Port: srv.Port, User: "u", Auth: "password"}); err != nil {
				t.Fatal(err)
			}
		}
		if r := openTerm(t, c, "t2", nil); r.Status != "hostKeyUnknown" || r.Fingerprint != srv.Fingerprint() {
			t.Fatalf("%s: %+v", method, r)
		}
	}
}

// An edit that lands between the dial and the record must not pin the old
// endpoint's key on the new one.
func TestTrustRecordSkippedWhenEndpointMoved(t *testing.T) {
	h, srv, _, _ := trustHub(t)
	before, _ := os.ReadFile(h.o.StorePath)
	dc, err := h.Resolve("box")
	if err != nil {
		t.Fatal(err)
	}
	dc.HostKey, dc.HostKeyAlgo = srv.Fingerprint(), "ssh-ed25519"
	dc.Port++ // the dial ran against the old port
	if err := h.recordTrust("box", dc); !errors.Is(err, config.ErrPinSkipped) {
		t.Fatalf("got %v", err)
	}
	if after, _ := os.ReadFile(h.o.StorePath); !bytes.Equal(before, after) {
		t.Fatal("pinned a key for a moved endpoint")
	}
}

func TestKnownHostsHintReachesThePrompt(t *testing.T) {
	h, srv, c, _ := trustHub(t)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	other, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))}, other)
	if err := os.WriteFile(h.o.KnownHostsPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := openTerm(t, c, "t1", nil); r.KnownHosts != "different" {
		t.Fatalf("got %+v", r)
	}
}
```

In `internal/hub/term_test.go` `TestTermOpenValidatesParams`, add this entry at the end of the params table:

```go
		{"id": "ok", "server": "vis", "rows": 24, "cols": 80, "trustHostKey": map[string]string{"fingerprint": "SHA256:x"}},
```

Run: `go test ./internal/hub/ -run 'TestTrust|TestRotated|TestForgetOrDelete|TestKnownHostsHintReaches|TestTermOpenValidatesParams' -v`
Expected: FAIL to compile: `h.o.KnownHostsPath undefined`, `h.recordTrust undefined`.

- [ ] **Step 4: Add `Options.KnownHostsPath` and `recordTrust`**

In `internal/hub/hub.go` `type Options`, add after `IdleLock`:

```go
	KnownHostsPath string                         // "" means ~/.ssh/known_hosts; only a hint in the Trust prompt
```

In `internal/hub/hosts.go`, add `"github.com/lang315/ssh-mcp/internal/sshx"` to the imports and append:

```go
// recordTrust pins the key a trusted open just verified. The write is
// skipped, and the open fails, if the server got a pin or moved to another
// host or port while the dial ran.
func (h *Hub) recordTrust(name string, dc sshx.DialConfig) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	if err := config.RecordHostKey(h.o.StorePath, name, dc.Host, dc.Port, dc.HostKey, dc.HostKeyAlgo, key); err != nil {
		return err
	}
	_ = h.Reload()
	return nil
}
```

- [ ] **Step 5: Change `term.open`**

In `internal/hub/term.go`, set the import block to:

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/lang315/ssh-mcp/internal/sshx"
	"golang.org/x/crypto/ssh"
)
```

Replace the whole `s.HandleRequest("term.open", ...)` block with:

```go
	s.HandleRequest("term.open", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			ID           string `json:"id"`
			Server       string `json:"server"`
			Rows         int    `json:"rows"`
			Cols         int    `json:"cols"`
			TrustHostKey *struct {
				Fingerprint string `json:"fingerprint"`
				KeyType     string `json:"keyType"`
			} `json:"trustHostKey"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
		}
		if !termIDRe.MatchString(p.ID) {
			return nil, &rpc.Error{Code: -32602, Message: "id must be 1-64 characters of A-Z a-z 0-9 _ -"}
		}
		if !validDims(p.Rows, p.Cols) {
			return nil, &rpc.Error{Code: -32602, Message: fmt.Sprintf("rows and cols must be 1-%d", maxTermDim)}
		}
		if tk := p.TrustHostKey; tk != nil && (tk.Fingerprint == "" || tk.KeyType == "") {
			return nil, &rpc.Error{Code: -32602, Message: "trustHostKey needs fingerprint and keyType"}
		}
		e := &termEntry{}
		mu.Lock()
		_, dup := terms[p.ID]
		if !dup {
			terms[p.ID] = e
		}
		mu.Unlock()
		if dup {
			return nil, fmt.Errorf("terminal %q already open", p.ID)
		}
		release := func() {
			mu.Lock()
			if terms[p.ID] == e {
				delete(terms, p.ID)
			}
			mu.Unlock()
		}

		_ = h.Reload()
		dc, err := h.resolveForTerm(p.Server)
		if err != nil {
			release()
			return nil, err
		}
		// Trust applies only while the server has no pin; a pin set in the
		// meantime governs instead. The retry dials pinned to exactly the key
		// the user confirmed (a new config hash, so a fresh manager).
		trust := p.TrustHostKey != nil && dc.HostKey == ""
		if trust {
			dc.HostKey, dc.HostKeyAlgo = p.TrustHostKey.Fingerprint, p.TrustHostKey.KeyType
		}
		mgr := h.Registry().Get(p.Server, dc)
		mgr.StartKeepalive(30*time.Second, nil)
		t, err := mgr.OpenTerm(p.Rows, p.Cols,
			func(b []byte) {
				if owns(p.ID, e) {
					s.Notify("term.data", map[string]any{"id": p.ID, "data": b})
				}
			},
			func(code int, reason string) {
				// Only a terminal that exited on its own reports it; after
				// term.close the id may already belong to a new terminal.
				mu.Lock()
				owned := terms[p.ID] == e
				if owned {
					delete(terms, p.ID)
				}
				mu.Unlock()
				if owned {
					s.Notify("term.exit", map[string]any{"id": p.ID, "code": code, "reason": reason})
				}
			})
		if err != nil {
			release()
			if res := h.hostKeyResult(p.Server, dc, trust, err); res != nil {
				return res, nil
			}
			return nil, err
		}
		if trust {
			if err := h.recordTrust(p.Server, dc); err != nil {
				release() // first, so the close below sends no term.exit
				t.Close()
				h.Registry().Close(p.Server)
				return nil, err
			}
		}
		mu.Lock()
		gone := terms[p.ID] != e // closed (or exited) while opening
		if !gone {
			e.t = t
		}
		mu.Unlock()
		if gone {
			t.Close()
		}
		return map[string]string{"status": "open", "id": p.ID}, nil
	})
```

Append to `internal/hub/term.go`:

```go
// hostKeyResult turns a host-key refusal into term.open's result; nil for
// any other error. Nothing was opened or recorded on these paths. After a
// trusted retry a different key is asked about again, never reported as a
// mismatch: the user never confirmed that pin.
func (h *Hub) hostKeyResult(name string, dc sshx.DialConfig, trusting bool, err error) map[string]any {
	var unknown *sshx.HostKeyUnknownError
	var mismatch *sshx.HostKeyMismatchError
	switch {
	case errors.As(err, &unknown):
		return h.unknownResult(name, dc, unknown.Fingerprint, unknown.KeyType, unknown.Key)
	case errors.As(err, &mismatch) && trusting:
		h.reg.Close(name)
		return h.unknownResult(name, dc, mismatch.Presented, mismatch.KeyType, mismatch.Key)
	case errors.As(err, &mismatch):
		return map[string]any{"status": "hostKeyMismatch", "server": name, "host": dc.Host, "port": dc.Port, "user": dc.User,
			"pinned": mismatch.Pinned, "presented": mismatch.Presented}
	}
	return nil
}

func (h *Hub) unknownResult(name string, dc sshx.DialConfig, fp, keyType string, key ssh.PublicKey) map[string]any {
	return map[string]any{"status": "hostKeyUnknown", "server": name, "host": dc.Host, "port": dc.Port, "user": dc.User,
		"fingerprint": fp, "keyType": keyType, "knownHosts": sshx.KnownHostsHint(h.knownHostsPath(), dc.Host, dc.Port, key)}
}

func (h *Hub) knownHostsPath() string {
	if h.o.KnownHostsPath != "" {
		return h.o.KnownHostsPath
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "known_hosts")
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/hub/ ./internal/sshx/ -run 'TestTrust|TestRotated|TestForgetOrDelete|TestKnownHosts|TestTerm' -v`
Expected: PASS. `TestTermOpenWriteInOrderAndClose` still reads `id` from the result.

Run: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...`
Expected: all `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/sshx internal/hub
git commit -m "$(cat <<'EOF'
feat(hub): term.open host-key results and a trusted retry

An unknown or changed host key is a term.open result, never an error, so
the app can show it. trustHostKey retries pinned to exactly the confirmed
key and records it only while the server is unpinned at the dialled
endpoint. known_hosts is shown as a hint, never pinned from.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
)"
```

---

### Task 6: Bind AI approvals to `user@host:port` and the pin

**Files:**
- Modify: `internal/broker/broker.go:23-32` (Request), `internal/hub/hub.go` (imports, error vars, `target`, `Exec`)
- Test: `internal/hub/hub_test.go`, `internal/broker/broker_test.go` (`TestJSONTags`)

**Interfaces:**
- Consumes: `newHubExpiry`, `waitPending`, `allowFirst`, `readAudit` (existing test helpers); `config.Update` (Task 1).
- Produces: `broker.Request.Target string` (`json:"target"`, `user@host:port`); `hub.ErrServerChanged` (`"server changed"`); `func target(dc sshx.DialConfig) string`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/hub/hub_test.go`:

```go
// An approval is for user@host:port and pin as shown; an edit during the
// wait voids it.
func TestApprovalBoundToEndpoint(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHubExpiry(t, fe, time.Minute)
	done := make(chan error, 1)
	go func() { _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); done <- err }()
	waitPending(t, h.Broker(), 1)
	if got := h.Broker().Pending()[0].Target; got != "u@h:22" {
		t.Fatalf("target = %q", got)
	}
	if err := config.Update(path, nil, func(f *config.File) error { f.Servers[0].Port = 2222; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	allowFirst(t, h.Broker())
	if err := <-done; !errors.Is(err, ErrServerChanged) {
		t.Fatalf("got %v", err)
	}
	if len(fe.calls) != 0 {
		t.Fatalf("ran on the new endpoint: %v", fe.calls)
	}
	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if last["outcome"] != "error" || !strings.Contains(last["reason"].(string), "server changed") {
		t.Fatalf("audit: %v", last)
	}
}
```

In `internal/broker/broker_test.go` `TestJSONTags`, replace the first marshal and check with:

```go
	rb, err := json.Marshal(Request{ID: "x", TimeoutSec: 5, Target: "u@h:22"})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(rb); !strings.Contains(s, `"id":"x"`) || !strings.Contains(s, `"timeoutSec":5`) || !strings.Contains(s, `"target":"u@h:22"`) {
		t.Fatalf("Request tags: %s", s)
	}
```

Run: `go test ./internal/hub/ ./internal/broker/ -run 'TestApprovalBoundToEndpoint|TestJSONTags' -v`
Expected: FAIL to compile: `unknown field Target in struct literal`, `undefined: ErrServerChanged`.

- [ ] **Step 2: Add `Request.Target`**

In `internal/broker/broker.go`, replace `type Request` with:

```go
type Request struct {
	ID          string    `json:"id"`
	Client      string    `json:"client"`
	Server      string    `json:"server"`
	Target      string    `json:"target"` // user@host:port the approval is for
	Command     string    `json:"command"`
	Description string    `json:"description"`
	Sudo        bool      `json:"sudo"`
	TimeoutSec  int       `json:"timeoutSec"`
	ReceivedAt  time.Time `json:"receivedAt"`
}
```

- [ ] **Step 3: Bind and re-check the endpoint in `Exec`**

In `internal/hub/hub.go`, add `"net"` and `"strconv"` to the imports. In the AI-facing `var (...)` block, add after `ErrConnFailed`:

```go
	// ErrServerChanged: the server was edited between approval and run, so
	// the approval (for the old user@host:port and pin) no longer applies.
	ErrServerChanged = errors.New("server changed")
```

Add below `redactorFor`:

```go
// target is the endpoint an approval is for, as the approval card shows it.
func target(dc sshx.DialConfig) string {
	return dc.User + "@" + net.JoinHostPort(dc.Host, strconv.Itoa(dc.Port))
}
```

In `Exec`, change the request literal to carry the target:

```go
	req := broker.Request{Client: r.Client, Server: r.Server, Target: target(dc), Command: cmd, Description: r.Description, Sudo: r.Sudo, TimeoutSec: timeout}
```

and insert right after the `dc2, err := h.resolveForAI(r.Server)` error block (before `// Secrets may have changed...`):

```go
	if target(dc2) != target(dc) || dc2.HostKey != dc.HostKey {
		base.Outcome = "error"
		base.Reason = fmt.Sprintf("server changed: approved %s %s, now %s %s", target(dc), dc.HostKey, target(dc2), dc2.HostKey)
		h.record(base)
		return ExecResponse{}, ErrServerChanged
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/hub/ ./internal/broker/ -v`
Expected: PASS.

Run: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...`
Expected: all `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/broker internal/hub
git commit -m "$(cat <<'EOF'
feat(hub): bind AI approvals to user@host:port and the pin

The request carries the endpoint the human approves; after approval the
re-resolved server must match it and the pin, else the exec fails with
"server changed" and is audited.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
)"
```

---

### Task 7: Config audit records

**Files:**
- Modify: `internal/broker/audit.go`, `internal/hub/hosts.go` (whole file)
- Test: `internal/broker/audit_test.go`, `internal/hub/configaudit_test.go` (new)

**Interfaces:**
- Consumes: everything in `hosts.go` so far (Tasks 3–5); test helpers `newHubAt`, `startTermDoor`, `save`, `openTerm`, `trusting`, `readAudit`.
- Produces:
  - `type broker.ConfigRecord struct { Time time.Time; Kind, Action, Server, Host string; Port int; Fingerprint, Algo, OldFingerprint string; KeptServers, Changed []string }` (JSON `time kind action server host port fingerprint algo oldFingerprint keptServers changed`, all but `time`, `kind`, `action` omitempty)
  - `func (a *Audit) WriteConfig(r ConfigRecord) error` (sets `Kind = "config"`)
  - `func (h *Hub) auditConfig(r broker.ConfigRecord)`; `func changes(a, b config.Server, in config.ServerInput) []string`

- [ ] **Step 1: Write the failing tests**

Append to `internal/broker/audit_test.go`:

```go
func TestAuditConfigRecordShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.WriteConfig(ConfigRecord{Time: time.Now(), Action: "trust", Server: "s", Host: "h", Port: 22, Fingerprint: "SHA256:x", Algo: "ssh-ed25519"}); err != nil {
		t.Fatal(err)
	}
	a.Close()
	raw, _ := os.ReadFile(path)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["kind"] != "config" || m["action"] != "trust" || m["fingerprint"] != "SHA256:x" || m["algo"] != "ssh-ed25519" {
		t.Fatalf("got %v", m)
	}
	if _, has := m["command"]; has {
		t.Fatal("a config record carries exec fields")
	}
}
```

Create `internal/hub/configaudit_test.go`:

```go
package hub

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx/sshtest"
)

func TestConfigAuditOneRecordPerActionAndNoSecrets(t *testing.T) {
	srv := sshtest.Start(t)
	h, path := newHubAt(t, &config.File{Version: 1, Servers: []config.Server{
		{Name: "box", Host: srv.Host, Port: srv.Port, User: "u", Auth: "password"}}}, nil)
	h.o.KnownHostsPath = filepath.Join(t.TempDir(), "known_hosts")
	c, _, _ := startTermDoor(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	secrets := []string{"sekret-pw-1", "sekret-su-2", "sekret-sudo-3", "sekret-kp-4"}
	in := config.ServerInput{Name: "box", Host: srv.Host, Port: srv.Port, User: "u", Auth: "password", AIVisible: true,
		Password: &secrets[0], SuPassword: &secrets[1], SudoPassword: &secrets[2], KeyPassphrase: &secrets[3]}
	if err := save(c, "box", in); err != nil {
		t.Fatal(err)
	}
	if r := openTerm(t, c, "t1", trusting(openTerm(t, c, "t1", nil))); r.Status != "open" {
		t.Fatalf("trust: %+v", r)
	}
	for _, m := range []string{"servers.forgetHostKey", "servers.delete"} {
		if err := c.Call(ctx, m, map[string]string{"name": "box"}, nil); err != nil {
			t.Fatal(err)
		}
	}

	raw, recs := readAudit(t, path)
	for _, s := range secrets {
		if strings.Contains(raw, s) {
			t.Fatalf("audit leaked %q", s)
		}
	}
	var actions []string
	by := map[string]map[string]any{}
	for _, r := range recs {
		if r["kind"] == "config" {
			a := r["action"].(string)
			actions = append(actions, a)
			by[a] = r
		}
	}
	if !slices.Equal(actions, []string{"vaultCreate", "save", "trust", "forgetHostKey", "delete"}) {
		t.Fatalf("actions %v", actions)
	}
	if kept := by["vaultCreate"]["keptServers"].([]any); len(kept) != 1 || kept[0] != "box" {
		t.Fatalf("keptServers %v", kept)
	}
	changed := fmt.Sprint(by["save"]["changed"])
	for _, want := range []string{"aiVisible: false → true", "password", "suPassword", "sudoPassword", "keyPassphrase"} {
		if !strings.Contains(changed, want) {
			t.Fatalf("changed %s lacks %q", changed, want)
		}
	}
	if tr := by["trust"]; tr["server"] != "box" || tr["fingerprint"] != srv.Fingerprint() || tr["algo"] != "ssh-ed25519" || tr["host"] != srv.Host {
		t.Fatalf("trust %v", tr)
	}
	if by["forgetHostKey"]["oldFingerprint"] != srv.Fingerprint() || by["delete"]["server"] != "box" {
		t.Fatalf("forget %v delete %v", by["forgetHostKey"], by["delete"])
	}
}
```

Run: `go test ./internal/broker/ ./internal/hub/ -run 'TestAuditConfigRecordShape|TestConfigAudit' -v`
Expected: FAIL to compile: `undefined: ConfigRecord`, `a.WriteConfig undefined`.

- [ ] **Step 2: Add `ConfigRecord`**

In `internal/broker/audit.go`, add after `AuditRecord`:

```go
// ConfigRecord is an audit line for a vault change made from the app. It
// never holds a secret: Changed names a changed secret field, nothing more.
type ConfigRecord struct {
	Time           time.Time `json:"time"`
	Kind           string    `json:"kind"`   // always "config"; exec records have none
	Action         string    `json:"action"` // trust, forgetHostKey, delete, vaultCreate, save
	Server         string    `json:"server,omitempty"`
	Host           string    `json:"host,omitempty"`
	Port           int       `json:"port,omitempty"`
	Fingerprint    string    `json:"fingerprint,omitempty"`
	Algo           string    `json:"algo,omitempty"`
	OldFingerprint string    `json:"oldFingerprint,omitempty"`
	KeptServers    []string  `json:"keptServers,omitempty"`
	Changed        []string  `json:"changed,omitempty"`
}
```

Replace `Write` with:

```go
func (a *Audit) Write(r AuditRecord) error { return a.append(r) }

func (a *Audit) WriteConfig(r ConfigRecord) error {
	r.Kind = "config"
	return a.append(r)
}

func (a *Audit) append(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err = a.f.Write(append(line, '\n'))
	return err
}
```

- [ ] **Step 3: Record every config action**

Replace the whole of `internal/hub/hosts.go` with:

```go
package hub

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/lang315/ssh-mcp/internal/broker"
	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

// errNoVault: every write from the app needs a vault, the only thing that
// gives a key to encrypt with and to MAC under.
var errNoVault = errors.New("create a vault first")

// CreateVault gives a store with no master password (or no file yet) one.
// Kept servers lose aiVisible: a KDF-less file was never MAC'd, so its flags
// are unauthenticated. The key is derived once, here, and installed in the
// same h.mu section that saves and reloads, so nothing sees a vault that
// exists but is locked. Lock order h.mu → config.Update is safe: no path
// takes h.mu from inside an Update.
func (h *Hub) CreateVault(pw string) error {
	// A fresh load, not deps.File. It must come first: Update checks the key
	// before fn runs, so an existing vault would only fail as "wrong master
	// key" (and a race still fails that way, closed).
	if f, err := config.Load(h.o.StorePath); err == nil && f.KDF != nil {
		return errors.New("a vault already exists")
	}
	k, mk, err := config.NewKDF(pw)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var kept []string
	err = config.Update(h.o.StorePath, mk, func(f *config.File) error {
		for i := range f.Servers {
			s := &f.Servers[i]
			if s.EncPassword != "" || s.EncSuPassword != "" || s.EncSudoPassword != "" || s.EncKeyPassphrase != "" {
				return errors.New("the store has encrypted fields but no master password; it is corrupt or was tampered with")
			}
			s.AIVisible = false
			kept = append(kept, s.Name)
		}
		f.KDF = &k
		return nil
	})
	if err != nil {
		clear(mk)
		return err
	}
	clear(h.deps.MasterKey)
	h.deps.MasterKey = mk
	h.lastActivity = time.Now()
	h.auditConfig(broker.ConfigRecord{Action: "vaultCreate", KeptServers: kept})
	return h.reloadLocked()
}

// auditConfig never takes h.mu, so CreateVault may call it holding h.mu.
func (h *Hub) auditConfig(r broker.ConfigRecord) {
	if h.audit != nil {
		r.Time = time.Now()
		_ = h.audit.WriteConfig(r)
	}
}

// changes lists what a save changed: before→after for plain fields and the
// pin, and only the name of a secret that was re-supplied or dropped.
func changes(a, b config.Server, in config.ServerInput) []string {
	var out []string
	for _, f := range []struct {
		name string
		x, y any
	}{
		{"name", a.Name, b.Name}, {"host", a.Host, b.Host}, {"port", a.Port, b.Port}, {"user", a.User, b.User},
		{"auth", a.Auth, b.Auth}, {"keyPath", a.KeyPath, b.KeyPath}, {"aiVisible", a.AIVisible, b.AIVisible},
		{"hostKey", a.HostKey, b.HostKey},
	} {
		if f.x != f.y {
			out = append(out, fmt.Sprintf("%s: %v → %v", f.name, f.x, f.y))
		}
	}
	for _, s := range []struct {
		name string
		in   *string
		x, y string
	}{
		{"password", in.Password, a.EncPassword, b.EncPassword},
		{"suPassword", in.SuPassword, a.EncSuPassword, b.EncSuPassword},
		{"sudoPassword", in.SudoPassword, a.EncSudoPassword, b.EncSudoPassword},
		{"keyPassphrase", in.KeyPassphrase, a.EncKeyPassphrase, b.EncKeyPassphrase},
	} {
		if s.in != nil || (s.x != "" && s.y == "") {
			out = append(out, s.name)
		}
	}
	return out
}

// writeKey returns a copy of the master key for a store write.
func (h *Hub) writeKey() ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.deps.MasterKey != nil:
		return bytes.Clone(h.deps.MasterKey), nil
	case h.deps.File != nil && h.deps.File.KDF != nil:
		return nil, ErrLocked
	}
	return nil, errNoVault
}

// denyPending denies name's pending AI requests: each was for the server as
// it was when submitted.
func (h *Hub) denyPending(name string) {
	for _, r := range h.broker.Pending() {
		if r.Server == name {
			_ = h.broker.Decide(r.ID, broker.Decision{Outcome: broker.Denied, Reason: "server changed"})
		}
	}
}

// dialChanged: every field but AIVisible feeds the dial config or the name
// the connection is registered under.
func dialChanged(a, b config.Server) bool {
	a.AIVisible = b.AIVisible
	return a != b
}

// SaveServer creates (original == "") or updates a server. Its connection is
// closed only if something that feeds the dial changed, never for an
// aiVisible toggle; its pending AI requests are denied either way.
func (h *Hub) SaveServer(original string, in config.ServerInput) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	var before, after config.Server
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		var err error
		before, after, err = config.ApplyServer(f, original, in, key)
		return err
	})
	if err != nil {
		return err
	}
	_ = h.Reload()
	name := original
	if name == "" {
		name = in.Name
	}
	if dialChanged(before, after) {
		h.reg.Close(name)
	}
	h.denyPending(name)
	h.auditConfig(broker.ConfigRecord{Action: "save", Server: after.Name, Changed: changes(before, after, in)})
	return nil
}

func (h *Hub) DeleteServer(name string) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		for i, s := range f.Servers {
			if s.Name == name {
				f.Servers = append(f.Servers[:i], f.Servers[i+1:]...)
				return nil
			}
		}
		return serverNotFound(name)
	})
	if err != nil {
		return err
	}
	_ = h.Reload()
	h.reg.Close(name)
	h.denyPending(name)
	h.auditConfig(broker.ConfigRecord{Action: "delete", Server: name})
	return nil
}

// ForgetHostKey clears a server's pin and closes its connection, so the next
// open asks the user again. An unpinned server is a no-op success.
func (h *Hub) ForgetHostKey(name string) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	var old string
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == name {
				old = f.Servers[i].HostKey
				f.Servers[i].HostKey, f.Servers[i].HostKeyAlgo = "", ""
				return nil
			}
		}
		return serverNotFound(name)
	})
	if err != nil {
		return err
	}
	_ = h.Reload()
	h.reg.Close(name)
	h.auditConfig(broker.ConfigRecord{Action: "forgetHostKey", Server: name, OldFingerprint: old})
	return nil
}

// recordTrust pins the key a trusted open just verified. The write is
// skipped, and the open fails, if the server got a pin or moved to another
// host or port while the dial ran.
func (h *Hub) recordTrust(name string, dc sshx.DialConfig) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	if err := config.RecordHostKey(h.o.StorePath, name, dc.Host, dc.Port, dc.HostKey, dc.HostKeyAlgo, key); err != nil {
		return err
	}
	_ = h.Reload()
	h.auditConfig(broker.ConfigRecord{Action: "trust", Server: name, Host: dc.Host, Port: dc.Port, Fingerprint: dc.HostKey, Algo: dc.HostKeyAlgo})
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/broker/ ./internal/hub/ -v`
Expected: PASS, including `TestAuditConfigRecordShape`, `TestConfigAuditOneRecordPerActionAndNoSecrets`, and the existing `TestAuditOutcomes`.

Run: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...`
Expected: all `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/broker internal/hub
git commit -m "$(cat <<'EOF'
feat(hub): audit vault changes made from the app

vaultCreate, save, trust, forgetHostKey, and delete each write one
kind:"config" record: before→after for plain fields and the pin, and only
the names of changed secrets.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
)"
```

---

### Task 8: Protocol v2 on both sides, transport and types, approval card target

**Files:**
- Modify: `internal/hub/idle.go:5`, `internal/hub/uidoor_test.go:111`, `desktop/src/shared/protocol.ts` (whole file), `desktop/src/renderer/transport.ts` (whole file), `desktop/src/renderer/ApprovalPanel.tsx` (whole file), `desktop/src/renderer/App.tsx:107` (ApprovalPanel props), `desktop/test/fixtures/fakeHub.mjs:26`
- Test: `desktop/test/transport.test.ts`, `desktop/test/ipc.test.ts`, `desktop/test/ApprovalPanel.test.ts`, `desktop/test/approvals.test.ts:6`, `desktop/test/attention.test.ts:6`

**Interfaces:**
- Consumes: the hub methods and result shapes from Tasks 3–6.
- Produces (TypeScript, `shared/protocol.ts`): `ApprovalRequest.target`; `ServerInfo` with `keyPath, hostKeyAlgo, hasPassword, hasSuPassword, hasSudoPassword, hasKeyPassphrase`; `type SecretField = 'password' | 'suPassword' | 'sudoPassword' | 'keyPassphrase'`; `interface ServerInput`; `interface Status { locked; hasStore; hasVault; storePath; pending }`; `interface HostKeyUnknown`; `interface HostKeyMismatch`; `type TermOpenResult`; `PROTOCOL_VERSION = 2`.
- Produces (`transport.ts` `hub`): `status(): Promise<Status>`, `createVault(password)`, `saveServer(server: ServerInput, original?: string)`, `deleteServer(name)`, `forgetHostKey(name)`, `termOpen(id, server, rows, cols, trustHostKey?: { fingerprint: string; keyType: string }): Promise<TermOpenResult>`.
- `ApprovalPanel` no longer takes `servers`; `Item` no longer takes `server`.

The Go and TypeScript version bumps must land in this one commit: `fakeHub.mjs` answering `protocol: 1` fails every `hubProcess.test.ts` case once `PROTOCOL_VERSION` is 2.

- [ ] **Step 1: Write the failing tests**

In `desktop/test/transport.test.ts`, replace the `termOpen sends the client-chosen id` test with:

```ts
  it('termOpen returns the hub result and sends trustHostKey only on a trusted retry', async () => {
    bridge.call.mockResolvedValueOnce({ status: 'open' })
    expect(await hub.termOpen('t2', 'box', 24, 80)).toEqual({ status: 'open' })
    expect(bridge.call).toHaveBeenCalledWith('term.open', { id: 't2', server: 'box', rows: 24, cols: 80 })
    await hub.termOpen('t2', 'box', 24, 80, { fingerprint: 'SHA256:x', keyType: 'ssh-ed25519' })
    expect(bridge.call).toHaveBeenLastCalledWith('term.open',
      { id: 't2', server: 'box', rows: 24, cols: 80, trustHostKey: { fingerprint: 'SHA256:x', keyType: 'ssh-ed25519' } })
  })

  it('host management calls', async () => {
    await hub.createVault('password1')
    expect(bridge.call).toHaveBeenLastCalledWith('vault.create', { password: 'password1' })
    const s = { name: 'box', host: 'h', port: 22, user: 'u', auth: 'password', keyPath: '', aiVisible: false, password: '' }
    await hub.saveServer(s, 'old')
    expect(bridge.call).toHaveBeenLastCalledWith('servers.save', { original: 'old', server: s })
    await hub.deleteServer('box')
    expect(bridge.call).toHaveBeenLastCalledWith('servers.delete', { name: 'box' })
    await hub.forgetHostKey('box')
    expect(bridge.call).toHaveBeenLastCalledWith('servers.forgetHostKey', { name: 'box' })
  })
```

In `desktop/test/ipc.test.ts` `relays whitelisted requests`, add before the closing `})`:

```ts
    expect(await relayCall(h, 'servers.save', { server: {} })).toEqual({ ok: true })
```

Replace the whole of `desktop/test/ApprovalPanel.test.ts` with:

```ts
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { Item } from '../src/renderer/ApprovalPanel'
import { seed } from '../src/renderer/approvals'
import type { ApprovalRequest } from '../src/shared/protocol'

const req: ApprovalRequest = {
  id: 'a', client: 'claude-code', server: 'box', target: 'u@h:22', command: 'ls', description: '', sudo: false, timeoutSec: 60, receivedAt: '2024-01-01T00:00:00Z',
}

describe('Item', () => {
  it('makes Allow and Send to tab keyboard-unreachable; only Deny is a submit button', () => {
    const [item] = seed([req], 0)
    const html = renderToStaticMarkup(createElement(Item, {
      item, now: 10_000,
      onDecide: async () => {}, onSendToTab: async () => {},
    }))
    const buttons = [...html.matchAll(/<button([^>]*)>([^<]*)<\/button>/g)].map((m) => ({ attrs: m[1], text: m[2] }))

    const submitButtons = buttons.filter((b) => /type="submit"/.test(b.attrs))
    expect(submitButtons).toHaveLength(1)
    expect(submitButtons[0].text).toBe('Deny')

    const allow = buttons.find((b) => b.text === 'Allow')!
    expect(allow.attrs).toMatch(/type="button"/)
    expect(allow.attrs).toMatch(/tabindex="-1"/)

    const sendToTab = buttons.find((b) => b.text === 'Send to tab')!
    expect(sendToTab.attrs).toMatch(/type="button"/)
    expect(sendToTab.attrs).toMatch(/tabindex="-1"/)
  })

  it('shows the user@host:port the request was submitted for', () => {
    const [item] = seed([req], 0)
    const html = renderToStaticMarkup(createElement(Item, { item, now: 10_000, onDecide: async () => {}, onSendToTab: async () => {} }))
    expect(html).toContain('u@h:22')
  })
})
```

In `desktop/test/approvals.test.ts` line 6 and `desktop/test/attention.test.ts` line 6, add `target: 'u@h:22', ` after `server: 'box', ` in the request fixture.

Run: `cd desktop && npm run typecheck && npm test`
Expected: FAIL: typecheck reports `Object literal may only specify known properties, and 'target' does not exist in type 'ApprovalRequest'` and `Property 'createVault' does not exist`.

- [ ] **Step 2: Rewrite `shared/protocol.ts`**

Replace the whole of `desktop/src/shared/protocol.ts` with:

```ts
export type HubState =
  | { kind: 'starting' }
  | { kind: 'running' }
  | { kind: 'restarting'; attempt: number; inMs: number }
  | { kind: 'failed'; message: string; stderr: string }

export type Outcome = 'allowed' | 'denied' | 'expired' | 'withdrawn' | 'sent_to_tab'

export interface ApprovalRequest {
  id: string; client: string; server: string; target: string; command: string
  description: string; sudo: boolean; timeoutSec: number; receivedAt: string
}

export interface ServerInfo {
  name: string; host: string; port: number; user: string; auth: string; keyPath: string
  hostKey: string; hostKeyAlgo: string; aiVisible: boolean; locked: boolean
  hasPassword: boolean; hasSuPassword: boolean; hasSudoPassword: boolean; hasKeyPassphrase: boolean
}

export type SecretField = 'password' | 'suPassword' | 'sudoPassword' | 'keyPassphrase'

// Secrets are write-only: an omitted one is kept, '' clears it.
export interface ServerInput {
  name: string; host: string; port: number; user: string; auth: string; keyPath: string; aiVisible: boolean
  password?: string; suPassword?: string; sudoPassword?: string; keyPassphrase?: string
}

export interface Status { locked: boolean; hasStore: boolean; hasVault: boolean; storePath: string; pending: number }

export interface HostKeyUnknown {
  status: 'hostKeyUnknown'; server: string; host: string; port: number; user: string
  fingerprint: string; keyType: string; knownHosts: 'match' | 'different' | 'absent'
}

export interface HostKeyMismatch {
  status: 'hostKeyMismatch'; server: string; host: string; port: number; user: string
  pinned: string; presented: string
}

export type TermOpenResult = { status: 'open' } | HostKeyUnknown | HostKeyMismatch

export type HubEvent =
  | { method: 'pending'; params: { request: ApprovalRequest } }
  | { method: 'decided'; params: { request: ApprovalRequest; decision: { outcome: Outcome; reason: string } } }
  | { method: 'locked'; params: { reason: 'idle' | 'manual' } }
  | { method: 'term.data'; params: { id: string; data: string } }
  | { method: 'term.exit'; params: { id: string; code: number; reason: string } }
  | { method: 'term.dropped'; params: { id: string; bytes: number } }

export const REQUEST_METHODS = ['hello', 'status', 'unlock', 'lock', 'servers', 'pending',
  'decide', 'denyAll', 'term.open', 'term.close',
  'vault.create', 'servers.save', 'servers.delete', 'servers.forgetHostKey'] as const
export type RequestMethod = (typeof REQUEST_METHODS)[number]
export const NOTIFY_METHODS = ['term.write', 'term.ack', 'term.resize'] as const
export type NotifyMethod = (typeof NOTIFY_METHODS)[number]
export const PROTOCOL_VERSION = 2
```

- [ ] **Step 3: Rewrite `transport.ts`**

Replace the whole of `desktop/src/renderer/transport.ts` with:

```ts
import type { ApprovalRequest, HubEvent, HubState, ServerInfo, ServerInput, Status, TermOpenResult } from '../shared/protocol'

interface Bridge {
  call(method: string, params?: unknown): Promise<unknown>
  notify(method: string, params?: unknown): void
  getState(): Promise<HubState>
  onEvent(cb: (e: HubEvent) => void): () => void
  onState(cb: (s: HubState) => void): () => void
}

const bridge = (): Bridge => (window as unknown as { sshmcp: Bridge }).sshmcp

// Electron prefixes invoke errors with "Error invoking remote method 'hub:call': Error: ".
export function cleanError(e: unknown): Error {
  const msg = e instanceof Error ? e.message : String(e)
  return new Error(msg.replace(/^Error invoking remote method '[^']+': (Error: )?/, ''))
}

async function call<T>(method: string, params?: unknown): Promise<T> {
  try {
    return (await bridge().call(method, params)) as T
  } catch (e) {
    throw cleanError(e)
  }
}

export function toBase64(b: Uint8Array): string {
  let s = ''
  for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000))
  return btoa(s)
}

export function fromBase64(s: string): Uint8Array {
  const bin = atob(s)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

export const hub = {
  hello: () => call<{ protocol: number }>('hello'),
  status: () => call<Status>('status'),
  unlock: async (password: string) => { await call('unlock', { password }) },
  lock: async () => { await call('lock') },
  servers: () => call<ServerInfo[]>('servers'),
  pending: () => call<ApprovalRequest[]>('pending'),
  decide: async (id: string, outcome: 'allowed' | 'denied' | 'sent_to_tab', reason = '') => {
    await call('decide', { id, outcome, reason })
  },
  denyAll: async (reason = '') => { await call('denyAll', { reason }) },
  createVault: async (password: string) => { await call('vault.create', { password }) },
  saveServer: async (server: ServerInput, original?: string) => { await call('servers.save', { original, server }) },
  deleteServer: async (name: string) => { await call('servers.delete', { name }) },
  forgetHostKey: async (name: string) => { await call('servers.forgetHostKey', { name }) },
  // A host-key outcome is a result, not an error; trustHostKey retries pinned to exactly that key.
  termOpen: (id: string, server: string, rows: number, cols: number, trustHostKey?: { fingerprint: string; keyType: string }) =>
    call<TermOpenResult>('term.open', trustHostKey ? { id, server, rows, cols, trustHostKey } : { id, server, rows, cols }),
  termClose: async (id: string) => { await call('term.close', { id }) },
  termWrite: (id: string, data: Uint8Array, user: boolean) => bridge().notify('term.write', { id, data: toBase64(data), user }),
  termAck: (id: string, n: number) => bridge().notify('term.ack', { id, n }),
  termResize: (id: string, rows: number, cols: number) => bridge().notify('term.resize', { id, rows, cols }),
  onEvent: (cb: (e: HubEvent) => void) => bridge().onEvent(cb),
  onState: (cb: (s: HubState) => void) => bridge().onState(cb),
  getState: () => bridge().getState(),
}
```

- [ ] **Step 4: Show the bound target on the approval card**

Replace the whole of `desktop/src/renderer/ApprovalPanel.tsx` with:

```tsx
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { allowEnabled, blockKeyboardActivation, highlightNonAscii, ListChanges, type PendingItem } from './approvals'

export function ApprovalPanel({ items, seedError, onDecide, onDenyAll, onSendToTab }: {
  items: PendingItem[]
  seedError?: string
  onDecide: (id: string, outcome: 'allowed' | 'denied', reason: string) => Promise<void>
  onDenyAll: () => Promise<void>
  onSendToTab: (item: PendingItem) => Promise<void>
}) {
  const [now, setNow] = useState(Date.now())
  const [denyAllError, setDenyAllError] = useState<string>()
  // Mount time and every change to the list (ids or height) restart the Allow delay
  // (see allowEnabled).
  const listKey = items.map((i) => i.request.id).join('\n')
  const changes = useRef<ListChanges>(null)
  changes.current ??= new ListChanges(listKey, Date.now())
  changes.current.setKey(listKey, Date.now())
  const changedAt = changes.current.at
  const listRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const ro = new ResizeObserver(([e]) => {
      if (changes.current!.setHeight(e.contentRect.height, Date.now())) setNow(Date.now())
    })
    ro.observe(listRef.current!)
    return () => ro.disconnect()
  }, [])
  const young = items.some((i) => !allowEnabled(i, now, changedAt))
  useEffect(() => {
    if (!young) return
    const t = setInterval(() => setNow(Date.now()), 100)
    return () => clearInterval(t)
  }, [young])

  const denyAll = () => onDenyAll().then(() => setDenyAllError(undefined), (e) => setDenyAllError((e as Error).message))

  return (
    <aside className="approvals" aria-label="Approval requests">
      <h3>AI requests {items.length > 0 && `(${items.length})`}</h3>
      {/* Always rendered so the list never shifts when it appears or disappears. */}
      <button className="denyall" onClick={denyAll} disabled={items.length < 2}>Deny all</button>
      {/* Observed for height changes: anything that shifts the items lives in here. */}
      <div ref={listRef}>
        {denyAllError && <p className="error">{denyAllError}</p>}
        {items.length === 0 && (
          seedError
            ? <p className="error">Could not load pending requests: {seedError}</p>
            : <p className="muted">Nothing waiting.</p>
        )}
        {items.map((item) => (
          <Item key={item.request.id} item={item} now={now} changedAt={changedAt}
            onDecide={onDecide} onSendToTab={onSendToTab} />
        ))}
      </div>
    </aside>
  )
}

export function Item({ item, now, changedAt = 0, onDecide, onSendToTab }: {
  item: PendingItem; now: number; changedAt?: number
  onDecide: (id: string, outcome: 'allowed' | 'denied', reason: string) => Promise<void>
  onSendToTab: (item: PendingItem) => Promise<void>
}) {
  const r = item.request
  const [reason, setReason] = useState('')
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const act = (p: Promise<void>) => {
    setBusy(true); setError(undefined)
    p.then(() => setBusy(false), (e) => { setBusy(false); setError((e as Error).message) })
  }
  const deny = (e: FormEvent) => { e.preventDefault(); if (!busy) act(onDecide(r.id, 'denied', reason)) }
  return (
    <form className="approval" onSubmit={deny}>
      <div className="who">
        <strong>{r.server}</strong>
        {/* The endpoint this approval is bound to; the hub refuses the run if it changes. */}
        <span className="muted">{` ${r.target}`}</span>
        {r.sudo && <span className="badge warn">SUDO</span>}
      </div>
      <pre className="cmd">
        {highlightNonAscii(r.command).map((s, i) => (s.nonAscii ? <mark key={i}>{s.text}</mark> : <span key={i}>{s.text}</span>))}
      </pre>
      <div className="muted">timeout: {r.timeoutSec}s</div>
      {r.description && <div className="desc">AI's description (unverified): {r.description}</div>}
      <div className="muted">client: {r.client} (unverified) · {new Date(r.receivedAt).toLocaleTimeString()}</div>
      <input placeholder="Reason (optional)" value={reason} onChange={(e) => setReason(e.target.value)} />
      <div className="actions">
        <button type="submit" className="deny" disabled={busy}>Deny</button>
        <button type="button" className="allow" tabIndex={-1} onKeyDown={blockKeyboardActivation}
          disabled={busy || !allowEnabled(item, now, changedAt)}
          onClick={() => { if (!busy) act(onDecide(r.id, 'allowed', '')) }}>Allow</button>
        <button type="button" tabIndex={-1} onKeyDown={blockKeyboardActivation} disabled={busy}
          onClick={() => { if (!busy) act(onSendToTab(item)) }}>Send to tab</button>
      </div>
      {error && <p className="error">{error}</p>}
    </form>
  )
}
```

In `desktop/src/renderer/App.tsx`, change `<ApprovalPanel items={items} servers={servers} seedError={seedError}` to `<ApprovalPanel items={items} seedError={seedError}`.

- [ ] **Step 5: Bump the protocol on both sides**

In `internal/hub/idle.go`: `const ProtocolVersion = 2`.

In `internal/hub/uidoor_test.go` `TestUIDoorHelloAndLockedNotification`: change `hello.Protocol != 1` to `hello.Protocol != 2`.

In `desktop/test/fixtures/fakeHub.mjs`: change `result: { protocol: mode === 'badproto' ? 99 : 1 }` to `result: { protocol: mode === 'badproto' ? 99 : 2 }`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd desktop && npm run typecheck && npm test`
Expected: typecheck clean; all Vitest files pass, including `transport.test.ts`, `ipc.test.ts`, `ApprovalPanel.test.ts`, `hubProcess.test.ts`.

Run: `go vet ./... && GOOS=windows go vet ./... && go test -race ./...`
Expected: all `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/hub/idle.go internal/hub/uidoor_test.go desktop/src desktop/test
git commit -m "$(cat <<'EOF'
feat(desktop): UI-door protocol 2 types, transport, and bound approval target

term.open now answers with a status result and takes trustHostKey; the
transport gains vault and host methods. The approval card shows the
user@host:port the request is bound to instead of the current listing.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
)"
```

---

### Task 9: Create vault screen, host list actions, host editor

**Files:**
- Create: `desktop/src/renderer/hostForm.ts`, `desktop/src/renderer/CreateVault.tsx`, `desktop/src/renderer/HostEditor.tsx`, `desktop/test/hostForm.test.ts`
- Modify: `desktop/src/renderer/shell.ts` (whole file), `desktop/src/renderer/HostList.tsx` (whole file), `desktop/src/renderer/App.tsx` (whole file), `desktop/src/renderer/terminals.ts` (`TabSet.openCount`), `desktop/src/renderer/TerminalTabs.tsx:6-9,39-48` (handle), `desktop/src/renderer/styles.css` (append)
- Test: `desktop/test/shell.test.ts`, `desktop/test/terminals.test.ts`

**Interfaces:**
- Consumes: `Status`, `ServerInfo`, `ServerInput`, `SecretField`, `hub.createVault/saveServer/deleteServer/forgetHostKey/servers` (Task 8).
- Produces:
  - `Screen` kind `'create-vault'` (replaces `'no-store'`); `screenFor(hub, status?: { locked: boolean; hasVault: boolean }, unlockError?)`
  - `hostForm.ts`: `SECRET_FIELDS`, `interface SecretEdit { value: string; cleared: boolean }`, `interface HostDraft`, `draftFrom(s?)`, `toInput(d)`, `secretPlaceholder(saved, e)`, `endpointChanged(s, d)`, `closesTabs(s, d)`, `vaultPasswordProblem(pw, again)`
  - `CreateVault({ servers, onCreate })`, `HostEditor({ server?, openTabs, focusForget?, onSave, onForget, onClose })`, `EditorWarnings({ server?, draft, openTabs })`
  - `HostList({ servers, storePath, onOpen, onNew, onEdit, onDelete })`
  - `TabSet.openCount(server): number`; `TerminalsHandle.openCount(server): number`

- [ ] **Step 1: Write the failing tests**

Replace the whole of `desktop/test/shell.test.ts` with:

```ts
import { describe, expect, it } from 'vitest'
import { screenFor } from '../src/renderer/shell'

describe('screenFor', () => {
  it('shows the hub screen until the hub runs', () => {
    expect(screenFor({ kind: 'starting' })).toEqual({ kind: 'hub', state: { kind: 'starting' } })
    const failed = { kind: 'failed' as const, message: 'x', stderr: 'y' }
    expect(screenFor(failed, { locked: false, hasVault: true })).toEqual({ kind: 'hub', state: failed })
  })
  it('waits for status after the hub runs', () => {
    expect(screenFor({ kind: 'running' })).toEqual({ kind: 'hub', state: { kind: 'starting' } })
  })
  it('asks to create a vault when the store has none, then locked, then ready', () => {
    expect(screenFor({ kind: 'running' }, { locked: false, hasVault: false })).toEqual({ kind: 'create-vault' })
    expect(screenFor({ kind: 'running' }, { locked: true, hasVault: true }, 'wrong master password'))
      .toEqual({ kind: 'locked', error: 'wrong master password' })
    expect(screenFor({ kind: 'running' }, { locked: false, hasVault: true })).toEqual({ kind: 'ready' })
  })
})
```

Create `desktop/test/hostForm.test.ts`:

```ts
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { closesTabs, draftFrom, endpointChanged, secretPlaceholder, toInput, vaultPasswordProblem } from '../src/renderer/hostForm'
import { EditorWarnings, HostEditor } from '../src/renderer/HostEditor'
import type { ServerInfo } from '../src/shared/protocol'

const box: ServerInfo = {
  name: 'box', host: 'h', port: 22, user: 'u', auth: 'password', keyPath: '', hostKey: 'SHA256:x', hostKeyAlgo: 'ssh-ed25519',
  aiVisible: false, locked: false, hasPassword: true, hasSuPassword: false, hasSudoPassword: false, hasKeyPassphrase: false,
}
const noop = async () => {}

describe('host editor secrets', () => {
  it('omits untouched secrets, sends "" for cleared ones and the value for typed ones', () => {
    const d = draftFrom(box)
    d.secrets.password = { value: '', cleared: true }
    d.secrets.suPassword = { value: 'su!', cleared: false }
    const input = toInput(d)
    expect(input).toEqual({ name: 'box', host: 'h', port: 22, user: 'u', auth: 'password', keyPath: '', aiVisible: false, password: '', suPassword: 'su!' })
    expect('sudoPassword' in input).toBe(false)
    expect('keyPassphrase' in input).toBe(false)
  })
  it('shows "saved" for a stored secret and offers Clear only for it', () => {
    expect(secretPlaceholder(true, { value: '', cleared: false })).toBe('saved')
    expect(secretPlaceholder(false, { value: '', cleared: false })).toBe('')
    expect(secretPlaceholder(true, { value: '', cleared: true })).toBe('will be cleared')
    const html = renderToStaticMarkup(createElement(HostEditor, { server: box, openTabs: 0, onSave: noop, onForget: noop, onClose: () => {} }))
    expect(html.match(/placeholder="saved"/g)).toHaveLength(1)
    expect(html.match(/>Clear</g)).toHaveLength(1)
    expect(html).toContain('SHA256:x')
    expect(html).toContain('Forget host key')
  })
})

describe('host editor warnings', () => {
  it('warns that a new host or port forgets the key and saved passwords', () => {
    const d = draftFrom(box)
    expect(endpointChanged(box, d)).toBe(false)
    expect(endpointChanged(box, { ...d, user: 'root' })).toBe(false)
    expect(endpointChanged(box, { ...d, port: '2222' })).toBe(true)
    expect(endpointChanged(box, { ...d, host: 'h2' })).toBe(true)
    expect(endpointChanged(undefined, d)).toBe(false)
    const html = renderToStaticMarkup(createElement(EditorWarnings, { server: box, draft: { ...d, port: '2222' }, openTabs: 2 }))
    expect(html).toContain('Changing host or port forgets the host key and saved passwords unless you re-enter them.')
    expect(html).toContain('Saving will close 2 open tabs.')
  })
  it('closes tabs only for connection changes, not for Visible to AI', () => {
    const d = draftFrom(box)
    expect(closesTabs(box, { ...d, aiVisible: true })).toBe(false)
    expect(closesTabs(box, { ...d, user: 'root' })).toBe(true)
    expect(closesTabs(box, { ...d, secrets: { ...d.secrets, sudoPassword: { value: 'x', cleared: false } } })).toBe(true)
    expect(renderToStaticMarkup(createElement(EditorWarnings, { server: box, draft: { ...d, aiVisible: true }, openTabs: 2 }))).toBe('')
  })
})

describe('vaultPasswordProblem', () => {
  it('needs 8 characters and a matching confirmation', () => {
    expect(vaultPasswordProblem('short', 'short')).toMatch(/8 characters/)
    expect(vaultPasswordProblem('password1', 'password2')).toMatch(/do not match/)
    expect(vaultPasswordProblem('password1', 'password1')).toBeUndefined()
  })
})
```

In `desktop/test/terminals.test.ts`, add inside `describe('TabSet', ...)`:

```ts
  it('counts the tabs still open per server', () => {
    const t = new TabSet()
    const a = t.open('box')
    t.open('box')
    t.open('other')
    t.exited(a.id, 'bye')
    expect(t.openCount('box')).toBe(1)
    expect(t.openCount('none')).toBe(0)
  })
```

Run: `cd desktop && npm test`
Expected: FAIL: `Failed to resolve import "../src/renderer/hostForm"`, `t.openCount is not a function`, and `create-vault` expectations.

- [ ] **Step 2: `screenFor` with `hasVault`**

Replace the whole of `desktop/src/renderer/shell.ts` with:

```ts
import type { HubState } from '../shared/protocol'

export type Screen =
  | { kind: 'hub'; state: HubState }
  | { kind: 'create-vault' }
  | { kind: 'locked'; error?: string }
  | { kind: 'ready' }

// A store without a master password (none yet, or key/agent-only) can only
// go forward by creating a vault: every write from the app needs one.
export function screenFor(
  hub: HubState,
  status?: { locked: boolean; hasVault: boolean },
  unlockError?: string,
): Screen {
  if (hub.kind !== 'running') return { kind: 'hub', state: hub }
  if (!status) return { kind: 'hub', state: { kind: 'starting' } }
  if (!status.hasVault) return { kind: 'create-vault' }
  if (status.locked) return unlockError ? { kind: 'locked', error: unlockError } : { kind: 'locked' }
  return { kind: 'ready' }
}
```

- [ ] **Step 3: Editor logic**

Create `desktop/src/renderer/hostForm.ts`:

```ts
import type { SecretField, ServerInfo, ServerInput } from '../shared/protocol'

export const SECRET_FIELDS: { field: SecretField; label: string; has: (s: ServerInfo) => boolean }[] = [
  { field: 'password', label: 'Password', has: (s) => s.hasPassword },
  { field: 'suPassword', label: 'su password', has: (s) => s.hasSuPassword },
  { field: 'sudoPassword', label: 'sudo password', has: (s) => s.hasSudoPassword },
  { field: 'keyPassphrase', label: 'Key passphrase', has: (s) => s.hasKeyPassphrase },
]

export interface SecretEdit { value: string; cleared: boolean }

export interface HostDraft {
  name: string; host: string; port: string; user: string; auth: string; keyPath: string; aiVisible: boolean
  secrets: Record<SecretField, SecretEdit>
}

const untouched = (): SecretEdit => ({ value: '', cleared: false })

export function draftFrom(s?: ServerInfo): HostDraft {
  return {
    name: s?.name ?? '', host: s?.host ?? '', port: String(s?.port ?? 22), user: s?.user ?? '',
    auth: s?.auth ?? 'password', keyPath: s?.keyPath ?? '', aiVisible: s?.aiVisible ?? false,
    secrets: { password: untouched(), suPassword: untouched(), sudoPassword: untouched(), keyPassphrase: untouched() },
  }
}

// Secrets are write-only: an untouched field is omitted (the hub keeps it),
// a cleared one is sent as '', a typed one as its value.
export function toInput(d: HostDraft): ServerInput {
  const input: ServerInput = {
    name: d.name.trim(), host: d.host.trim(), port: Number(d.port), user: d.user.trim(), auth: d.auth,
    keyPath: d.auth === 'key' ? d.keyPath.trim() : '', aiVisible: d.aiVisible,
  }
  for (const { field } of SECRET_FIELDS) {
    const e = d.secrets[field]
    if (e.cleared) input[field] = ''
    else if (e.value !== '') input[field] = e.value
  }
  return input
}

export function secretPlaceholder(saved: boolean, e: SecretEdit): string {
  if (e.cleared) return 'will be cleared'
  return saved ? 'saved' : ''
}

export function endpointChanged(s: ServerInfo | undefined, d: HostDraft): boolean {
  return !!s && (d.host.trim() !== s.host || Number(d.port) !== s.port)
}

// Mirrors the hub: everything but "Visible to AI" is part of the connection,
// so changing it closes the server's open tabs.
export function closesTabs(s: ServerInfo | undefined, d: HostDraft): boolean {
  if (!s) return false
  const i = toInput(d)
  return i.name !== s.name || i.host !== s.host || i.port !== s.port || i.user !== s.user || i.auth !== s.auth ||
    i.keyPath !== s.keyPath || SECRET_FIELDS.some(({ field }) => i[field] !== undefined)
}

export function vaultPasswordProblem(pw: string, again: string): string | undefined {
  if (pw.length < 8) return 'Use at least 8 characters.'
  if (pw !== again) return 'The passwords do not match.'
  return undefined
}
```

- [ ] **Step 4: Create vault screen and host editor**

Create `desktop/src/renderer/CreateVault.tsx`:

```tsx
import { useState, type FormEvent } from 'react'
import type { ServerInfo } from '../shared/protocol'
import { vaultPasswordProblem } from './hostForm'

export function CreateVault({ servers, onCreate }: { servers: ServerInfo[]; onCreate: (pw: string) => Promise<void> }) {
  const [pw, setPw] = useState('')
  const [again, setAgain] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const problem = vaultPasswordProblem(pw, again)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (problem) return
    setBusy(true); setError(undefined)
    try { await onCreate(pw) } catch (err) { setError((err as Error).message) } finally { setBusy(false) }
  }
  return (
    <form className="unlock" onSubmit={submit}>
      <h2>Create vault</h2>
      <p className="muted">The master password encrypts saved passwords and protects the host list. It cannot be recovered.</p>
      <input type="password" autoFocus aria-label="New master password" value={pw}
        onChange={(e) => setPw(e.target.value)} disabled={busy} />
      <input type="password" aria-label="Confirm master password" value={again}
        onChange={(e) => setAgain(e.target.value)} disabled={busy} />
      {pw !== '' && problem && <p className="muted">{problem}</p>}
      <button type="submit" disabled={busy || !!problem}>Create vault</button>
      {servers.length > 0 && (
        <div>
          <p className="muted">These servers are kept, with "Visible to AI" turned off; turn it back on in the host editor.</p>
          <ul>{servers.map((s) => <li key={s.name}>{s.name} <span className="muted">{`${s.user}@${s.host}:${s.port}`}</span></li>)}</ul>
        </div>
      )}
      {error && <p className="error">{error}</p>}
    </form>
  )
}
```

Create `desktop/src/renderer/HostEditor.tsx`:

```tsx
import { useState, type FormEvent } from 'react'
import type { SecretField, ServerInfo, ServerInput } from '../shared/protocol'
import { closesTabs, draftFrom, endpointChanged, SECRET_FIELDS, secretPlaceholder, toInput, type HostDraft } from './hostForm'

export function EditorWarnings({ server, draft, openTabs }: { server?: ServerInfo; draft: HostDraft; openTabs: number }) {
  return (
    <>
      {endpointChanged(server, draft) && (
        <p className="warn-text">Changing host or port forgets the host key and saved passwords unless you re-enter them.</p>
      )}
      {openTabs > 0 && closesTabs(server, draft) && (
        <p className="warn-text">{`Saving will close ${openTabs} open ${openTabs === 1 ? 'tab' : 'tabs'}.`}</p>
      )}
    </>
  )
}

// server undefined = a new host. Secrets are never shown: an empty field
// keeps the saved value, Clear removes it.
export function HostEditor({ server, openTabs, focusForget, onSave, onForget, onClose }: {
  server?: ServerInfo; openTabs: number; focusForget?: boolean
  onSave: (input: ServerInput, original?: string) => Promise<void>
  onForget: (name: string) => Promise<void>
  onClose: () => void
}) {
  const [draft, setDraft] = useState(() => draftFrom(server))
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const set = (patch: Partial<HostDraft>) => setDraft((d) => ({ ...d, ...patch }))
  const setSecret = (field: SecretField, value: string, cleared = false) =>
    setDraft((d) => ({ ...d, secrets: { ...d.secrets, [field]: { value, cleared } } }))
  const run = async (p: () => Promise<void>) => {
    setBusy(true); setError(undefined)
    try { await p() } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  const save = (e: FormEvent) => { e.preventDefault(); run(() => onSave(toInput(draft), server?.name)) }
  return (
    <div className="modal">
      <form className="dialog" role="dialog" aria-label="Host editor" onSubmit={save}>
        <h3>{server ? `Edit ${server.name}` : 'New host'}</h3>
        <label>Name<input value={draft.name} autoFocus={!focusForget} onChange={(e) => set({ name: e.target.value })} /></label>
        <label>Host<input value={draft.host} onChange={(e) => set({ host: e.target.value })} /></label>
        <label>Port<input value={draft.port} inputMode="numeric" onChange={(e) => set({ port: e.target.value })} /></label>
        <label>User<input value={draft.user} onChange={(e) => set({ user: e.target.value })} /></label>
        <label>Auth
          <select value={draft.auth} onChange={(e) => set({ auth: e.target.value })}>
            <option value="password">password</option>
            <option value="key">key</option>
            <option value="agent">agent</option>
          </select>
        </label>
        {draft.auth === 'key' && (
          <label>Key path<input value={draft.keyPath} onChange={(e) => set({ keyPath: e.target.value })} /></label>
        )}
        {SECRET_FIELDS.map(({ field, label, has }) => {
          const saved = !!server && has(server)
          const edit = draft.secrets[field]
          return (
            <div className="row" key={field}>
              <label>{label}<input type="password" autoComplete="off" value={edit.value}
                placeholder={secretPlaceholder(saved, edit)} onChange={(e) => setSecret(field, e.target.value)} /></label>
              {saved && !edit.cleared && <button type="button" className="link" onClick={() => setSecret(field, '', true)}>Clear</button>}
            </div>
          )
        })}
        <label className="check">
          <input type="checkbox" checked={draft.aiVisible} onChange={(e) => set({ aiVisible: e.target.checked })} />
          Visible to AI
        </label>
        {server && (
          <div>
            <div className="muted">Host key</div>
            {server.hostKey ? (
              <>
                <pre className="cmd">{`${server.hostKeyAlgo} ${server.hostKey}`.trim()}</pre>
                <button type="button" autoFocus={focusForget} disabled={busy} onClick={() => run(() => onForget(server.name))}>Forget host key</button>
              </>
            ) : (
              <p className="muted">Not pinned. You will be asked to confirm it on the next connect.</p>
            )}
          </div>
        )}
        <EditorWarnings server={server} draft={draft} openTabs={openTabs} />
        {error && <p className="error">{error}</p>}
        <div className="actions">
          <button type="submit" disabled={busy}>Save</button>
          <button type="button" onClick={onClose}>Close</button>
        </div>
      </form>
    </div>
  )
}
```

- [ ] **Step 5: Host list, tab count, and App wiring**

Replace the whole of `desktop/src/renderer/HostList.tsx` with:

```tsx
import type { ServerInfo } from '../shared/protocol'

export function HostList({ servers, storePath, onOpen, onNew, onEdit, onDelete }: {
  servers: ServerInfo[]; storePath: string
  onOpen: (name: string) => void; onNew: () => void; onEdit: (name: string) => void; onDelete: (name: string) => void
}) {
  return (
    <nav className="hosts">
      <h3>Servers</h3>
      <button className="new" onClick={onNew}>New host</button>
      <ul>
        {servers.map((s) => (
          <li key={s.name}>
            <button onClick={() => onOpen(s.name)} title={`${s.user}@${s.host}:${s.port}`}>
              {s.name}
            </button>
            {s.aiVisible && <span className="badge" title="Visible to AI">AI</span>}
            {!s.hostKey && <span className="badge warn" title="Host key not pinned yet">new</span>}
            <button className="small" aria-label={`Edit ${s.name}`} onClick={() => onEdit(s.name)}>Edit</button>
            <button className="small" aria-label={`Delete ${s.name}`} onClick={() => onDelete(s.name)}>Delete</button>
          </li>
        ))}
      </ul>
      <footer className="muted">Vault file: <code>{storePath}</code> — copy it to back up.</footer>
    </nav>
  )
}
```

In `desktop/src/renderer/terminals.ts`, add to `class TabSet` after `mostRecentFor`:

```ts
  openCount(server: string): number {
    return this.tabs.filter((t) => t.server === server && t.state !== 'exited').length
  }
```

In `desktop/src/renderer/TerminalTabs.tsx`, change `TerminalsHandle` to:

```ts
export interface TerminalsHandle {
  open(server: string): void
  sendToTab(server: string, text: string): Promise<void>
  openCount(server: string): number
}
```

and add to the `useImperativeHandle` object, after `sendToTab`:

```ts
    openCount: (server) => tabs.openCount(server),
```

Replace the whole of `desktop/src/renderer/App.tsx` with:

```tsx
import { useCallback, useEffect, useRef, useState } from 'react'
import type { HubState, ServerInfo, Status } from '../shared/protocol'
import { hub } from './transport'
import { screenFor } from './shell'
import { Unlock } from './Unlock'
import { CreateVault } from './CreateVault'
import { HostList } from './HostList'
import { HostEditor } from './HostEditor'
import { Terminals, type TerminalsHandle } from './TerminalTabs'
import { ApprovalPanel } from './ApprovalPanel'
import { Latest, mergeSeed, reduceApprovals, type PendingItem } from './approvals'

export function App() {
  const [hubState, setHubState] = useState<HubState>({ kind: 'starting' })
  const [status, setStatus] = useState<Status>()
  const [unlockError, setUnlockError] = useState<string>()
  const [idleLocked, setIdleLocked] = useState(false)
  const [servers, setServers] = useState<ServerInfo[]>([])
  const terms = useRef<TerminalsHandle>(null)
  const [everReady, setEverReady] = useState(false)
  const [editing, setEditing] = useState<{ name?: string; focusForget?: boolean }>()

  const refresh = useCallback(async () => {
    try { setStatus(await hub.status()) } catch { setStatus(undefined) }
  }, [])

  useEffect(() => {
    hub.getState().then(setHubState).catch(() => {})
    const offState = hub.onState(setHubState)
    const offEvent = hub.onEvent((e) => {
      if (e.method === 'locked') { setIdleLocked(e.params?.reason === 'idle'); refresh() }
    })
    return () => { offState(); offEvent() }
  }, [refresh])

  useEffect(() => {
    if (hubState.kind === 'running') refresh()
    else { setStatus(undefined); setUnlockError(undefined); setIdleLocked(false) }
  }, [hubState, refresh])

  const screen = screenFor(hubState, status, unlockError)

  // One fetch per screen change or host edit, never a poll: every call but
  // status counts as UI activity and would hold off the idle lock.
  const reloadServers = useCallback(() => hub.servers().then(setServers).catch(() => setServers([])), [])
  useEffect(() => {
    if (screen.kind === 'ready' || screen.kind === 'create-vault') reloadServers()
    else setServers([])
  }, [screen.kind, reloadServers])

  const [items, setItems] = useState<PendingItem[]>([])
  const [seedError, setSeedError] = useState<string>()
  const decidedSince = useRef(new Set<string>())
  const pendingSince = useRef(new Set<string>())
  const seedGen = useRef(new Latest())
  useEffect(() => hub.onEvent((e) => {
    // Only approval events can change items. A no-op setItems still queues an update
    // (holding e) until App next renders, so calling it per term.data leaks every chunk.
    if (e.method !== 'pending' && e.method !== 'decided') return
    if (e.method === 'decided') decidedSince.current.add(e.params.request.id)
    else pendingSince.current.add(e.params.request.id)
    setItems((cur) => reduceApprovals(cur, e, Date.now()))
  }), [])
  useEffect(() => {
    const gen = seedGen.current.next() // a reply to any earlier pending() call is now stale
    if (screen.kind === 'ready') {
      decidedSince.current = new Set()
      pendingSince.current = new Set()
      hub.pending()
        .then((p) => {
          if (!seedGen.current.isCurrent(gen)) return
          setItems((cur) => mergeSeed(cur, p, decidedSince.current, pendingSince.current, Date.now())); setSeedError(undefined)
        })
        .catch((e) => { if (seedGen.current.isCurrent(gen)) setSeedError((e as Error).message) })
    }
    if (hubState.kind !== 'running') { setItems([]); setSeedError(undefined) }
  }, [screen.kind, hubState.kind])

  const unlock = async (pw: string) => {
    try { await hub.unlock(pw); setUnlockError(undefined); setIdleLocked(false) } catch (e) { setUnlockError((e as Error).message) }
    await refresh()
  }
  const createVault = async (pw: string) => { await hub.createVault(pw); await refresh() }
  const deleteHost = async (name: string) => {
    if (!window.confirm(`Delete ${name}? Its saved passwords and host key are removed and its open tabs close.`)) return
    try { await hub.deleteServer(name) } catch (e) { window.alert((e as Error).message) }
    await reloadServers()
  }

  const ready = screen.kind === 'ready'
  useEffect(() => { if (ready) setEverReady(true) }, [ready])

  // Once shown, terminals stay mounted (hidden and inert) through lock and hub restarts,
  // so their SSH sessions survive a lock and a restart can end them with Reconnect.
  return (
    <div className={ready ? 'layout' : 'app'}>
      {ready ? (
        <>
          <header>
            <span>ssh-mcp</span>
            <button onClick={async () => { try { await hub.lock(); setUnlockError(undefined); await refresh() } catch { /* the locked/hub-state events recover the UI */ } }}>Lock</button>
          </header>
          <HostList servers={servers} storePath={status?.storePath ?? ''} onOpen={(name) => terms.current?.open(name)}
            onNew={() => setEditing({})}
            onEdit={async (name) => { await reloadServers(); setEditing({ name }) }}
            onDelete={deleteHost} />
        </>
      ) : screen.kind === 'hub' ? (
        <HubScreen state={screen.state} />
      ) : screen.kind === 'create-vault' ? (
        <div className="center"><CreateVault servers={servers} onCreate={createVault} /></div>
      ) : (
        <div className="center">
          {idleLocked && <p className="muted">Locked after inactivity.</p>}
          <Unlock onUnlock={unlock} error={screen.error} />
        </div>
      )}
      {(ready || everReady) && (
        <main className="work" style={ready ? undefined : { display: 'none' }} inert={!ready}><Terminals ref={terms} /></main>
      )}
      {ready && (
        <ApprovalPanel items={items} seedError={seedError}
          onDecide={(id, outcome, reason) => hub.decide(id, outcome, reason)}
          onDenyAll={() => hub.denyAll('denied all by user')}
          onSendToTab={async (item) => {
            await terms.current!.sendToTab(item.request.server, item.request.command)
            await hub.decide(item.request.id, 'sent_to_tab')
          }} />
      )}
      {ready && editing && (
        <HostEditor key={editing.name ?? ''} server={servers.find((s) => s.name === editing.name)}
          openTabs={editing.name ? terms.current?.openCount(editing.name) ?? 0 : 0} focusForget={editing.focusForget}
          onSave={async (input, original) => { await hub.saveServer(input, original); await reloadServers(); setEditing(undefined) }}
          onForget={async (name) => { await hub.forgetHostKey(name); await reloadServers() }}
          onClose={() => setEditing(undefined)} />
      )}
    </div>
  )
}

function HubScreen({ state }: { state: HubState }) {
  if (state.kind === 'failed') {
    return (
      <div className="center">
        <h2>The hub stopped</h2>
        <p>{state.message}</p>
        <pre className="stderr">{state.stderr}</pre>
      </div>
    )
  }
  if (state.kind === 'restarting') {
    return <div className="center"><p>Hub crashed; restarting in {Math.round(state.inMs / 1000)} s (attempt {state.attempt}).</p></div>
  }
  return <div className="center"><p>Starting the hub…</p></div>
}
```

Append to `desktop/src/renderer/styles.css`:

```css
.hosts .new, .hosts button.small { flex: none; }
.hosts .new { margin: 0 8px 4px; padding: 4px 8px; border: 1px solid #444; }
.hosts button.small { font-size: 11px; color: #999; padding: 2px 4px; }
.hosts footer { padding: 8px; word-break: break-all; }
.modal { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.5); display: flex; align-items: center; justify-content: center; z-index: 10; }
.dialog { background: #252526; border: 1px solid #444; border-radius: 4px; padding: 12px 16px; width: 460px; max-height: 90%; overflow: auto; display: flex; flex-direction: column; gap: 6px; }
.dialog label { display: flex; flex-direction: column; gap: 2px; font-size: 12px; }
.dialog label.check { flex-direction: row; align-items: center; gap: 6px; }
.dialog .row { display: flex; gap: 6px; align-items: flex-end; }
.dialog .row label { flex: 1; }
.link { background: none; border: 0; color: #3794ff; cursor: pointer; padding: 0; font-size: 12px; }
.warn-text { color: #cca700; font-size: 12px; }
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd desktop && npm run typecheck && npm test`
Expected: typecheck clean; all Vitest files pass, including `shell.test.ts`, `hostForm.test.ts`, `terminals.test.ts`.

- [ ] **Step 7: Commit**

```bash
git add desktop/src desktop/test
git commit -m "$(cat <<'EOF'
feat(desktop): create the vault and add, edit, and delete hosts in the app

A store without a master password opens at Create vault. The host list
gains New, Edit, Delete and the vault file path. The editor never shows a
secret: empty keeps it, Clear removes it. It warns before a host or port
change drops the pin and passwords, and before a save closes open tabs.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
)"
```

---

### Task 10: Host-key prompt queue and mismatch dialog

**Files:**
- Create: `desktop/src/renderer/hostkeys.ts`, `desktop/src/renderer/HostKeyDialog.tsx`, `desktop/test/hostkeys.test.ts`
- Modify: `desktop/src/renderer/TermView.tsx` (whole file), `desktop/src/renderer/TerminalTabs.tsx` (whole file), `desktop/src/renderer/App.tsx` (imports, state, `Terminals` props, two dialogs), `desktop/src/renderer/styles.css` (append)

**Interfaces:**
- Consumes: `HostKeyUnknown`, `HostKeyMismatch`, `hub.termOpen(..., trustHostKey)` (Task 8); `ListChanges`, `ALLOW_DELAY_MS`, `blockKeyboardActivation` (`approvals.ts`); App's `editing` state and `reloadServers` (Task 9).
- Produces:
  - `class HostKeyPrompts { ask(id, info): Promise<boolean>; get current(): Prompt | undefined; answer(trust: boolean): void; drop(id): void; subscribe(cb): () => void }`; `interface Prompt { id; info: HostKeyUnknown; resolve }`; `promptKey(p?)`; `trustEnabled(changedAt, now)`
  - `HostKeyPromptView({ info, trustEnabled, onTrust, onCancel })`, `HostKeyDialog({ prompts })`, `HostKeyMismatchDialog({ info, onClose, onEdit })`
  - `TermView` props `hostKeys`, `onMismatch`, `onTrusted`; `Terminals` props `{ hostKeys, onMismatch, onTrusted }`

This task ends the Task 5–9 gap where a `hostKeyUnknown` result counted as an open.

- [ ] **Step 1: Write the failing tests**

Create `desktop/test/hostkeys.test.ts`:

```ts
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { HostKeyPrompts, promptKey, trustEnabled } from '../src/renderer/hostkeys'
import { HostKeyPromptView } from '../src/renderer/HostKeyDialog'
import { ListChanges } from '../src/renderer/approvals'
import type { HostKeyUnknown } from '../src/shared/protocol'

const info = (fingerprint: string, knownHosts: HostKeyUnknown['knownHosts'] = 'absent'): HostKeyUnknown => ({
  status: 'hostKeyUnknown', server: 'box', host: 'h', port: 22, user: 'u', fingerprint, keyType: 'ssh-ed25519', knownHosts,
})

describe('HostKeyPrompts', () => {
  it('shows prompts one at a time in arrival order; a closed tab drops its own', async () => {
    const q = new HostKeyPrompts()
    const seen = vi.fn()
    q.subscribe(seen)
    const a = q.ask('t1', info('SHA256:a'))
    const b = q.ask('t2', info('SHA256:b'))
    expect(q.current?.id).toBe('t1')
    q.answer(true)
    await expect(a).resolves.toBe(true)
    expect(q.current?.id).toBe('t2')
    q.drop('t2')
    await expect(b).resolves.toBe(false)
    expect(q.current).toBeUndefined()
    expect(seen).toHaveBeenCalledTimes(4)
  })
})

describe('Trust delay', () => {
  it('lasts 500 ms and restarts whenever the prompt shown changes', () => {
    const q = new HostKeyPrompts()
    q.ask('t1', info('SHA256:a'))
    q.ask('t2', info('SHA256:b'))
    const c = new ListChanges(promptKey(q.current), 0)
    expect(trustEnabled(c.at, 499)).toBe(false)
    expect(trustEnabled(c.at, 500)).toBe(true)
    q.answer(false)
    c.setKey(promptKey(q.current), 600)
    expect(trustEnabled(c.at, 1000)).toBe(false)
    expect(trustEnabled(c.at, 1100)).toBe(true)
  })
})

describe('HostKeyPromptView', () => {
  it('makes Cancel the only Enter target and Trust mouse-only', () => {
    const html = renderToStaticMarkup(createElement(HostKeyPromptView,
      { info: info('SHA256:abc'), trustEnabled: false, onTrust: () => {}, onCancel: () => {} }))
    expect(html).toContain('SHA256:abc')
    expect(html).toContain('u@h:22')
    expect(html).toContain('ssh-ed25519')
    const buttons = [...html.matchAll(/<button([^>]*)>([^<]*)<\/button>/g)].map((m) => ({ attrs: m[1], text: m[2] }))
    expect(buttons.filter((b) => /type="submit"/.test(b.attrs)).map((b) => b.text)).toEqual(['Cancel'])
    const trust = buttons.find((b) => b.text === 'Trust')!
    expect(trust.attrs).toMatch(/type="button"/)
    expect(trust.attrs).toMatch(/tabindex="-1"/)
    expect(trust.attrs).toMatch(/disabled=""/)
  })
  it('warns in the mismatch style when known_hosts lists a different key', () => {
    const html = renderToStaticMarkup(createElement(HostKeyPromptView,
      { info: info('SHA256:abc', 'different'), trustEnabled: true, onTrust: () => {}, onCancel: () => {} }))
    expect(html).toContain('class="mismatch-text"')
    expect(html).toContain('lists a different key')
  })
})
```

Run: `cd desktop && npm test`
Expected: FAIL: `Failed to resolve import "../src/renderer/hostkeys"`.

- [ ] **Step 2: The prompt queue**

Create `desktop/src/renderer/hostkeys.ts`:

```ts
import type { HostKeyUnknown } from '../shared/protocol'
import { ALLOW_DELAY_MS } from './approvals'

export interface Prompt { id: string; info: HostKeyUnknown; resolve: (trust: boolean) => void }

// Host-key prompts from every tab, shown one at a time in arrival order.
export class HostKeyPrompts {
  private q: Prompt[] = []
  private subs = new Set<() => void>()
  ask(id: string, info: HostKeyUnknown): Promise<boolean> {
    return new Promise((resolve) => { this.q.push({ id, info, resolve }); this.changed() })
  }
  get current(): Prompt | undefined { return this.q[0] }
  answer(trust: boolean): void {
    const p = this.q.shift()
    if (p) { p.resolve(trust); this.changed() }
  }
  // A tab closed while waiting: its prompt goes away unanswered.
  drop(id: string): void {
    const i = this.q.findIndex((p) => p.id === id)
    if (i < 0) return
    this.q.splice(i, 1)[0].resolve(false)
    this.changed()
  }
  subscribe(cb: () => void): () => void {
    this.subs.add(cb)
    return () => { this.subs.delete(cb) }
  }
  private changed() { for (const cb of [...this.subs]) cb() }
}

// Identifies what the dialog shows; a change restarts the Trust delay.
export const promptKey = (p?: Prompt) => (p ? `${p.id}|${p.info.fingerprint}` : '')

// Trust stays disabled for ALLOW_DELAY_MS after the dialog's content last
// changed: a queued prompt can replace the dialog under the cursor, the same
// hazard as the approval list shifting.
export const trustEnabled = (changedAt: number, now: number) => now - changedAt >= ALLOW_DELAY_MS
```

- [ ] **Step 3: The dialogs**

Create `desktop/src/renderer/HostKeyDialog.tsx`:

```tsx
import { useEffect, useReducer, useRef, useState } from 'react'
import type { HostKeyMismatch, HostKeyUnknown } from '../shared/protocol'
import { ALLOW_DELAY_MS, blockKeyboardActivation, ListChanges } from './approvals'
import { promptKey, trustEnabled, type HostKeyPrompts } from './hostkeys'

const KNOWN_HOSTS_TEXT = {
  match: 'Your ~/.ssh/known_hosts lists this same key for this host.',
  absent: 'This host is not in your ~/.ssh/known_hosts.',
  different: 'Warning: your ~/.ssh/known_hosts lists a different key for this host. The connection may be intercepted.',
}

// Cancel is the default (Enter) and has focus; Trust is mouse-only.
export function HostKeyPromptView({ info, trustEnabled: enabled, onTrust, onCancel }: {
  info: HostKeyUnknown; trustEnabled: boolean; onTrust: () => void; onCancel: () => void
}) {
  return (
    <div className="modal" role="dialog" aria-label="Unknown host key">
      <form className="dialog" onSubmit={(e) => { e.preventDefault(); onCancel() }}>
        <h3>Trust this host key?</h3>
        <p><strong>{info.server}</strong> <span className="muted">{`${info.user}@${info.host}:${info.port}`}</span></p>
        <p className="muted">{info.keyType}</p>
        <pre className="cmd">{info.fingerprint}</pre>
        <p className={info.knownHosts === 'different' ? 'mismatch-text' : 'muted'}>{KNOWN_HOSTS_TEXT[info.knownHosts]}</p>
        <p className="muted">Trust only if this matches the key you expect for this server.</p>
        <div className="actions">
          <button type="submit" autoFocus>Cancel</button>
          <button type="button" className="allow" tabIndex={-1} onKeyDown={blockKeyboardActivation}
            disabled={!enabled} onClick={onTrust}>Trust</button>
        </div>
      </form>
    </div>
  )
}

export function HostKeyDialog({ prompts }: { prompts: HostKeyPrompts }) {
  const [, rerender] = useReducer((n: number) => n + 1, 0)
  useEffect(() => prompts.subscribe(rerender), [prompts])
  const p = prompts.current
  const changes = useRef<ListChanges>(null)
  changes.current ??= new ListChanges(promptKey(p), Date.now())
  changes.current.setKey(promptKey(p), Date.now())
  const at = changes.current.at
  const [now, setNow] = useState(Date.now())
  const enabled = trustEnabled(at, now)
  useEffect(() => {
    if (!p || enabled) return
    const t = setTimeout(() => setNow(Date.now()), Math.max(0, at + ALLOW_DELAY_MS - Date.now()) + 10)
    return () => clearTimeout(t)
  }, [p, enabled, at])
  if (!p) return null
  // Keyed per prompt so Cancel takes focus again when the next one shows.
  return <HostKeyPromptView key={promptKey(p)} info={p.info} trustEnabled={enabled}
    onTrust={() => prompts.answer(true)} onCancel={() => prompts.answer(false)} />
}

// A changed key is refused outright; the only way forward is Forget in the
// host editor, never a one-click re-pin here.
export function HostKeyMismatchDialog({ info, onClose, onEdit }: { info: HostKeyMismatch; onClose: () => void; onEdit: () => void }) {
  return (
    <div className="modal" role="dialog" aria-label="Host key mismatch">
      <div className="dialog mismatch">
        <h3>Host key changed</h3>
        <p><strong>{info.server}</strong> <span className="muted">{`${info.user}@${info.host}:${info.port}`}</span></p>
        <p className="mismatch-text">The server presented a different host key than the one pinned. The connection may be intercepted. Nothing was sent.</p>
        <p className="muted">Pinned</p>
        <pre className="cmd">{info.pinned}</pre>
        <p className="muted">Presented</p>
        <pre className="cmd">{info.presented}</pre>
        <p className="muted">If you know the key changed on purpose, forget the old key in the host editor and connect again.</p>
        <div className="actions">
          <button autoFocus onClick={onClose}>Close</button>
          <button onClick={onEdit}>Open host editor</button>
        </div>
      </div>
    </div>
  )
}
```

Append to `desktop/src/renderer/styles.css`:

```css
.dialog.mismatch { border-color: #f48771; }
.mismatch-text { color: #f48771; }
```

- [ ] **Step 4: Ask, retry, and refuse in `TermView`**

Replace the whole of `desktop/src/renderer/TermView.tsx` with:

```tsx
import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { hub, fromBase64 } from './transport'
import type { HostKeyMismatch, HubEvent } from '../shared/protocol'
import type { HostKeyPrompts } from './hostkeys'
import { clipboardKey, Debouncer, isUserInput, printable, type Dispatcher, type Tab, type TabSet } from './terminals'

export interface TermApi { paste(text: string): void }
// A hub event for this tab's id, or 'hub.stopped' when the hub leaves the running state.
export type TermEvent = HubEvent | { method: 'hub.stopped' }

export function TermView({ tab, tabs, events, visible, onChange, register, hostKeys, onMismatch, onTrusted }: {
  tab: Tab; tabs: TabSet; events: Dispatcher<TermEvent>; visible: boolean; onChange: () => void
  register: (id: string, api: TermApi | undefined) => void
  hostKeys: HostKeyPrompts; onMismatch: (m: HostKeyMismatch) => void; onTrusted: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal>(undefined)
  const visibleRef = useRef(visible)
  visibleRef.current = visible

  useEffect(() => { if (visible) termRef.current?.focus() }, [visible])

  useEffect(() => {
    const el = ref.current!
    const term = new Terminal({ convertEol: false, fontFamily: 'Menlo, Consolas, monospace', fontSize: 13 })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(el)
    fit.fit()
    termRef.current = term
    // Copy runs xterm's own copy handler; paste is left to Blink, whose paste event
    // xterm handles as a paste (bracketed). Neither sends the key itself to the shell.
    term.attachCustomKeyEventHandler((e) => {
      const a = clipboardKey(e, navigator.platform)
      if (a === 'copy' && e.type === 'keydown') document.execCommand('copy')
      return a === 'pass'
    })
    const enc = new TextEncoder()
    // Nothing is sent for this id before the term.open reply, nor after it ends.
    let phase: 'opening' | 'open' | 'ended' = 'opening'
    let disposed = false
    let heldAck = 0 // bytes rendered before the open reply; acked once it arrives
    let sent = { rows: term.rows, cols: term.cols }

    const end = (reason: string) => {
      if (phase === 'ended') return
      phase = 'ended'
      hostKeys.drop(tab.id) // a prompt for a dead tab must not stay queued
      reason = printable(reason)
      term.write(`\r\n[exited: ${reason}]\r\n`)
      tabs.exited(tab.id, reason)
      onChange()
    }

    register(tab.id, { paste: (text) => term.paste(text) })

    // An unknown host key goes to the user; each Trust retries once, with the
    // same id, pinned to exactly the confirmed key. A changed key is refused.
    const open = async (): Promise<void> => {
      let r = await hub.termOpen(tab.id, tab.server, sent.rows, sent.cols)
      let trusted = false
      while (r.status === 'hostKeyUnknown') {
        if (!(await hostKeys.ask(tab.id, r)) || disposed || phase !== 'opening') throw new Error('host key not trusted')
        r = await hub.termOpen(tab.id, tab.server, sent.rows, sent.cols, { fingerprint: r.fingerprint, keyType: r.keyType })
        trusted = true
      }
      if (r.status === 'hostKeyMismatch') {
        onMismatch(r)
        throw new Error('host key mismatch')
      }
      if (trusted) onTrusted()
    }
    const opening = open()
    opening.then(
      () => {
        if (disposed || phase !== 'opening') return
        phase = 'open'
        if (heldAck) { hub.termAck(tab.id, heldAck); heldAck = 0 }
        if (term.rows !== sent.rows || term.cols !== sent.cols) {
          sent = { rows: term.rows, cols: term.cols }
          hub.termResize(tab.id, sent.rows, sent.cols)
        }
        tabs.opened(tab.id)
        onChange()
        if (visibleRef.current) term.focus()
      },
      (e) => {
        // A timed-out open may still finish in the hub later; close it so it is not orphaned.
        hub.termClose(tab.id).catch(() => {})
        if (!disposed) end(`open failed: ${(e as Error).message}`)
      },
    )

    // Real user input in this terminal; capture phase, since xterm stops some events.
    let lastInputAt = -Infinity
    const input = () => { lastInputAt = Date.now() }
    const inputEvents = ['keydown', 'paste', 'compositionend', 'mousedown'] as const
    for (const t of inputEvents) el.addEventListener(t, input, true)
    const dataSub = term.onData((s) => {
      if (phase === 'open') hub.termWrite(tab.id, enc.encode(s), isUserInput(lastInputAt, Date.now()))
    })
    const off = events.on(tab.id, (e) => {
      if (phase === 'ended') return
      if (e.method === 'hub.stopped') {
        end('hub restarted')
      } else if (e.method === 'term.data') {
        const bytes = fromBase64(e.params.data)
        term.write(bytes, () => {
          if (disposed || phase === 'ended') return
          if (phase === 'open') hub.termAck(tab.id, bytes.length)
          else heldAck += bytes.length
        })
        if (!tab.sawOutput) { tabs.output(tab.id); onChange() }
      } else if (e.method === 'term.exit') {
        end(e.params.reason || `code ${e.params.code}`)
      } else if (e.method === 'term.dropped') {
        term.write(`\r\n[input dropped: ${e.params.bytes} bytes]\r\n`)
      }
    })
    const resize = new Debouncer(50, () => {
      if (!el.clientHeight) return // hidden tab: fit would shrink the remote pty to its minimum
      fit.fit()
      if (phase === 'open' && (term.rows !== sent.rows || term.cols !== sent.cols)) {
        sent = { rows: term.rows, cols: term.cols }
        hub.termResize(tab.id, sent.rows, sent.cols)
      }
    })
    const ro = new ResizeObserver(() => resize.poke())
    ro.observe(el)

    return () => {
      disposed = true
      hostKeys.drop(tab.id)
      ro.disconnect(); resize.cancel(); dataSub.dispose(); off()
      for (const t of inputEvents) el.removeEventListener(t, input, true)
      register(tab.id, undefined)
      // Closing while opening waits for the reply; an ended session needs no close.
      if (phase !== 'ended') opening.then(() => hub.termClose(tab.id)).catch(() => {})
      termRef.current = undefined
      term.dispose()
    }
    // tab identity is fixed for the life of this component
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return <div className="term" ref={ref} style={{ display: visible ? 'block' : 'none' }} />
}
```

Replace the whole of `desktop/src/renderer/TerminalTabs.tsx` with:

```tsx
import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react'
import type { HostKeyMismatch } from '../shared/protocol'
import { hub } from './transport'
import type { HostKeyPrompts } from './hostkeys'
import { Dispatcher, TabSet } from './terminals'
import { TermView, type TermApi, type TermEvent } from './TermView'

export interface TerminalsHandle {
  open(server: string): void
  sendToTab(server: string, text: string): Promise<void>
  openCount(server: string): number
}

export const Terminals = forwardRef<TerminalsHandle, {
  hostKeys: HostKeyPrompts; onMismatch: (m: HostKeyMismatch) => void; onTrusted: () => void
}>(function Terminals({ hostKeys, onMismatch, onTrusted }, ref) {
  const tabs = useRef(new TabSet()).current
  const apis = useRef(new Map<string, TermApi>()).current
  const events = useRef(new Dispatcher<TermEvent>()).current
  useEffect(() => {
    const offEvent = hub.onEvent((e) => {
      const id = (e.params as { id?: unknown } | undefined)?.id
      if (e.method.startsWith('term.') && typeof id === 'string') events.emit(id, e)
    })
    const offState = hub.onState((s) => { if (s.kind !== 'running') events.emitAll({ method: 'hub.stopped' }) })
    return () => { offEvent(); offState() }
  }, [events])
  const [, setVersion] = useState(0)
  const changed = () => setVersion((v) => v + 1)
  const register = (id: string, api: TermApi | undefined) => { if (api) apis.set(id, api); else apis.delete(id) }

  const waitReady = (id: string) => new Promise<void>((resolve, reject) => {
    const started = Date.now()
    const check = () => {
      const t = tabs.tabs.find((x) => x.id === id)
      if (!t || t.state === 'exited') return reject(new Error('terminal closed'))
      if (t.state === 'open' && t.sawOutput && apis.has(id)) return resolve()
      if (Date.now() - started > 15000) return reject(new Error('terminal did not become ready'))
      setTimeout(check, 50)
    }
    check()
  })

  useImperativeHandle(ref, () => ({
    open(server) { tabs.open(server); changed() },
    async sendToTab(server, text) {
      let tab = tabs.mostRecentFor(server)
      if (!tab) tab = tabs.open(server)
      tabs.activate(tab.id); changed()
      await waitReady(tab.id)
      apis.get(tab.id)!.paste(text)
    },
    openCount: (server) => tabs.openCount(server),
  }))

  return (
    <div className="terms">
      <div className="tabbar">
        {tabs.tabs.map((t) => (
          <div key={t.id} className={'tab' + (t.id === tabs.active ? ' active' : '')}>
            <button onClick={() => { tabs.activate(t.id); changed() }}>
              {t.server}{t.state === 'exited' ? ' (exited)' : ''}
            </button>
            {t.state === 'exited' && (
              <button onClick={() => { tabs.close(t.id); tabs.open(t.server); changed() }}>Reconnect</button>
            )}
            <button title="Close" aria-label="Close" onClick={() => { tabs.close(t.id); changed() }}>×</button>
          </div>
        ))}
      </div>
      <div className="termarea">
        {tabs.tabs.map((t) => (
          <TermView key={t.id} tab={t} tabs={tabs} events={events} visible={t.id === tabs.active} onChange={changed} register={register}
            hostKeys={hostKeys} onMismatch={onMismatch} onTrusted={onTrusted} />
        ))}
      </div>
    </div>
  )
})
```

- [ ] **Step 5: Wire the dialogs into `App.tsx`**

In `desktop/src/renderer/App.tsx` (the Task 9 version):

1. Change the protocol import to `import type { HostKeyMismatch, HubState, ServerInfo, Status } from '../shared/protocol'` and add after `import { HostEditor } from './HostEditor'`:

```tsx
import { HostKeyDialog, HostKeyMismatchDialog } from './HostKeyDialog'
import { HostKeyPrompts } from './hostkeys'
```

2. Add after `const [editing, setEditing] = ...`:

```tsx
  const hostKeys = useRef(new HostKeyPrompts()).current
  const [mismatch, setMismatch] = useState<HostKeyMismatch>()
```

3. Change `<Terminals ref={terms} />` to:

```tsx
<Terminals ref={terms} hostKeys={hostKeys} onMismatch={setMismatch} onTrusted={reloadServers} />
```

4. Add just before the closing `</div>` of the returned layout (after the `HostEditor` block):

```tsx
      {ready && <HostKeyDialog prompts={hostKeys} />}
      {ready && mismatch && (
        <HostKeyMismatchDialog info={mismatch} onClose={() => setMismatch(undefined)}
          onEdit={async () => { const name = mismatch.server; setMismatch(undefined); await reloadServers(); setEditing({ name, focusForget: true }) }} />
      )}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd desktop && npm run typecheck && npm test`
Expected: typecheck clean; all Vitest files pass, including `hostkeys.test.ts`.

- [ ] **Step 7: Commit**

```bash
git add desktop/src desktop/test
git commit -m "$(cat <<'EOF'
feat(desktop): confirm host keys by fingerprint; refuse changed keys

term.open's hostKeyUnknown result shows the fingerprint, key type, and a
known_hosts hint. Prompts from all tabs queue; Cancel is the default and
Trust is mouse-only and disabled for 500 ms after any change. Trust retries
the same id pinned to that key. A mismatch shows both fingerprints and can
only lead to Forget in the host editor.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
)"
```

---

### Task 11: Playwright `hosts.spec.ts`

**Files:**
- Modify: `desktop/e2e/launch.ts` (whole file)
- Create: `desktop/e2e/hosts.spec.ts`

**Interfaces:**
- Consumes: every renderer label and role from Tasks 9–10: "New master password", "Confirm master password", button "Create vault", button "New host", dialog "Host editor" with labels Name/Host/Port/User/Password, button "Save", button "Edit box", dialog "Unknown host key" with buttons Trust/Cancel, button "Forget host key", button "Close", text "Not pinned".
- Produces: `launch(extraEnv?, opts?: { emptyStore?: boolean }): Promise<Launched>`; `Launched.port` (sshtestd's port).

- [ ] **Step 1: Let `launch` start from an empty store and report the sshd port**

Replace the whole of `desktop/e2e/launch.ts` with:

```ts
import { _electron as electron, type ElectronApplication, type Page } from '@playwright/test'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import * as fs from 'node:fs'
import * as path from 'node:path'
import * as readline from 'node:readline'

const repo = path.resolve(__dirname, '..', '..')

export interface Launched { app: ElectronApplication; tmp: string; socket: string; port: number; close(): Promise<void> }

// launch builds ssh-mcp and sshtestd into <tmp>/bin, starts sshtestd, and
// launches the app against <tmp>/store/servers.json. By default sshtestd
// writes a ready vault there (password "pw", server "box"), as smoke.spec.ts
// does; emptyStore leaves no store, so the app opens at Create vault. port is
// sshtestd's. extraEnv is added to the app's environment.
export async function launch(extraEnv: Record<string, string> = {}, opts: { emptyStore?: boolean } = {}): Promise<Launched> {
  const tmp = fs.mkdtempSync('/tmp/sme')
  // Binaries go in bin/: the hub's socket directory is <tmp>/ssh-mcp.
  const bin = path.join(tmp, 'bin')
  execFileSync('go', ['build', '-o', path.join(bin, 'ssh-mcp'), './cmd/ssh-mcp'], { cwd: repo, stdio: 'inherit' })
  execFileSync('go', ['build', '-o', path.join(bin, 'sshtestd'), './internal/sshx/sshtest/sshtestd'], { cwd: repo, stdio: 'inherit' })
  const store = path.join(tmp, 'store', 'servers.json')
  const args = opts.emptyStore ? [] : [`-write-store=${store}`, '-password=pw']
  const sshd: ChildProcess = spawn(path.join(bin, 'sshtestd'), args, { stdio: ['pipe', 'pipe', 'inherit'] })
  // sshtestd's first line is PORT=<port>.
  const port = await new Promise<number>((resolve) =>
    readline.createInterface({ input: sshd.stdout! }).once('line', (l) => resolve(Number(l.replace('PORT=', '')))))
  const app = await electron.launch({
    args: ['.'],
    cwd: path.resolve(__dirname, '..'),
    env: { ...process.env, SSH_MCP_BIN: path.join(bin, 'ssh-mcp'), SSH_MCP_STORE: store, SSH_MCP_RUNTIME_DIR: tmp, ...extraEnv },
  })
  return {
    app, tmp, socket: path.join(tmp, 'ssh-mcp', 'hub.sock'), port,
    async close() {
      await app.close()
      sshd.stdin?.end()
      sshd.kill()
      fs.rmSync(tmp, { recursive: true, force: true })
    },
  }
}

export async function unlock(win: Page): Promise<void> {
  await win.getByLabel('Master password').fill('pw')
  await win.getByRole('button', { name: 'Unlock' }).click()
}

export async function openBox(win: Page): Promise<void> {
  await win.locator('nav.hosts').getByRole('button', { name: 'box', exact: true }).click()
  await win.locator('.xterm').click()
}
```

- [ ] **Step 2: Write the spec**

Create `desktop/e2e/hosts.spec.ts`:

```ts
import { test, expect } from '@playwright/test'
import { launch, type Launched } from './launch'

// Spec 2a §Testing: vault, host CRUD, and the host-key prompt, in order.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')

let l: Launched
test.beforeAll(async () => { l = await launch({}, { emptyStore: true }) })
test.afterAll(async () => { await l?.close() })

test('create a vault, add a host, trust its key, edit the port, forget the key', async () => {
  const win = await l.app.firstWindow()
  const hosts = win.locator('nav.hosts')
  const editor = win.getByRole('dialog', { name: 'Host editor' })
  const prompt = win.getByRole('dialog', { name: 'Unknown host key' })
  const openBox = () => hosts.getByRole('button', { name: 'box', exact: true }).click()
  const editBox = () => hosts.getByRole('button', { name: 'Edit box' }).click()
  const trust = async () => {
    await expect(prompt).toContainText('SHA256:')
    await expect(prompt).toContainText(`test@127.0.0.1:${l.port}`)
    const button = prompt.getByRole('button', { name: 'Trust' })
    await expect(button).toBeEnabled({ timeout: 2000 })
    await button.click()
    await expect(prompt).toBeHidden()
  }

  // 1. Create a vault on an empty store.
  await win.getByLabel('New master password').fill('password1')
  await win.getByLabel('Confirm master password').fill('password1')
  await win.getByRole('button', { name: 'Create vault' }).click()
  await expect(hosts).toContainText('Vault file:')

  // 2. Add a host pointing at sshtestd.
  await hosts.getByRole('button', { name: 'New host' }).click()
  await editor.getByLabel('Name', { exact: true }).fill('box')
  await editor.getByLabel('Host', { exact: true }).fill('127.0.0.1')
  await editor.getByLabel('Port', { exact: true }).fill(String(l.port))
  await editor.getByLabel('User', { exact: true }).fill('test')
  await editor.getByLabel('Password', { exact: true }).fill('testpass')
  await editor.getByRole('button', { name: 'Save' }).click()
  await expect(editor).toBeHidden()

  // 3. Connect: the prompt shows the fingerprint; Trust gives a shell.
  await openBox()
  await trust()
  await win.locator('.xterm').click()
  await win.keyboard.type('echo hosts-ok')
  await expect(win.locator('.xterm-rows')).toContainText('echo hosts-ok')

  // 4. Edit the port: the editor warns, and saving closes the tab.
  await editBox()
  await editor.getByLabel('Port', { exact: true }).fill(String(l.port + 1))
  await expect(editor).toContainText('Changing host or port forgets the host key and saved passwords unless you re-enter them.')
  await expect(editor).toContainText('Saving will close 1 open tab.')
  await editor.getByRole('button', { name: 'Save' }).click()
  await expect(win.locator('.tabbar .tab').first()).toContainText('(exited)')
  // Back to the real port: the pin went with the old endpoint, so connecting asks again.
  await editBox()
  await editor.getByLabel('Port', { exact: true }).fill(String(l.port))
  await editor.getByRole('button', { name: 'Save' }).click()
  await expect(editor).toBeHidden()
  await openBox()
  await trust()

  // 5. Forget the key; reconnecting shows the prompt again.
  await editBox()
  await expect(editor).toContainText('SHA256:')
  await editor.getByRole('button', { name: 'Forget host key' }).click()
  await expect(editor).toContainText('Not pinned')
  await editor.getByRole('button', { name: 'Close' }).click()
  await openBox()
  await expect(prompt).toBeVisible()
  await prompt.getByRole('button', { name: 'Cancel' }).click()
  await expect(prompt).toBeHidden()
  await expect(win.locator('.tabbar .tab').last()).toContainText('(exited)')
})
```

- [ ] **Step 3: Run the e2e suite**

Run: `cd desktop && npm run e2e` (on Linux: `xvfb-run -a npm run e2e`)
Expected: `hosts.spec.ts`, `smoke.spec.ts`, `idle.spec.ts`, `throughput.spec.ts`, and `clipboard.spec.ts` all pass. The last step shows `open failed: host key not trusted` in the third tab.

- [ ] **Step 4: Commit**

```bash
git add desktop/e2e
git commit -m "$(cat <<'EOF'
test(desktop): e2e for vault creation, host CRUD, and the host-key prompt

Starts from an empty store: create a vault, add a host on sshtestd, Trust
its key and get a shell, change the port (the tab closes), come back and
trust again, forget the key, and see the prompt again.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
)"
```

---

### Task 12: Docs

**Files:**
- Modify: `CLAUDE.md`, `README.md`, `docs/superpowers/ROADMAP.md`

**Interfaces:** none (docs only).

- [ ] **Step 1: `CLAUDE.md`**

Make these replacements (old text → new text).

1. In the Hub and bridge bullet, replace
`(\`status\`, \`unlock\`, \`lock\`, \`servers\`, \`pending\`, \`decide\`, \`denyAll\`, \`term.*\`), consumed by the \`desktop/\` Electron app;`
with
`(\`hello\`, \`status\`, \`unlock\`, \`lock\`, \`servers\`, \`pending\`, \`decide\`, \`denyAll\`, \`vault.create\`, \`servers.save\`, \`servers.delete\`, \`servers.forgetHostKey\`, \`term.*\`; protocol 2, \`ProtocolVersion\` in \`idle.go\`), consumed by the \`desktop/\` Electron app;`

2. In the `Hub.Exec` order bullet, replace
`→ \`broker.Submit\` blocks for approval → re-check ctx and re-verify the server (state can change during the wait) →`
with
`→ \`broker.Submit\` blocks for approval (the request's \`target\` is the \`user@host:port\` shown to the approver) → re-check ctx and re-verify the server (state can change during the wait); \`user@host:port\` and the pin must still match the approved ones, else \`ErrServerChanged\` ("server changed") →`

3. Add a bullet after the Hub.Exec bullet:
`- Host writes (\`hosts.go\`): \`vault.create\` derives the key once and saves, reloads, and unlocks in one \`h.mu\` section (kept servers lose \`aiVisible\`; a KDF-less file with \`Enc*\` fields is refused). \`servers.save\`/\`delete\`/\`forgetHostKey\` need an unlocked vault (\`writeKey\`: \`ErrLocked\` or "create a vault first") and go through \`config.Update\`, then \`Reload\`. A save closes the server's connection (\`Registry.Close\`) only when the stored server changed beyond \`aiVisible\` (\`dialChanged\`), and denies its pending AI requests ("server changed"). Each writes a \`kind: "config"\` audit record (\`broker.ConfigRecord\`), never a secret.`

4. Replace
`- \`Deps.Resolve(name)\` still has both branches (shared code) and turns either source into a \`sshx.DialConfig\`, decrypting secrets on demand.`
with
`- \`Deps.Resolve(name)\` still has both branches (shared code) and turns either source into a \`sshx.DialConfig\`, decrypting secrets on demand. The file branch serves only the hub: \`StrictHostKey\`, the stored \`HostKeyAlgo\`, and no learner.`

5. Replace
`Renaming a server or changing host/port/user/auth therefore invalidates its ciphertexts, and the web handlers must re-encrypt (\`preserveSecrets\`).`
with
`Renaming a server or changing host/port/user/auth therefore invalidates its ciphertexts. \`config.ApplyServer\` (the hub's \`servers.save\`; \`ServerInput\` secrets are write-only: nil keeps, "" clears) re-encrypts a kept secret only when its AAD changes, and on a host or port change drops the pin and every secret not re-supplied. The web handlers re-encrypt with \`preserveSecrets\`.`

6. Replace
`- Writers serialize through \`withFlock\`. The helper is duplicated in \`internal/config\` and \`internal/web\`, with a unix and a non-unix variant of each.`
with
`- \`config.Update(path, masterKey, fn)\` is the only store writer (hub, web UI, \`RecordHostKey\`): a package mutex plus \`withFlock\` (unix; mutex only elsewhere), then load (a missing file starts empty), check key and MAC for a KDF store (refused without a key), run \`fn\`, save. Nothing is written when \`fn\` fails.`

7. Replace
`- \`Registry\` caches one \`Manager\` per server name, keyed by a hash of the \`DialConfig\` (secrets and \`HostKey\` included, callbacks excluded). A changed config closes the old connection and dials fresh. The comparison uses the manager's current pin, so a manager that just learned a key by TOFU is kept when the caller comes back with that same pin or with no pin (\`--host\` mode, a store that could not record it).`
with
`- \`Registry\` caches one \`Manager\` per server name, keyed by a hash of the \`DialConfig\` (secrets, \`HostKey\` and \`HostKeyAlgo\` included, callbacks excluded). A changed config closes the old connection and dials fresh. The comparison uses the manager's current pin, so a manager that just learned a key by TOFU is kept when the caller comes back with that same pin or with no pin (\`--host\` mode only: a strict caller's empty pin is never filled from the cache). \`Registry.Close(name)\` closes and drops one manager; its terminals see EOF and send \`term.exit\`.`

8. Replace the whole bullet that starts `- Host keys use TOFU (\`hostkey.go\`).` and ends `so the list shows the new pin.` with:
`- Host keys (\`hostkey.go\`): every hub path dials strict (\`DialConfig.StrictHostKey\`): no pin fails with \`*HostKeyUnknownError\`, a different key with \`*HostKeyMismatchError\` (wraps \`ErrHostKeyMismatch\`); both carry the presented key. Nothing on a hub path learns a key, and the approval path refuses an AI-visible server with no pin (\`ErrNoHostKey\`). The UI door's \`term.open\` answers those as results (\`{status: "hostKeyUnknown", fingerprint, keyType, knownHosts, ...}\`, \`{status: "hostKeyMismatch", pinned, presented, ...}\`), never errors. A retry with \`trustHostKey: {fingerprint, keyType}\` dials pinned to exactly that key and records it with \`config.RecordHostKey\`, which writes only while the server is unpinned and still has the dialled host and port (\`ErrPinSkipped\` otherwise, and the open fails). \`knownHosts\` (\`KnownHostsHint\` on \`~/.ssh/known_hosts\`, \`Options.KnownHostsPath\` in tests) is a hint for the human, never pinned. A pin's \`HostKeyAlgo\` limits \`HostKeyAlgorithms\` to its family. TOFU (\`OnLearnHostKey\`, the \`Manager\` keeping a learned pin) remains only in \`--host\` mode and the web UI's \`/api/test-connection\` handler, which reloads \`a.file\` after recording. \`--insecureIgnoreHostKey\` skips checks and warns on stderr; it is \`--host\` mode only, and \`ssh-mcp hub\` refuses to start with it.`

9. In the \`desktop/\` section, add a bullet after the approval panel bullet:
`- Hosts and host keys (\`CreateVault.tsx\`, \`HostEditor.tsx\`, \`hostForm.ts\`, \`hostkeys.ts\`, \`HostKeyDialog.tsx\`): \`status.hasVault\` false shows Create vault; the host list has New/Edit/Delete and the \`storePath\` footer. The editor never shows a secret (untouched = omitted = kept, Clear sends ""), warns before a host or port change, and counts the tabs a save will close. \`TermView\` asks \`HostKeyPrompts\` on \`hostKeyUnknown\` (one prompt at a time; Cancel default; Trust mouse-only and disabled for 500 ms after any content change, via \`ListChanges\`) and retries the same id with \`trustHostKey\`; \`hostKeyMismatch\` opens a dialog with both fingerprints whose only way forward is the editor's Forget button.`

- [ ] **Step 2: `README.md`**

1. In "Using saved servers from an AI client", replace the paragraph that starts `The AI only sees servers with "Visible to AI" checked in \`ssh-mcp web\`` and ends `Only do this on a network you trust for that first connection.` with:

```markdown
The AI only sees servers with "Visible to AI" checked (off by default; set it in the desktop app's host editor or in `ssh-mcp web`), and only once a host key is pinned for them — the hub refuses an AI-visible server that has no pin rather than learning one on the fly. To pin one, connect to it once from the desktop app: it shows the server's `SHA256:` fingerprint, its key type, and whether `~/.ssh/known_hosts` already lists that key, and pins it only when you click **Trust**. `ssh-mcp web` can still pin one without asking: leave "Host key fingerprint" blank, save, and click **Test connection**; only do that on a network you trust for that first connection.
```

2. In "Desktop app", replace `The window shows an unlock screen first. Once unlocked, you get:` with:

```markdown
The window shows an unlock screen first, or **Create vault** when the store has no master password yet (servers already in a password-less store are kept, with "Visible to AI" turned off). Once unlocked, you get:
```

3. Replace the first bullet (`- a host list, with an "AI" badge ... appear after Lock → Unlock (or an app restart);`) with:

```markdown
- a host list with **New host**, **Edit**, and **Delete**, an "AI" badge on servers marked "Visible to AI", a "new" badge on any server whose host key isn't pinned yet, and the vault file's path at the bottom (copying that file is the backup). The host editor never shows a saved password: an empty field keeps it, **Clear** removes it. Changing host or port forgets the pinned key and every password you don't re-enter. Saving a change to the connection closes that server's open tabs and denies any AI request waiting for it. Servers added in `ssh-mcp web` while the app is open appear after Lock → Unlock;
- a host-key prompt on first connect with the server's fingerprint; **Cancel** is the default and **Trust** needs a mouse click. A server whose key changed is refused with both fingerprints shown; if the change is expected, click **Forget host key** in the host editor and connect again;
```

- [ ] **Step 3: `docs/superpowers/ROADMAP.md`**

Replace `Updated: 2026-09-25 (slice 2 specced)` with `Updated: 2026-09-25 (slice 2a implemented)`, and in the 2a row replace `Spec drafted; entry gate overridden by the author 2026-09-25 for 2a only` with `Implemented (\`plans/2026-09-25-desktop-slice2a.md\`); exit gate running. Entry gate overridden by the author 2026-09-25 for 2a only`.

- [ ] **Step 4: Verify nothing else changed and the suites still pass**

Run: `git diff --stat`
Expected: only `CLAUDE.md`, `README.md`, `docs/superpowers/ROADMAP.md`.

Run: `go vet ./... && go test -short ./... && (cd desktop && npm run typecheck && npm test)`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md README.md docs/superpowers/ROADMAP.md
git commit -m "$(cat <<'EOF'
docs: host management, host-key prompt, and config.Update in 2a

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
EOF
)"
```

---

## Self-Review

**Spec coverage** (spec section → task):

| Spec | Task |
|---|---|
| `config.Update` (mutex, flock, load, KDF key + MAC, fn, save; keyless KDF refused) | 1 |
| `RecordHostKey` as a thin wrapper; web `saveLocked` through `Update` | 1 |
| `status.hasVault`, `status.storePath` | 3 |
| `vault.create` (fresh-load KDF check, ≥8 chars, `Enc*` refusal, `aiVisible` reset, one `h.mu` section, no second Argon2) | 3 |
| `servers.save` (create/update/rename, secret nil/""/value, AAD re-encrypt, host/port clears pin + unsupplied secrets, field validation, close only on dial change, deny pending) | 1 (logic), 4 (hub) |
| `servers.delete`, `servers.forgetHostKey`, `Registry.Close` | 2, 4 |
| `servers` extra fields, no secret | 4 |
| `HostKeyAlgo` in the MAC'd store; strict mode with typed errors; learner removed from `resolveLocked` and `Deps.Resolve`; `HostKeyAlgorithms`; registry substitution for `--host` only | 1, 2 |
| `term.open` results, `trustHostKey`, record only if still unpinned at the same endpoint, `hostKeyUnknown` after a failed trust, id released, `knownHosts` hint | 5 |
| Config audit records, no secrets | 7 |
| AI exec endpoint binding, approval card target, "server changed" audited | 6, 8 |
| Protocol 2 on both sides | 8 |
| Create vault screen, host list buttons + footer, host editor (secrets, warnings, pin + Forget, tab count, delete confirm) | 9 |
| Host-key prompt (queue, Cancel default, Trust mouse-only + 500 ms restart), mismatch dialog (both fingerprints, Close / Open host editor) | 10 |
| Web UI stays; Test connection keeps TOFU | 1 (unchanged handler, new signature) |
| Testing: Go list | 1, 2, 3, 4, 5, 6, 7 |
| Testing: Vitest list, Playwright `hosts.spec.ts` | 9, 10, 11 |
| Docs | 12 |

**Placeholder scan:** no TBD/TODO. Every code step has the code; every run step has a command and expected output.

**Type consistency:** `ServerInput` (Go JSON tags ↔ TS interface), `uiServer` ↔ `ServerInfo`, `term.open` result keys ↔ `HostKeyUnknown`/`HostKeyMismatch` ↔ `openResult` (Go test), `trustHostKey {fingerprint, keyType}` (hub ↔ transport ↔ TermView), `Request.Target` ↔ `ApprovalRequest.target`, `ConfigRecord` fields ↔ `configaudit_test.go` keys, `TerminalsHandle.openCount` ↔ `TabSet.openCount`, test helpers `newHubAt` (3) → `waitNote`/`noNote`/`save`/`inputFor`/`uiServerNamed` (4) → `trustHub`/`openTerm`/`trusting` (5) → Task 7.
