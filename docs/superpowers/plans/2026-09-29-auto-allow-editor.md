# Auto-allow in the Host editor, with root and sudo-exec opt-ins: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the auto-allow control into the Host editor and apply it on Save. Add two per-host opt-ins: "Allow on root hosts" and "Also auto-allow sudo-exec".

**Architecture:**
- **Vault.** Two new booleans on `config.Server`, both covered by the vault MAC and written by `servers.save`.
- **Refusals.** With "Allow on root hosts" on, `autoRefusal` stops refusing root-equivalent hosts.
- **sudo-exec.** `autoStart` lets `sudoExec` through when "Also auto-allow sudo-exec" is on.
- **Arming on save.** `servers.save` takes an `autoAllow` mode. The hub ends the old grant and arms the new one in the same `h.mu` section as the write, through `armLocked`, which is factored out of `SetAutoAllow`.
- **Renderer.** Adds the controls to the editor's AI access section and reuses the confirm dialog before Save. The host card keeps only Stop.

**Tech Stack:** Go 1.27 (hub, config), Electron + React + vitest + Playwright (`desktop/`).

**Spec:** `docs/superpowers/specs/2026-09-28-auto-allow-design.md`, section "Amendment 2026-09-29". It is authoritative; earlier sections apply where the amendment does not override them. Read `CLAUDE.md` too.

## Global Constraints

- No new dependencies.
- The opt-ins default to false and are independent of each other. They are written only through `servers.save`.
- **Refusals with "Allow on root hosts" off:** a root login, a stored su password, and a stored sudo password.
- **Refusals that always apply:** not AI-visible, no pinned host key, locked, no vault. Hidden and nonexistent servers return byte-identical errors.
- `sudoExec` on a granted host waits for approval unless `AutoAllowSudo` is set. When it is set, the run is audited with `approval: "auto"` and `sudo: true`.
- Every save ends the old grant first. Arming for the saved mode happens in the same `h.mu` section as the write.
- If arming fails, the save stands and the call returns an error that starts with exactly `saved, but auto-allow was not turned on: `.
- Protocol version 7 (`internal/hub/idle.go`, `desktop/src/shared/protocol.ts`, `desktop/test/fixtures/fakeHub.mjs`).
- The renderer never calls the hub on a timer or in response to a notification.
- Confirm buttons are mouse-only, have a 500 ms delay, and default to Cancel.
- Keep the lock order: h.mu → config.Update is allowed. Never call `notifyAuto`, `writeKey` or `Reload` (the non-`Locked` forms) while holding h.mu.
- Commit trailer (use your model name):
  `Co-Authored-By: <model> <noreply@anthropic.com>`
  `Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2`
- Checks: `go vet ./...` and `go test -race` on the touched packages, each run with `-timeout`. Desktop: `cd desktop && npm run typecheck && npm test`.

---

### Task 1: The two opt-ins in the vault, refusals, and the sudo-exec auto path

**Files:**
- Modify: `internal/config/store.go` (`Server`), `internal/config/server_input.go` (`ServerInput`, `ApplyServer`)
- Modify: `internal/hub/hosts.go` (`dialChanged`, `changes`, `CreateVault`)
- Modify: `internal/hub/uidoor.go` (`uiServer`, `serversForUI`)
- Modify: `internal/hub/autoallow.go` (`autoRefusal`, `autoStart`, `autoExec`)
- Modify: `internal/hub/hub.go` (`Exec`: the `!r.Sudo` guard)
- Test: `internal/config/server_input_test.go`, `internal/hub/hosts_test.go`, `internal/hub/autoallow_test.go`

