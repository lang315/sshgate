# sshgate: A setting for the soft-lock limit, "No limit" included — Design

Date: 2026-10-12
Status: Approved 2026-10-12 (scope, UI/UX and this document approved in chat; remembering the master key is out).
Depends on: `2026-10-02-soft-lock-design.md` and `2026-10-03-manual-soft-lock-design.md`. Everything there still holds unless this document changes it by name; where they disagree, this document wins.

This spec reverses these statements of the soft-lock spec:

- Decisions, "Maximum soft-lock duration: 24 hours, a constant, then a hard lock; forever grants included". The limit is now a vault setting: 24 hours (the default), 3 days, 7 days, or no limit.
- Idle lock and the ceiling: "`maxSoftLock = 24 * time.Hour`, a constant with no flag and no setting".
- Security rules: the key in memory under a forever grant is "bounded by the 24-hour ceiling". It is bounded by the setting, and not at all under "No limit".
- Known limits: "the soft lock reaches 24 hours" becomes "the soft lock reaches the limit set in Settings, if any".

## Goal

Let the author leave an auto-allow task running for longer than 24 hours while the app is locked, without unlocking once a day.

Why: a forever grant keeps running through a soft lock for at most 24 hours. A task that runs over a weekend stops on Saturday and waits for an unlock and a Resume. Today the only way around it is to come back and unlock: `Unlock` clears `softLockedAt`, so the next soft lock counts from zero again.

The change: the limit moves from a constant into the vault, and the author picks it from a new Settings dialog. The default stays 24 hours, so nothing changes for a vault that never sets it.

What this protects is unchanged: the app's window. Under "No limit" the key stays in memory for as long as a grant is live and the hub runs.

Exit gate: with "No limit" set and a forever grant on a real host, the author locks and leaves a Claude Code task running for more than 24 hours. `audit.jsonl` shows `exec` records with `approval: "auto"` more than 24 hours after the `softLock` record, and no `autoAllowOff` with reason "soft lock limit". **Stop auto-allow and lock** then makes the next `exec` fail with the locked error.

## Scope

In:
- `config.File` gains `softLockLimit`. It sits under the vault's MAC.
- A new UI-door request, `settings.setSoftLockLimit {limit}`, and `status.softLockLimit` while unlocked.
- The hub's two ceiling checks use the limit taken when the soft lock began.
- A Settings dialog in the desktop app, opened from the tab strip, with one setting.
- UI-door protocol 12.

Out:
- Remembering the master key across restarts (Electron `safeStorage`, OS keychain). Discussed in chat 2026-10-12. On Windows, DPAPI and Credential Manager are open to every process of the same user, as Termius's own `localKey` is. If it is ever done, it is opt-in and gets its own spec.
- Unlock with Touch ID or Windows Hello. A separate feature.
- Keeping the machine awake while a grant is live (`powerSaveBlocker`). A separate change.
- A per-host limit. The limit is the hub's, like the soft lock itself.
- Showing on the unlock screen when the soft lock will end.
- `hub --cli`: it has no grants and never soft-locks. Unchanged.
- The MCP door: no access to the setting.

## The setting

| `limit` (wire) | Stored `softLockLimit` | Duration |
|---|---|---|
| `"24h"` | absent (`""`) | 24 hours, the default |
| `"3d"` | `"3d"` | 72 hours |
| `"7d"` | `"7d"` | 168 hours |
| `"none"` | `"none"` | no limit |

- Stored on `config.File` as `SoftLockLimit string json:"softLockLimit,omitempty"`. The default is stored as absent, so a vault that never changes it keeps the same bytes and still opens in an older sshgate.
- `computeMAC` marshals the whole `File`, so the field is MAC-covered with no change there. A process that edits it fails the MAC check like any other edit: on the next reload while unlocked, and on the next sweep under soft lock (which ends every grant and hard-locks, as today).
- A value the hub does not know (a file written by a newer sshgate) reads as 24 hours: fail closed, to the shortest limit.
- Downgrade: an older sshgate does not know the field, drops it when it computes the MAC, and refuses the vault as tampered. Setting the limit back to 24 hours removes the field and makes the vault readable by an older sshgate again. The README says so.
- `vault.create` clears the field, as it clears `autoAllow`, `autoAllowRoot` and `autoAllowSudo`. A KDF-less file was never MAC'd, so a value planted in it must not survive into the new vault.

## Hub

