# Show a saved secret: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The host editor can show one saved secret after Touch ID or the master password typed again; the hub checks the master password on every call.

**Architecture:** New UI-door method `servers.reveal {server, field, password}` in the hub: it derives the password and checks it the way `Unlock` does, decrypts one `Enc*` field, and audits. The Touch ID helper gains a `reveal` verb (same as `unlock`, own prompt); `touchid.Manager.Reveal` unseals the master password and calls `servers.reveal` itself, so the password never reaches a view. The editor gets a Show button and a small dialog.

**Tech Stack:** Go (hub, app on mygo `ui`), Swift (helper).

**Spec:** `docs/superpowers/specs/2026-11-22-reveal-secret-design.md`

## Global Constraints

- UI door only. `servers.reveal` must not exist on the MCP door, in `hub --cli`, or in `--host` mode.
- No secret or master password in a log, stderr, error string, audit record, command-line argument, or editable field.
- No "verified for N minutes" state anywhere: every showing carries its own proof.
- Wire fields: `password`, `suPassword`, `sudoPassword`, `keyPassphrase` → stored `encPassword`, `encSuPassword`, `encSudoPassword`, `encKeyPassphrase` (label `<name>/<stored>`, AAD `config.AADFor(f, s, <stored>)`).
- Wrong password text is exactly `wrong master password` (the app's `wrongPassword` matches it).
- Audit: `kind: "config"`, action `secretReveal` or `secretRevealDenied`, `server`, `field` (wire name).
- Helper prompt for reveal: `show a saved password in sshgate`.
- Shown value hides after 15 s, and at once on lock, leaving or saving the editor, and hub restart. No Copy button.
- Editor hint: `Leave a field empty to keep it.`
- Protocol 16.

## Review Focus

1. The hub locks (or the server is deleted or edited) while the 1 s derive runs: the reveal must fail (`ErrLocked`, or the server error), not decrypt with a key the hub no longer holds or a blob that moved. Test in Task 1 (`TestRevealLockDuringDerive`).
2. Two reveals at once: the second is refused at once with `ErrRevealBusy`, never derives in parallel. Test in Task 1 (`TestRevealOneAtATime`).
3. A stale `touchid.json` after a master password change: Touch ID reveal answers `wrong master password`, the file is deleted and the person sees `ErrReset`. Test in Task 2 (`TestRevealStaleDisables`).
4. The value stays on screen after the vault locks: it must vanish at the lock, not at the 15 s timer. Test in Task 3 (`TestEditorRevealHidesOnLock`).
5. Saving the editor right after a reveal must not send the revealed value (the input stays empty, meaning "keep"). Test in Task 3 (`TestEditorRevealNotInInput`).

---

### Task 1: Hub `servers.reveal`

**Files:**
- Create: `internal/hub/reveal.go`, `internal/hub/reveal_test.go`
- Modify: `internal/hub/uidoor.go` (register next to `keys.delete`, ~line 305), `internal/broker/audit.go:38-58` (`ConfigRecord`), `internal/hub/idle.go:5` (`ProtocolVersion = 16`), `app/hubproc/proc.go:19` (`Protocol = 16`), any test that asserts protocol 15 (`grep -rn "15" --include=*_test.go | grep -i protocol`), `CLAUDE.md` (UI door method list, protocol 16, the `servers.reveal` paragraph: order, audit actions, one at a time).

**Interfaces:**
- Produces: `func (h *Hub) RevealSecret(server, field, password string) (string, error)`; `var ErrRevealBusy = errors.New("another reveal is running; try again")`; `ConfigRecord.Field string \`json:"field,omitempty"\`` (comment: `secretReveal*: the wire name of the field, never its value`); door reply `{"value": "<plaintext>"}`.

- [ ] **Step 1: Write the failing tests** in `reveal_test.go`, using `newEncHub` (server `enc`, password `s3cr3t-pw`, master `pw`) unlocked with `unlockForTest`, `startUI`, `readAudit`, `blockDerive`, `startDoor`:
  - `TestRevealShows`: `servers.reveal {server:"enc", field:"password", password:"pw"}` → `value == "s3cr3t-pw"`; last record `{kind:"config", action:"secretReveal", server:"enc", field:"password"}`; raw audit file contains neither `s3cr3t-pw` nor `"pw"` as a value.
  - `TestRevealEachField`: a server with all four secrets (build with `config.Encrypt` as `newEncHub` does) → each wire name returns its own plaintext.
  - `TestRevealWrongPassword`: `password:"nope"` → error text `wrong master password`; record action `secretRevealDenied`.
  - `TestRevealRefusals`: hard lock (`lock {stopAuto:true}`) → `ErrLocked`; soft lock (a live grant, as `softlock_door_test.go` sets one up, then `lock {}`) → `ErrLocked`; no vault (`newHubAt(t, nil, nil)`) → `create a vault first`; unknown server → `no such server`; `agent` server, field `password` → `no saved password`; none of these writes an audit record.
  - `TestRevealStrictParams`: extra key, missing `field` handled as bad field, `field:"encPassword"` → `-32602`.
  - `TestRevealLockDuringDerive`: `blockDerive`, start a reveal, wait `started`, `h.Lock()`, `close(release)` → `ErrLocked`. Same with the server deleted mid-derive → `no such server`; with the server's host changed (blob re-keyed) → decrypt not attempted with the old blob (error, no plaintext).
  - `TestRevealOneAtATime`: `blockDerive`; second call while the first waits → `ErrRevealBusy`.
  - `TestRevealNotOnMCPDoor`: `startDoor(t, h)`; `servers.reveal` → `*rpc.Error` code `-32601`.

- [ ] **Step 2: Run** `go test ./internal/hub -run TestReveal -v` → FAIL (method not found / undefined).

- [ ] **Step 3: Implement `RevealSecret` in `reveal.go`**, in this order (the spec's): 
  1. `h.revealing atomic.Bool` (new `Hub` field): `CompareAndSwap(false, true)` else `ErrRevealBusy`; `defer Store(false)`.
  2. Under `h.mu`: `mk, err := h.writeKeyLocked()` (gives `ErrLocked` under soft and hard lock, "create a vault first" with no vault), `clear(mk)`; find the server in `h.deps.File.Servers` by name (hidden ones included) else `errors.New("no such server")`; map `field` (unknown → `errBadParams`); empty blob → `errors.New("no saved " + field)`; copy the KDF.
  3. Without `h.mu`: `dk, err := deriveKey(&kdf, password)`; `defer clear(dk)`.
  4. Under `h.mu` again: KDF changed → `the vault changed during unlock; try again`; `!KDF.Verify(dk)` → audit `secretRevealDenied`, return `errors.New("wrong master password")`; `writeKeyLocked()` again (lock during derive → `ErrLocked`); look the server and blob up again (gone → `no such server`, blob now empty → `no saved <field>`); `config.Decrypt(mk, s.Name+"/"+stored, config.AADFor(h.deps.File, s, stored), blob)` with the fresh `mk`; audit `secretReveal`; return the plaintext.
  - Errors from `Decrypt` return a generic `errors.New("could not decrypt the saved " + field)`.
  - Register in `uidoor.go`: `req("servers.reveal", ...)` with `strictParams(raw, &p, "server", "field", "password")`, reply `map[string]string{"value": v}`.
  - Validate `field` in the door handler or in `RevealSecret` before any lock work, as `-32602` (`errBadParams`), and refuse it before the busy flag so a bad call does not take the slot.

- [ ] **Step 4: Run** `go test -race ./internal/hub -run 'TestReveal|TestHello|Protocol' -v` → PASS; then `go test -race -short ./...` and `(cd app && go test ./hubproc/...)` → PASS.

- [ ] **Step 5: Commit** `feat(hub): servers.reveal shows one saved secret after the master password is checked again`.

### Task 2: Touch ID `reveal`

**Files:**
- Modify: `app/native/touchid/main.swift` (`case "reveal":` sharing `unlock`'s body, `localizedReason = "show a saved password in sshgate"`), `app/touchid/testdata/fake-touchid.sh` (`unlock|reveal)` same branch), `app/touchid/touchid.go`, `app/touchid/touchid_test.go`, `app/hubapi/client.go`.

**Interfaces:**
- Consumes: door method `servers.reveal` from Task 1.
- Produces:
  - `func (h *Client) ServersReveal(ctx context.Context, server, field, password string) (string, error)` in `hubapi/client.go` (calls `servers.reveal`, decodes `{value}`).
  - `touchid.Hub` gains `ServersReveal(ctx context.Context, server, field, password string) (string, error)`.
  - `var ErrCancelled = errors.New("Touch ID was cancelled")` in `touchid`.
  - `func (m *Manager) Reveal(ctx context.Context, server, field string) (string, error)`: runs the helper with verb `reveal`, then `ServersReveal` with the unsealed password, wipes it, returns only the value. Exit 2 → `ErrCancelled`; 3 → `Disable`, `ErrReset`; 4 → `ErrUnavailable`; other → `ErrFailed`; hub `wrong master password` → `Disable`, `ErrReset`; same `busy`/`gen`/`stopped` handling as `Unlock`.

- [ ] **Step 1: Write the failing tests** in `touchid_test.go`, with the fake helper and the test hub fake the existing `Unlock` tests use (add `ServersReveal` to it, recording the password it got and returning a fixed value):
  - `TestRevealReturnsValue`: mode `ok` → returns the fake hub's value; the fake hub received the enrolled password.
  - `TestRevealCancel`: mode `cancel` → `ErrCancelled`; `touchid.json` still there.
  - `TestRevealStaleDisables`: fake hub answers `&door.Error{Message: "wrong master password"}` → `ErrReset`; `touchid.json` gone.
  - `TestRevealInvalidKey`: mode `invalid` → `ErrReset`, file gone. Mode `unavailable` → `ErrUnavailable`.
  - `TestRevealBusy`: mode `slow`, second call → `ErrBusy`.

- [ ] **Step 2: Run** `(cd app && go test ./touchid -run TestReveal -v)` → FAIL.

- [ ] **Step 3: Implement.** Extract from `Unlock` the shared part (busy flag, status read, sealed file, `m.run([]string{verb}, ...)`, exit-code switch) into one unexported method taking the verb and returning the unsealed bytes; `Unlock` keeps its behaviour and tests unchanged. Add the Swift `reveal` case by sharing `unlock`'s code with the reason as the only difference (a function taking the reason).

- [ ] **Step 4: Run** `(cd app && go test -race ./touchid ./hubapi)` → PASS (existing `Unlock` tests included). Hand check, macOS: `(cd app && ./scripts/build-touchid.sh)`, then with Touch ID enabled in the app, `reveal` shows the Touch ID sheet reading "show a saved password in sshgate".

- [ ] **Step 5: Commit** `feat(touchid): reveal verb with its own prompt; Manager.Reveal passes the unsealed password straight to servers.reveal`.

### Task 3: Editor Show button, dialog and audit row

**Files:**
- Modify: `app/view/editor.go` (`secret := func(f model.Secret)` at ~line 224; hint at line 281), `app/view/touch.go` (`Toucher` gains `Reveal`), `app/view/touch_test.go` (`fakeToucher.Reveal`), `app/model/hostform.go` (`func (f Secret) Wire() string`: `password`, `suPassword`, `sudoPassword`, `keyPassphrase`), `app/model/audit.go:343` (default `cmp.Or` gains `r.str("field")` after `r.str("key")`), `app/model/audit_test.go`, new `app/view/reveal_test.go`, `CLAUDE.md` (`app/` section: one line on Show).

**Interfaces:**
- Consumes: `hubapi.Client.ServersReveal` and `touchid.Manager.Reveal`, `touchid.ErrCancelled` (Task 2).
- Produces: `Toucher.Reveal(ctx context.Context, server, field string) (string, error)`; `editorState.reveal *revealState` (`field model.Secret`, `pw string`, `busy bool`, `err string`, `value string`, `at time.Time`).

Behaviour (from the spec): a `Show` button next to `Clear` only when `saved && !e.Cleared` and the host exists. Click opens the dialog: master password input (`Password()`, Enter submits), Cancel (focused, filled primary), and `Use Touch ID` when `a.touch.Enabled`. With Touch ID on, the dialog calls `a.Touch.Reveal` once when it opens. `ErrCancelled` leaves the dialog open with no error; other Touch ID errors show in the dialog and reload touch status (`a.loadTouch()`). A typed submit calls `a.hub().ServersReveal`; `wrong master password` keeps the dialog open with that error; success closes the dialog, clears `pw`, sets `value` and `at`. The value shows as read-only monospace text under the field row (`ui.Text`), never in the input. `c.After(at.Add(15*time.Second).Sub(now))` hides it; lock, editor close or save, and `hubGen` change set `reveal = nil`. A result that arrives after any of those is dropped (compare `hubGen` and the editor's server name, as `touchUnlock` does).

- [ ] **Step 1: Write the failing tests.**
  - `model`: `TestSecretWire` (four names); `TestAuditRowViewConfig` gains `{kind:"config", action:"secretReveal", server:"enc", field:"password"}` → `Main == "password"`, badge `secret reveal`.
  - `view/reveal_test.go` (flow tests start a real hub as the editor tests do; the vault holds `enc` with a saved password):
    - `TestEditorRevealTyped`: Show → type `pw` → Enter → text `s3cr3t-pw` visible; after `tt.Advance(15*time.Second)` gone.
    - `TestEditorRevealWrongPassword`: `nope` → dialog still open, `wrong master password` shown.
    - `TestEditorRevealTouch`: `fakeToucher` enabled, `Reveal` returns `s3cr3t-pw` → shown without typing; `Reveal` returns `touchid.ErrCancelled` → dialog open, no error text.
    - `TestEditorRevealHidesOnLock`: reveal, then lock → value gone before 15 s.
    - `TestEditorRevealNotInInput`: reveal, then Save → the `servers.save` params carry `password: null` (keep), never the value; input text empty.
    - `TestEditorNoShowWithoutSaved`: new host, and a host with no saved password → no `Show` button.
    - Hint text `Leave a field empty to keep it.`

- [ ] **Step 2: Run** `(cd app && go test ./model ./view -run 'Reveal|Wire|AuditRowViewConfig|NoShow' -v)` → FAIL.

- [ ] **Step 3: Implement** per the behaviour above, in `editor.go` beside the existing delete dialog (~line 460) for layout; reuse its dialog pattern.

- [ ] **Step 4: Run** `(cd app && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race ./...)` → PASS (about 6 min); `go vet ./... && (cd app && go vet ./...)` clean.

- [ ] **Step 5: Commit** `feat(app): Show a saved secret in the host editor behind Touch ID or the master password`.

### Task 4: Package and hand check

- [ ] **Step 1:** Quit sshgate; `(cd app && ../scripts/build-ghostty-vt.sh build/natives && ./scripts/package-mac.sh)` → ends with its checks passing.
- [ ] **Step 2:** Install (`rm -rf /Applications/sshgate.app && ditto app/out/sshgate.app /Applications/sshgate.app`); unlock; in a host with a saved password: Show → Touch ID sheet reads "show a saved password in sshgate" → value shows, hides after 15 s; Cancel the sheet → password field; Audit shows `secret reveal` rows.
- [ ] **Step 3:** Record the outcome in the PR body as evidence (commands with exit status, hand-check results; say what was not checked).