**Interfaces produced:**
- `config.Server.AutoAllowRoot bool` (`json:"autoAllowRoot,omitempty"`) and `config.Server.AutoAllowSudo bool` (`json:"autoAllowSudo,omitempty"`).
- `config.ServerInput.AutoAllowRoot bool` (`json:"autoAllowRoot"`) and `config.ServerInput.AutoAllowSudo bool` (`json:"autoAllowSudo"`).
- `uiServer.AutoAllowRoot` / `uiServer.AutoAllowSudo` (`json:"autoAllowRoot"`, `json:"autoAllowSudo"`).
- `func (h *Hub) autoStart(ctx context.Context, name string, sudo bool) *autoRun`.
- The `autoAllow.ran` notification gains `"sudo": true` when the run was `sudoExec`.

- [ ] **Step 1: Write failing tests.**
  - `config`: `ApplyServer` copies `AutoAllowRoot` and `AutoAllowSudo` from the input.
  - `hub`:
    - `dialChanged` ignores both fields.
    - `changes()` lists `autoAllowRoot: false → true` and `autoAllowSudo: …`.
    - `CreateVault` clears both on kept servers.
  - `autoallow_test.go`:
    - **`TestRootOptInAllowsRootHosts`:** a root-login server, a server with a stored su password, and one with a stored sudo password (use the existing store-rewrite helper) are refused by `SetAutoAllow("15m")` when `AutoAllowRoot` is false. With `AutoAllowRoot` true they are allowed. With the option on, the refusals for not AI-visible or no pinned host key still apply.
    - **`TestSudoExecNeedsOptIn`:** grant on `vis`.
      - With `AutoAllowSudo` false: `Exec` with `Sudo: true` goes pending. Deny it; the grant must still be there.
      - With `AutoAllowSudo` true (set via the store rewrite plus a reload; this leaves the grant intact because it is not a `servers.save`): the same call runs with no pending request. `fakeExec` records `"sudo:<cmd>"`. The audit record has `"approval":"auto","sudo":true`. `autoAllow.ran` carries `sudo: true`.
    - **`TestAutoStartEndsGrantWhenRootOptInRemoved`:** a root server with the option on and a grant. An outside rewrite turns `AutoAllowRoot` off and reloads. The next exec goes to approval, and the grant ended with reason `"server changed"`.
    - `uiServer` shows both options.
- [ ] **Step 2: Run the tests and confirm they fail.** `go test ./internal/config ./internal/hub -run 'ApplyServer|DialChanged|Changes|CreateVault|RootOptIn|SudoExecNeedsOptIn|RootOptInRemoved' -timeout 5m`
- [ ] **Step 3: Implement.**
  - `Server` gets the two fields after `AutoAllow`. `ServerInput` gets them after `AIVisible`. In `ApplyServer`, add `AutoAllowRoot: in.AutoAllowRoot, AutoAllowSudo: in.AutoAllowSudo` to the `after` literal. Update the doc comment: "AutoAllow is never carried over (every save turns auto-allow off); the two opt-ins are fields the editor sends."
  - `dialChanged`: `a.AIVisible, a.AutoAllow, a.AutoAllowRoot, a.AutoAllowSudo, a.Tunnels = b.AIVisible, b.AutoAllow, b.AutoAllowRoot, b.AutoAllowSudo, nil`.
  - `changes`: add `{"autoAllowRoot", a.AutoAllowRoot, b.AutoAllowRoot}` and `{"autoAllowSudo", a.AutoAllowSudo, b.AutoAllowSudo}`.
  - `CreateVault`: `s.AutoAllowRoot, s.AutoAllowSudo = false, false`, next to `s.AutoAllow = false`.
  - `autoRefusal`:
    ```go
    func autoRefusal(s config.Server) string {
    	switch {
    	case !s.AIVisible:
    		return "not visible to AI"
    	case s.HostKey == "":
    		return "no pinned host key"
    	case s.AutoAllowRoot:
    		return "" // opted in: root logins and stored su/sudo passwords allowed
    	case s.User == "root":
    		return "root login"
    	case s.EncSuPassword != "":
    		return "has an su password"
    	case s.EncSudoPassword != "":
    		return "has a sudo password"
    	}
    	return ""
    }
    ```
    Also update the comment above it: root access is refused unless the human opted in.
  - `Exec`: replace `if !r.Sudo { if ar := h.autoStart(ctx, r.Server); … }` with `if ar := h.autoStart(ctx, r.Server, r.Sudo); ar != nil { defer ar.done(); return h.autoExec(ar, dc, r, cmd, timeout, base) }`.
  - `autoStart(ctx, name, sudo)`: after the `reason` switch and the inflight cap, and before registering the run, add:
    ```go
    	if sudo && !s.AutoAllowSudo { // sudo-exec needs its own opt-in; the grant stays
    		h.mu.Unlock()
    		return nil
    	}
    ```
    It must come after the checks that can end the grant, so a changed server still ends it. Update the existing direct call in `autoallow_test.go` to pass `false`.
  - `autoExec`: `h.run(ar.ctx, r.Server, ar.dc, cmd, r.Sudo, timeout, base, red)`, and `if r.Sudo { ran["sudo"] = true }`.
  - `uiServer`: add `AutoAllowRoot bool json:"autoAllowRoot"` and `AutoAllowSudo bool json:"autoAllowSudo"`, set in `serversForUI`.