- `maxSoftLock` stays as the default, `24 * time.Hour`. A new `softLockLimitOf(f *config.File) time.Duration` maps the stored value; `0` means no limit.
- No new hub state. One helper, `pastLimitLocked(now time.Time) bool`, reads the limit from the current `h.deps.File` (`softLockLimitOf`) and replaces both comparisons: `aiLockedLocked` (`softlock.go:133`) and `lockIfIdle` (`idle.go:64`). It is false under "none". The two call sites cannot drift apart.
- Why a live read and not a value copied at `enterSoftLocked`: a copied `time.Duration` field would have zero, the field's default, mean "no limit", so any path that soft-locks without that copy (the test helper at `softlock_test.go:24` does) would run unbounded. With a live read the zero value, an absent field, is 24 hours: fail closed by construction. `deps.File` is MAC-verified: at unlock, on every reload while unlocked, and under soft lock by the sweep's reload (`vaultKeyLocked`). No writer runs during a soft lock, so for one hub the two reads give the same value. If a second hub that has the password lowers the limit, the next sweep picks it up.
- `settings.setSoftLockLimit {limit}`:
  - Params are strict (`strictParams`, key `limit` only). `limit` must be one of the four wire values; anything else is -32602.
  - It needs an unlocked vault (`writeKeyLocked`): `ErrLocked` while locked, soft or hard, and "create a vault first" with no vault. So the limit cannot be raised or lowered during a soft lock. **Stop auto-allow and lock** remains the stop.
  - It holds `h.mu` across `config.Update` and the reload that follows, like `servers.save`. A failed reload is returned; the write stands.
  - It writes one config audit record, `action: "softLockLimit"`, with a new `ConfigRecord` field `limit` (the wire value). Setting the value already stored writes nothing and still succeeds.
  - It does not end or change any grant.
  - It counts as UI activity, like every UI-door request but `status`.
- `status` gains `softLockLimit` only while unlocked. It reports the limit in force as a wire value, so an unknown stored value reports `"24h"`, not the raw string. "Unlocked" means `deps.MasterKey != nil`. While hard-locked the file's MAC is unchecked, and the unlock screen does not use the value.
- `ProtocolVersion` goes from 11 to 12. A renderer built for 11 would show "up to 24 hours" in the consent dialog while the vault says otherwise; the `hello` check refuses that pairing.

## Desktop

- `shared/protocol.ts`: `Status.softLockLimit?: SoftLockLimit`, the type `SoftLockLimit = '24h' | '3d' | '7d' | 'none'`, `'settings.setSoftLockLimit'` in `REQUEST_METHODS`, `PROTOCOL_VERSION = 12`. `transport.ts` gains `hub.setSoftLockLimit(limit)`.
- The Settings dialog is specified in **UI and UX** below.
- `AutoAllowDialog.tsx`: the forever text's "for up to 24 hours" and "the 24-hour limit" come from `status.softLockLimit` through one pure function, `limitText(limit)` in `autoallow.ts`. Under "No limit" the sentence reads "the AI keeps running on this host while the app is locked, with no time limit", and the limit drops out of the Resume list.
- The renderer still calls the hub only on real UI actions. Opening the dialog reads the `status` it already has.

## UI and UX: the Settings dialog

The mockups were reviewed in the brainstorming browser companion on 2026-10-12. The final one is `.superpowers/brainstorm/62302-1791775849/content/final.html`, gitignored and local only.

### Entry point

- A gear icon button in the tab strip's actions (`App.tsx`), between `ThemeControl` and Lock. The order is: AI, theme, Settings, Lock.
- It is a plain `.btn` holding a new `SettingsIcon` from `icons.tsx`, drawn in the same 24-unit stroke style as the other icons. It has `aria-label="Settings"` and `title="Settings"`.
- It is rendered only while `ready`, meaning unlocked with a vault. Under a lock the unlock screen covers the strip anyway.

### Frame, built to grow

- One modal, `.modal` > `form.dialog` (520 px, the existing dialog style). The title row is `.dialog-icon` with the gear, then `h3` "Settings".
- The body is a list of sections. Each section is a `section.settings-section`: a heading (`h4`, 13 px, weight 600, a `--divider` rule under it), then rows.
- A row (`.settings-row`) is a two-column grid: the label on the left (`<label htmlFor>`) and the control on the right (180 px). Help text spans both columns in `.muted`. When the dialog is narrower than 420 px, the row stacks, label above control.
- This release has one section, **Auto-allow**, with one row. A later setting adds a row or a section, and the dialog scrolls (`max-height: 90%`, as `.dialog` already does).
- Rejected for now: a sidebar of sections, like macOS System Settings. With one section it would show a sidebar holding a single entry. Revisit when there are about three sections.
- Save and Cancel cover the whole dialog. There is no per-row instant apply, because the riskiest change has to pass the Save gate below.

