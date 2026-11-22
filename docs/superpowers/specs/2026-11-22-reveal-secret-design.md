# Show a saved secret, behind Touch ID or the master password

Date: 2026-11-22. Status: approved in conversation (show saved secrets; Touch ID, with the master password where Touch ID is off; no separate PIN). Details marked "my call" are the author's.

## Why

The person saves server passwords in sshgate and sometimes needs one for work outside sshgate. Today a saved secret can never be read back (`ServerInput` secrets are write-only; the editor says "Saved secrets are never shown"), so a forgotten password means going back to wherever it came from.

This changes a standing rule. `2026-09-25-desktop-ui-redesign-design.md` says "Saved secrets are never shown" and "secrets never displayed", with no recorded reason. `2026-09-25-desktop-slice2a-design.md`'s "write-only on the wire" row gives a reason only for clearing secrets on a host change, and that half stands. This spec replaces the "never shown" rule for the human, in the app only.

## Goals

- In the host editor, show one saved secret of one server: `password`, `suPassword`, `sudoPassword` or `keyPassphrase`.
- Each showing needs a fresh proof: Touch ID when it is on, else the master password typed again.
- One audit record per attempt.

## Non-goals

- No separate PIN. The Mac has Touch ID; a PIN would be a second secret with its own set, reset and attempt-limit flows. If one is ever added, its hash must not survive a master password change (bind it to the KDF verifier, or delete it in that flow, as for `touchid.json`).
- No Copy button, no clipboard write (my call): clipboard managers and Universal Clipboard would keep the value outside the vault.
- No reveal on the MCP door, in `hub --cli`, or in `--host` mode.
- No master password change flow.

## Anti-goals

- No path by which the AI can obtain a saved secret.
- No state in which an unlocked app shows a secret without a proof made for that showing: no "verified for N minutes" in the app or the hub.
- A secret never goes into a log, stderr, an error string, an audit record, a command-line argument, or an editable field.

## Approaches

1. **The hub checks the master password on every call; Touch ID supplies that password (chosen).** `servers.reveal` carries the master password. The hub derives it and checks the verifier, as `Unlock` does. Touch ID unseals the same password from `touchid.json`, so one check in the hub covers both, and a master password change breaks Touch ID reveal the way it breaks Touch ID unlock (wrong password, file deleted).
2. **The app checks (Touch ID or password), then calls the hub.** Rejected: the UI door is full-privilege, so the hub would trust any caller that skipped the check.
3. **A separate PIN checked by the hub.** Rejected for now (see Non-goals).

## Hub: `servers.reveal`

UI door only. Request: `{server, field, password}`, strict params (`strictParams`); `field` is one of `password`, `suPassword`, `sudoPassword`, `keyPassphrase`, else -32602. Reply: `{value}`.

Order:

1. Refuse unless the vault is unlocked: `deps.MasterKey != nil` (soft lock refuses, `ErrLocked`; no vault, "create a vault first"). Counts as UI activity like every request but `status`.
2. The server must exist (hidden servers too: the UI sees them). It must hold the matching `Enc*` field, else `no saved <field>`.
3. One reveal at a time (my call: a hub-level flag; a second call while one derives gets `busy`), so parallel calls cannot parallelize guessing.
4. Copy the KDF under `h.mu`, derive the key from `password` without it (Argon2, about 1 s), then under `h.mu` refuse when the KDF changed (`the vault changed while the password was checked; try again`) and check `KDF.Verify`; failure is `wrong master password`, the same text `Unlock` uses, so the app's Touch ID code can recognise it. Do not call `Hub.Unlock`: on an unlocked hub it does more than verify.
5. Decrypt the field with `deps.MasterKey` under its stored label and AAD. The wire names map to the stored field names: `password` → `encPassword`, `suPassword` → `encSuPassword`, `sudoPassword` → `encSudoPassword`, `keyPassphrase` → `encKeyPassphrase` (label `<name>/encPassword` and so on, as `config/server_input.go` and `mcpserver/resolve.go` use). Clear the derived key.
6. Audit every attempt that passed step 2: `kind: "config"`, action `secretReveal` (shown) or `secretRevealDenied` (wrong master password), `server`, and a new `ConfigRecord.Field` (`field`, the wire name). Never the value or the password. Two actions instead of an `outcome` field (my call): `ConfigRecord` has no outcome, and `audit.read`'s `outcomes` filter reads exec records only. The app's audit view gets a row for both actions (`<server>: showed <field>` / `<server>: wrong master password for <field>`; wording the builder's).

No attempt limit beyond the derive time (my call): a locked hub already takes unlimited `unlock` guesses at the same cost.

Protocol 16 (`ProtocolVersion` in `idle.go`, the app's constant with it).

## Touch ID helper

`sshgate-touchid reveal`: the same as `unlock` (same stdin, exit codes, `.biometryCurrentSet` key) but `localizedReason = "show a saved password in sshgate"`, so the dialog does not say "unlock the sshgate vault". Fixed text, no server name from the caller. The real helper is a hand check, as for unlock.

`touchid.Manager.Reveal(ctx, server, field)` (name the builder's; my call on the shape): runs the `reveal` helper, calls `servers.reveal` itself with the unsealed password, wipes it, and returns only the value, as `Manager.Unlock` calls the hub itself. So the master password never reaches the editor, and the stale-file rule stays in one place: when `servers.reveal` answers `wrong master password`, the manager deletes the file (`Disable`) and returns `ErrReset`. Exit codes as `Unlock` (2 cancelled, 3 key gone and the file deleted, 4 not usable).

## App

- Host editor (`view/editor.go`): next to each secret field that shows the `saved` placeholder, a `Show` button. It is absent for a field with nothing saved and for a new host.
- Click opens a small dialog, shaped like the unlock screen (my call): a master password field (Enter submits; the field is cleared when the dialog closes), Cancel, and, when Touch ID is on, a `Use Touch ID` button. With Touch ID on, the dialog asks Touch ID once by itself when it opens. Cancelling the Touch ID prompt (exit 2; the helper has no system fallback button) leaves the dialog open with the password field, as a cancel at unlock leaves the typed field. Touch ID reset or not usable: the dialog shows why and stays open for the password.
- The value appears as read-only text under the field, never in the input (the input stays empty, which means "keep"). It hides after 15 s (my call), and at once on lock, on leaving or saving the editor, and on a hub restart.
- The editor hint becomes `Leave a field empty to keep it.`
- Errors are shown as the hub sends them; `wrong master password` from the typed dialog keeps the dialog open.

## Tests

- Hub: shows each field with the right password; `wrong master password` and a `secretRevealDenied` record; locked, soft-locked and no-vault refusals; unknown server and empty field; strict params; a KDF change during the derive; the record never holds the value or password (grep the audit file for both); the MCP door has no `servers.reveal` (method not found).
- App: Touch ID path with `touchid/testdata/fake-touchid.sh` (a `reveal` verb), stale password deletes `touchid.json`; typed path; the value hides after 15 s (`tt.Advance`) and on lock; the input stays empty.

## Left to the builder

Button and dialog layout in the house style; where the busy flag lives; the exact `Manager` method name.