- [ ] **Step 4: Run the tests and confirm they pass.** `go vet ./... && go test -race -count=1 -timeout 10m ./internal/config ./internal/hub ./cmd/sshgate`
- [ ] **Step 5: Commit.** `feat(hub): auto-allow opt-ins for root hosts and sudo-exec`

### Task 2: Arm on save (`servers.save` with a mode), protocol 7

**Files:**
- Modify: `internal/hub/autoallow.go` (extract `armLocked`; `SetAutoAllow` uses it)
- Modify: `internal/hub/hosts.go` (`SaveServerWithAutoAllow`; `SaveServer` wraps it)
- Modify: `internal/hub/uidoor.go` (`servers.save` handler)
- Modify: `internal/hub/idle.go` (`ProtocolVersion = 7`), `desktop/src/shared/protocol.ts` (`PROTOCOL_VERSION = 7` only), `desktop/test/fixtures/fakeHub.mjs` (protocol 7)
- Test: `internal/hub/autoallow_test.go`, `internal/hub/uidoor_test.go`

**Interfaces:**
- Consumes: Task 1's fields and refusals.
- Produces:
  - `type endedNote struct{ name, reason string }`
  - `func (h *Hub) armLocked(name, mode string) ([]endedNote, error)`. It is called with h.mu held, after a successful `reloadLocked`, and only with a mode other than `off`. It returns the grant-end notifications for the caller to send after unlocking.
  - `func (h *Hub) SaveServerWithAutoAllow(original string, in config.ServerInput, mode string) error`
  - `const errSavedButPrefix = "saved, but auto-allow was not turned on: "`
  - The UI door's `servers.save {original, server, autoAllow?}`.

- [ ] **Step 1: Write failing tests.**
  - **`TestSaveArmsTimed`:** `SaveServerWithAutoAllow("vis", <same fields>, "15m")` leaves a timed grant armed. The audit shows `save` and `autoAllowOn` with `until`.
  - **`TestSaveArmsForever`:** mode `"forever"` sets the flag on disk, arms the grant, and audits `autoAllowOn forever`.
  - **`TestSaveEndsOldGrantThenArms`:** an existing forever grant plus a save with `"30m"`. The audit records, in order: `autoAllowOff saved`, `save`, then `autoAllowOn` with `until`. Record the order you actually implement: the `autoAllowOff` audit is written inside the locked section, before `save`. The grant ends up timed and the flag false.
  - **`TestSaveOffEndsGrant`:** mode `"off"` or `""` behaves exactly like today's `SaveServer` (the grant ends and nothing is armed).
  - **`TestSaveArmRefusedKeepsSave`:** save a root server with the label changed and `AutoAllowRoot` false, mode `"15m"`. The label change is on disk, no grant is armed, and the error is `errSavedButPrefix + "auto-allow refused: root login"`. The same save with `AutoAllowRoot` true in the input arms the grant.
  - **`TestSaveRenameArmsUnderNewName`:** rename `vis`→`vis2` with mode `"15m"`. The grant is under `vis2`, and nothing remains under `vis`.
  - `uidoor_test.go`: `servers.save` with `"autoAllow":"15m"` arms a grant. `"autoAllow":"1h"` fails with -32602. A missing `autoAllow` means off. `hello` returns `{"protocol":7}`.
  - The existing `SetAutoAllow` tests (resume, idempotent, refusals, race, reload failure) must still pass unchanged. That proves the `armLocked` extraction.