### The limit row

- Label: "Keep running while locked". Control: the native `.select` (as in the host editor's Auto-allow select), with the options "24 hours (default)", "3 days", "7 days", "No limit". It opens on `status.softLockLimit`.
- Help text under the row depends on the chosen value:
  - for 24 hours, 3 days and 7 days: "Then the app locks fully and auto-allow stops. Forever grants wait for Resume."
  - for No limit: "Auto-allow stops only with Stop auto-allow and lock, a restart or a crash."
- With "No limit" chosen, the select gets a `--danger` border (`.select.danger`).

### States

| Choice compared with the value in force | Notice | Save |
|---|---|---|
| The same | none | disabled (nothing to save) |
| Lower | none | `.btn`, enabled |
| Higher, 3 or 7 days | amber `.banner-wait` (new, `--wait-tint` and `--wait`, mirroring `.banner-danger`) | `.btn.danger-outline` |
| No limit | red `.banner-danger` | `.btn.danger-outline` |

- The notice text: "While the app is locked, the AI keeps running on hosts with auto-allow for up to <3 days | 7 days>" (or "for as long as the app runs" for No limit) ". Any program running as your user can use that access too."
- After that text comes "On auto-allow now:" and the hosts as a `ul.files-sample.mono`, the list style the consent dialog uses for tunnels. The hosts come from `autoHosts(servers, now)` in `autoallow.ts`. With none: "No host is on auto-allow now. The limit applies from the next grant."
- The notice sits in an `aria-live="polite"` region, so a screen reader reads it when the select changes.
- **No typed confirmation.** The author decided in chat on 2026-10-12 that this choice is theirs and should not cost a second step. The red notice, the red Save and the delay below are the only friction. (The forever grant's typed host name in `AutoAllowDialog` is a different decision and is unchanged.)

### Keys, focus and timing

- Cancel is `.btn.primary`, has `autoFocus`, and is the form's submit: Enter cancels, as in `AutoAllowDialog`. Esc cancels too.
- Save is mouse-only (`tabIndex={-1}`, `blockKeyboardActivation`). It is disabled for `ALLOW_DELAY_MS` (500 ms) after the dialog opens and after the notice appears or changes. That uses `ListChanges`, keyed on the chosen value and the host list, as the consent dialog's Enable does. A click meant for something that moved cannot land on Save.
- The select is keyboard-reachable, and its native menu, type-ahead and arrow keys are unchanged.

### Save, errors, lock

- Save calls `hub.setSoftLockLimit(limit)` and then the app's existing `refresh()`, so `status.softLockLimit` is current. On success the dialog closes. Both calls are real UI actions.
- A failed save shows the error as a `.error` line above the actions. The dialog stays open with the choice kept, and Save waits out the delay again.
- The dialog is rendered only while `ready`, like the host editor, so a lock (idle or manual) unmounts it without saving.

### Theme

- Only CSS tokens, no raw colours, so dark, light and Auto work with no extra rules. New classes: `.settings-section`, `.settings-row`, `.banner-wait`, `.select.danger`.

## Security

- Only someone who knows the master password can change the limit: it is written only while unlocked, and it lives under the vault's MAC. A planted or edited value fails closed. Under soft lock a replayed older copy of the vault (a lower `Revision` with a valid MAC) is treated as a failed reload: every grant ends and the hub hard-locks. While unlocked, or across a restart, a replayed copy is accepted, as for every vault field; this is a known limit.
- No other file stores it. The setting is not in `localStorage`, an environment variable or a flag. In the packaged app the `SSHGATE_*` variables are already ignored. TablePro keeps its MCP approval settings in plain `UserDefaults`, where any same-user process can flip them; that is the pattern this design avoids.
- Under "No limit", a forever grant on an unattended machine lets the AI, and any process running as the same user that reaches the MCP door, run on that host for as long as the hub runs. The master key stays in memory for that time. This is the honest limit the soft-lock spec stated for 24 hours, now without a bound. The dialog says so before Save.
- Everything that ends a soft lock today still does: **Stop auto-allow and lock** (no password), Stop on a host card or Stop all auto-allow before locking, the last grant ending, a timed grant's deadline, a renderer crash (`recoverRenderer` hard-locks), a hub restart, and a changed or tampered vault.
- Nothing here is platform-specific. macOS, Windows and Linux behave the same.

## Known limits

Unattended work under "No limit" still stops when the renderer crashes, the hub restarts, the machine sleeps, a timed grant ends, or the AI needs a host or a `sudo-exec` that the grant does not cover.

A replayed older vault copy while the app is unlocked, or across a restart, can set the limit back to an older value; under soft lock it hard-locks instead.

## Testing

Go (`internal/hub`, `internal/config`):
- `softLockLimitOf`: each stored value, absent, and an unknown value (24 hours).
- With `softLockMax` at 3 days: one second short of 72 hours, nothing happens; at 72 hours, the existing ceiling behaviour (`softLockLimit` note, grants ended with "soft lock limit"). Under "none", `softLockedAt` a year back: the tick keeps the soft lock, an auto exec runs, and `aiLockedLocked` is false for the granted host.
- The limit is read live: a change made while unlocked applies to the next check; an absent field and an unknown value both read as 24 hours; a soft lock entered through the test helper (`softlock_test.go:24`, not through `enterSoftLocked`) is still bounded by 24 hours.
- Tamper during a soft lock: a vault on the 24-hour default soft-locks; `softLockLimit` is edited to `"none"` on disk; the next sweep sees the file differ, the reload fails the MAC, the hub hard-locks, and every grant ends with an `autoAllowOff` record.
- `settings.setSoftLockLimit`:
  - an unknown key, a non-string, or an unknown value is -32602;
  - it is refused hard-locked, soft-locked (`ErrLocked`) and with no vault;
  - it writes one `softLockLimit` record, and none when the value is unchanged;
  - `"24h"` removes the field from the file.
- `status.softLockLimit` is present unlocked and absent locked.
- `vault.create` on a KDF-less file that carries `softLockLimit: "none"` leaves no field.
- The existing ceiling tests (`softlock_test.go:283, 293, 337, 633`) keep testing the 24-hour default.

Unit (`desktop/test`): `limitText` for each value; the states rule as a pure function (same: Save disabled, no notice; lower: no notice; higher: amber; "none": red); the help text per value.

E2e (`desktop/e2e/settings.spec.ts`): the gear is absent on the unlock screen and present after unlock; Enter in the dialog cancels; set "No limit" (the red notice lists `box`, Save is disabled for the first 500 ms); the audit tab shows the `softLockLimit` record; the vault file on disk (`SSHGATE_STORE`) has `"softLockLimit": "none"`, and after a lock and an unlock the dialog shows "No limit"; the consent dialog's forever text has no "24 hours". The 24-hour wait itself is the exit gate, not an e2e test.

## Documentation

- README: "Locking" and the auto-allow section (lines 90, 102, 109) say the limit is set in Settings, 24 hours by default, and that "No limit" ends only by **Stop auto-allow and lock**, a restart or a crash. Add the downgrade note.
- PRODUCT.md line 26: "for up to 24 hours" becomes "for up to the limit set in Settings (24 hours by default)".
- CLAUDE.md: the `maxSoftLock` sentence and protocol 11 change; add `settings.setSoftLockLimit` to the UI-door method list.
- The soft-lock spec gets a Status line pointing here.

## Decisions made in chat (2026-10-12)

- The author asked for a permanent soft lock ("khóa vĩnh viễn") chosen in a settings dialog. Bounded choices are offered alongside it, and 24 hours stays the default.
- Settings UI: a native select (layout C), one dialog with sections (S1) so later settings can be added, and no typed confirmation for "No limit" (the author's call).
- The setting lives in the vault, not in an OS store. TablePro keeps settings in plain `UserDefaults`, and Termius keeps a `localKey` in Credential Manager with a silent plaintext fallback. Both fall to a same-user process, so neither pattern is used here.
- Rewriting the desktop app in Swift for macOS: rejected. The security logic is in the Go hub, Windows needs Electron anyway, and the Data Protection Keychain needs a provisioning profile that an ad-hoc build cannot have.
- No Apple Developer ID: the project is open source and ships ad-hoc signed. Nothing in this spec depends on signing.