- [ ] **Step 2: Run the tests and confirm they fail.** `go test ./internal/hub -run 'SaveArms|SaveEndsOldGrant|SaveOff|SaveArmRefused|SaveRenameArms|Hello|ServersSave' -timeout 5m`
- [ ] **Step 3: Implement.**
  - Extract everything in `SetAutoAllow` from `s, dc, err := h.autoEligibleLocked(name)` to the final `autoAllowOn`/`autoAllowResume` audit into `armLocked(name, mode string) ([]endedNote, error)`. Every `h.mu.Unlock()` + `h.notifyGrantEnded(n, r)` inside it becomes an append of `endedNote{n, r}` to the returned slice. Every `errAutoResolve` mapping to `ErrConnFailed` stays. Behaviour must be identical.
  - `SetAutoAllow` becomes:
    ```go
    func (h *Hub) SetAutoAllow(name, mode string) error {
    	if mode == "off" {
    		return h.autoAllowOff(name, "turned off")
    	}
    	h.mu.Lock()
    	if err := h.reloadLocked(); err != nil {
    		h.mu.Unlock()
    		return err
    	}
    	notes, err := h.armLocked(name, mode)
    	h.mu.Unlock()
    	for _, n := range notes {
    		h.notifyGrantEnded(n.name, n.reason)
    	}
    	return err
    }
    ```
  - `SaveServer(original, in)` becomes `return h.SaveServerWithAutoAllow(original, in, "off")`. Move today's body into `SaveServerWithAutoAllow`, with one addition. After the rename handling, and before `h.mu.Unlock()`, add:
    ```go
    	var armNotes []endedNote
    	var armErr error
    	if mode != "" && mode != "off" && reloadErr == nil {
    		armNotes, armErr = h.armLocked(after.Name, mode)
    	}
    ```
    After unlocking, send the existing notifies first, then `armNotes`. Then run the existing cleanup and the `save` audit. The return value:
    - `reloadErr`, if it is non-nil. When the reload failed nothing is armed, so also report `errSavedButPrefix + "the vault could not be reloaded"`: return `fmt.Errorf("%s%w", errSavedButPrefix, reloadErr)` when a mode was requested, else `reloadErr`.
    - otherwise, if `armErr != nil`: `fmt.Errorf("%s%w", errSavedButPrefix, armErr)`.
    - otherwise nil.
  - Handler: add `AutoAllow string json:"autoAllow"` to the params struct. After `Validate`, check `p.AutoAllow == "" || p.AutoAllow == "off" || p.AutoAllow == "forever" || autoModes[p.AutoAllow] != 0`; otherwise return -32602 with the message `"autoAllow must be off, 15m, 30m, 60m, 2h, 4h, or forever"`. Then call `SaveServerWithAutoAllow`.
  - Set `ProtocolVersion = 7`, `PROTOCOL_VERSION = 7`, and protocol 7 in the fixture.
- [ ] **Step 4: Run the tests and confirm they pass.** `go vet ./... && go test -race -count=1 -timeout 10m ./internal/hub ./cmd/sshgate`, then `go test -race -run 'SetAutoAllow|AutoAllow|Save' -count=10 -cpu=1,2,8 -timeout 10m ./internal/hub`, then `cd desktop && npm run typecheck && npm test`.
- [ ] **Step 5: Commit.** `feat(hub): servers.save arms auto-allow in the same locked section (protocol 7)`

### Task 3: Desktop: protocol, transport, editor form logic

**Files:**
- Modify: `desktop/src/shared/protocol.ts`, `desktop/src/renderer/transport.ts`, `desktop/src/renderer/hostForm.ts`, `desktop/src/renderer/autoallow.ts`
- Test: `desktop/test/hostForm.test.ts`, `desktop/test/autoallow.test.ts`

**Interfaces produced:**
- `protocol.ts`:
  - `ServerInfo.autoAllowRoot: boolean` and `ServerInfo.autoAllowSudo: boolean`.
  - `ServerInput.autoAllowRoot: boolean` and `ServerInput.autoAllowSudo: boolean`.
  - `AutoAllowRan.sudo?: boolean`.
- `transport.ts`: `hub.saveServer(server: ServerInput, original?: string, autoAllow?: AutoAllowMode)` sends `{original, server, autoAllow}`.
- `hostForm.ts`: `HostDraft` gains `autoAllow: AutoAllowMode`, `autoAllowRoot: boolean` and `autoAllowSudo: boolean`. `draftFrom(s)` sets `autoAllow: s?.autoAllow?.forever ? 'forever' : 'off'`, and takes `autoAllowRoot` and `autoAllowSudo` from `s` (false for a new host). `toInput` includes both booleans.
- `autoallow.ts`:
  - `export const SAVED_BUT = 'saved, but auto-allow was not turned on: '`
  - `export function timedNote(s: ServerInfo | undefined, now: number): string | undefined`. If `s.autoAllow.until` is still in the future, it returns `On for ${min} more min — saving ends it; pick a duration to keep auto-allow on`, where `min` is the minutes rounded up. Otherwise it returns undefined.
  - `export function saveConfirm(s: ServerInfo | undefined, d: HostDraft): { needed: boolean; typeName: boolean; rootNew: boolean; sudoNew: boolean }`:
    - `needed` is `d.autoAllow !== 'off'` and not "already armed forever with the same options". "Already armed forever with the same options" means `d.autoAllow === 'forever' && s?.autoAllow?.forever && !s.autoAllow.paused && d.autoAllowRoot === s.autoAllowRoot && d.autoAllowSudo === s.autoAllowSudo`.
    - `typeName` is `d.autoAllow === 'forever' && !s?.autoAllow?.forever`.
    - `rootNew` is `d.autoAllowRoot && !s?.autoAllowRoot`.
    - `sudoNew` is `d.autoAllowSudo && !s?.autoAllowSudo`.
  - `export function draftRefusal(s: ServerInfo | undefined, d: HostDraft): string | undefined`:
    - For a new host (no `s`), or when `s.hostKey` is empty: `'no pinned host key'`.
    - When `!d.aiVisible`: `'not visible to AI'`.
    - When `s.autoAllowRefused` is one of `'root login' | 'has an su password' | 'has a sudo password'`: return undefined if `d.autoAllowRoot` is true, otherwise return that reason.
    - Otherwise undefined. The hub has the final say on the draft's other edits.

- [ ] **Step 1: Write failing vitest tests** covering every branch above. For `draftFrom`, check a forever host (including a paused one) gives `'forever'`, and a timed or off host gives `'off'`. Check that `toInput` carries the booleans. Check `saveConfirm` for:
  - off;
  - timed;
  - forever that is new;
  - forever already armed with the same options, where `needed` is false;
  - forever already armed with the root option newly ticked, where `needed` is true and `rootNew` is true;
  - a paused forever, where `needed` is true and `typeName` is false.

  Check `timedNote` rounding, and past deadlines. Check `draftRefusal` for each case. Check that `transport.saveServer` sends `autoAllow`: extend the existing transport test pattern.
- [ ] **Step 2: Run the tests and confirm they fail.** `cd desktop && npx vitest run test/hostForm.test.ts test/autoallow.test.ts test/transport.test.ts`
- [ ] **Step 3: Implement** the interfaces above. Fix any type errors the new required `ServerInfo` fields cause in test fixtures, e.g. `desktop/test/fixtures` or tests that build `ServerInfo` literals, by adding `autoAllowRoot: false, autoAllowSudo: false`.
- [ ] **Step 4: Run the checks and confirm they pass.** `cd desktop && npm run typecheck && npm test`
- [ ] **Step 5: Commit.** `feat(desktop): editor form and save-confirm logic for auto-allow`

### Task 4: Desktop UI in the editor, card Stop only, e2e, docs

**Files:**
- Modify: `desktop/src/renderer/HostEditor.tsx`, `desktop/src/renderer/AutoAllowDialog.tsx`, `desktop/src/renderer/App.tsx`, `desktop/src/renderer/HostList.tsx`, `desktop/src/renderer/ApprovalPanel.tsx` (feed `sudo` tag)
- Modify: `desktop/e2e/autoallow.spec.ts`
- Modify: `docs/superpowers/ROADMAP.md`, `README.md`, `PRODUCT.md`, `CLAUDE.md`, the spec's Status line
- Test: `desktop/test/AutoAllowDialog.test.ts`

**Interfaces:**
- Consumes: Task 3's `draftFrom`, `toInput`, `saveConfirm`, `draftRefusal`, `timedNote`, `SAVED_BUT`, and `hub.saveServer(server, original, autoAllow)`.
- Produces:
  - `AutoAllowDialog` props become `{ server: ServerInfo; mode: Exclude<AutoAllowMode,'off'>; typeName: boolean; rootNew: boolean; sudoNew: boolean; refused?: string; remoteTunnels: string[]; check: () => Promise<AutoAllowCheck>; onEnable: () => Promise<void>; onCancel: () => void }`. The duration radios are removed.
  - `HostEditor` `onSave: (input: ServerInput, original: string | undefined, autoAllow: AutoAllowMode) => Promise<void>`.

- [ ] **Step 1: Update the vitest for `AutoAllowDialog`** (`renderToStaticMarkup`):
  - No radios. The chosen duration's text is shown, e.g. "For 15 min (ends at HH:MM)" or "Until turned off".
  - The typed-name input appears only when `typeName` is true.
  - The red root warning "Allowing root: the AI runs as root, and a command it plants can capture sudo or su passwords you type or store" appears only when `rootNew`.
  - The red sudo warning "sudo-exec will run without asking: the AI has full root on this host" appears only when `sudoNew`.
  - When `refused` is set, the refusal text is shown and Enable is disabled.
  - Enable is `tabindex="-1"` and disabled at mount.
  - Keep the existing `dialogChangeKey` / `ListChanges` render-time delay. The key must now include `mode`, `rootNew` and `sudoNew`.

  Run it and see it fail, then implement the dialog changes (keep `enableAllowed`, using `typed` only when `typeName`) and see it pass.
- [ ] **Step 2: `HostEditor.tsx`, the AI access section** (inside the existing `<details className="fold">`):
  - Keep the Visible to AI switch.
  - Add a labelled `<select>` "Auto-allow" with the options Off, 15 min, 30 min, 60 min, 2 h, 4 h, and Until turned off (use `AUTO_MODES` plus Off), bound to `draft.autoAllow`.
  - Add two checkboxes, "Allow on root hosts" and "Also auto-allow sudo-exec", bound to `draft.autoAllowRoot` and `draft.autoAllowSudo`. They are disabled while `draft.autoAllow === 'off'`, and each has a one-line hint.
  - When `timedNote(server, Date.now())` returns a note, show it as a muted line.
  - Show `draftRefusal(server, draft)` as a hint when the mode is not Off.
  - Change the summary to `AI access · On/Off` plus ` · auto` when `draft.autoAllow !== 'off'`.
  - On submit:
    - If `saveConfirm(server, draft).needed`, open the confirm dialog (render `AutoAllowDialog` from HostEditor, or lift it to App; either is fine, but keep Cancel returning to the editor without saving). Its Enable calls `onSave(toInput(draft), server?.name, draft.autoAllow)`.
    - Otherwise call `onSave(…, draft.autoAllow)` directly.
  - The dialog's `check` needs the hub (`hub.autoAllowCheck`). Pass it in as a prop from App, as today. For a new host (no `server`), the mode select is disabled with the hint "Save the host and trust its key first".
- [ ] **Step 3: `App.tsx`:**
  - `onSave = async (input, original, mode) => { try { await hub.saveServer(input, original, mode) } catch (e) { const m = (e as Error).message; if (!m.startsWith(SAVED_BUT)) throw e; window.alert(\`Saved, but auto-allow was not turned on: ${m.slice(SAVED_BUT.length)}\`) } await reloadServers(); await reloadTunnels(); setEditing(undefined) }`. On a real save error, the thrown error keeps the editor open with its error shown, as it does today.
  - Remove `autoDialog` / `enableAuto` / the card-dialog rendering. Keep `stopAuto`, `stopAll`, `stopPaused` and `resumeAll`. Remove `onAutoAllow` from `HostList`'s props and its Bolt button. The card keeps the chip and the Stop button, which is shown while the host is active or paused.
  - Clear any open editor confirm dialog on the `locked` notification, the same way `autoDialog` was cleared before.
- [ ] **Step 4: Feed:** in `ApprovalPanel`, show a small `sudo` tag on rows where `r.sudo`.
- [ ] **Step 5: Update the e2e** (`desktop/e2e/autoallow.spec.ts`). Keep every existing assertion's intent, but drive it from the editor:
  - Open Edit box → AI access. Choose "15 min" in the Auto-allow select → Save → the confirm dialog appears → wait 600 ms → Enable. The chip then reads `Auto 1[45]m`. An exec runs without a click and shows in the feed. Stop on the card. The next exec waits. Re-enable from the editor, then Lock and unlock: the chip is gone and the next exec waits.
  - Forever from the editor: type `box` into the dialog → Enable → chip `Auto ∞`. Lock and unlock: the banner shows paused and an exec waits. Resume, and the next exec runs.
  - New test: in the editor choose 15 min and tick "Also auto-allow sudo-exec" → Save → the dialog shows the sudo warning → Enable. A `doorCall(socket,'sudoExec',{requestId,client:'e2e',server:'box',command:'echo sudo-auto',description:'e2e'})` returns without a click (check the door method name in `internal/hub/mcpdoor.go`). Then Stop.
  - Read `desktop/e2e/hosts.spec.ts` for how the editor is opened and saved, and reuse its selectors.
- [ ] **Step 6: Docs:**
  - ROADMAP standing decision: "…plain exec only, never sudo-exec, never on root-equivalent hosts…" becomes "plain exec by default; root hosts and sudo-exec only when the human opts in per host".
  - README Auto-allow section: the control is now in the Host editor (AI access), applied on Save. Describe the two opt-ins and their honest risks (full root; a planted `sudo` function can capture passwords). Stop is on the card.
  - PRODUCT.md: the mouse-only control list still includes Enable. Update any "never sudo" or "never root" wording.
  - CLAUDE.md:
    - auto-allow bullet: opt-ins, arming on `servers.save`, protocol 7, `servers.save {…, autoAllow}`;
    - Desktop section: the editor controls, and the card shows only Stop.
  - The spec's Status line: add "Amendment implemented (plan `plans/2026-09-29-auto-allow-editor.md`)."
- [ ] **Step 7: Run everything.** `cd desktop && npm run typecheck && npm test`, then `npm run e2e -- e2e/autoallow.spec.ts` (twice), then the whole `npm run e2e`. Also `go vet ./... && go test -race -count=1 -timeout 10m ./...`.
- [ ] **Step 8: Commit.** Two commits: `feat(desktop): auto-allow in the Host editor, applied on Save; card keeps Stop` and `docs: auto-allow opt-ins and editor control`.
