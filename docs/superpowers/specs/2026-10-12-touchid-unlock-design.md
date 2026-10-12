# sshgate: Unlock with Touch ID on macOS — Design

Date: 2026-10-12
Status: Approved in chat 2026-10-12 (approach, sections 1–3, and the exception to the secret-persistence rule); this document awaits review.
Depends on: `2026-10-12-soft-lock-limit-design.md` (the Settings dialog). Everything there still holds unless this document changes it by name.

This spec reverses two "Out" items of the soft-lock-limit spec:

- "Remembering the master key across restarts (Electron `safeStorage`, OS keychain)". The master **password** is now remembered, opt-in, on macOS only, sealed to the Secure Enclave and gated by Touch ID. Not with `safeStorage`, and not on Windows.
- "Unlock with Touch ID or Windows Hello". Touch ID is in; Windows Hello stays out.

It also makes one exception to `CLAUDE.md`'s "no secret persistence outside the encrypted store" (see Security).

## Goal

Unlock the vault with a fingerprint instead of typing the master password. The password keeps working everywhere it works today.

Why: the vault locks after 15 minutes idle, on Lock, and on every app or hub restart. Each unlock means typing a long master password.

Exit gate: on the author's Mac, with the packaged app, the author turns Touch ID on in Settings, quits and reopens the app, and unlocks with a fingerprint and no typing. After adding a fingerprint in System Settings, the next Touch ID unlock fails with the "fingerprints changed" message, `touchid.json` is gone, and the password still unlocks.

## Scope

In:
- A Swift helper, `sshgate-touchid`, that seals and opens the master password with a Secure Enclave key.
- `desktop/src/main/touchid.ts`: the main-process side, its IPC and its file.
- A Touch ID row in the Settings dialog, and a Touch ID button plus auto-prompt on the unlock screen.
- `package-mac.sh` and a dev build script for the helper.

Out:
- Any change to the hub, the Go code, or the UI-door protocol (stays 12). The hub's `unlock` already takes a password.
- Windows Hello, Linux.
- Apple Watch unlock, or the device passcode as a fallback (`.biometryCurrentSet` only).
- Storing the derived key instead of the password.
- Developer ID signing. The probe of 2026-10-12 showed that a CryptoKit Secure Enclave key works with ad-hoc signing, and that a Keychain item with a biometry ACL does not (`-34018 A required entitlement isn't present`).

## The helper: `sshgate-touchid`

Source `desktop/native/touchid/main.swift`, one file, CryptoKit and LocalAuthentication only. It never touches the disk: main owns the file. Subcommands:

| Command | stdin | stdout | Exit |
|---|---|---|---|
| `available` | — | — | 0 usable, 4 not |
| `enroll` | the password, raw bytes | sealed JSON | 0, 4, 1 |
| `unlock` | sealed JSON | the password, raw bytes | 0, 2, 3, 4, 1 |

Exit codes: 0 ok; 1 other error (one line on stderr); 2 the user cancelled; 3 the key no longer works (fingerprints changed); 4 Touch ID not usable now (no sensor, lid closed, no fingerprint enrolled, locked out).

`available`: `LAContext().canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics)` and `SecureEnclave.isAvailable`.

`enroll`:
1. Access control: `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`, flags `[.privateKeyUsage, .biometryCurrentSet]`.
2. `SecureEnclave.P256.KeyAgreement.PrivateKey(accessControl:)`. Creating it needs no fingerprint.
3. A fresh software `P256.KeyAgreement.PrivateKey` (ephemeral) agrees with the SE public key. HKDF-SHA256, salt empty, info `"sshgate touchid v1"`, 32 bytes. AES-GCM seals the password.
4. Prints `{"v":1,"key":<b64 SE dataRepresentation>,"eph":<b64 ephemeral public raw>,"ct":<b64 AES-GCM combined>}`. The ephemeral private key is dropped.

`unlock`:
1. `LAContext` with `localizedReason = "unlock the sshgate vault"`.
2. `SecureEnclave.P256.KeyAgreement.PrivateKey(dataRepresentation:authenticationContext:)`, then `sharedSecretFromKeyAgreement(with: eph)`. This step shows the Touch ID dialog.
3. Same HKDF, then `AES.GCM.open`, then the bytes go to stdout with no newline.

Error mapping: `LAError.userCancel`, `.appCancel`, `.systemCancel` → 2. `.biometryNotAvailable`, `.biometryNotEnrolled`, `.biometryLockout` → 4. A key that fails to load or to agree after a fingerprint change → 3. Which error CryptoKit raises for a changed fingerprint set is not known yet; the live check (Testing) finds it and the mapping follows. Until then, any failure of step 2 that is not an `LAError` is 3. A malformed stdin, or an AES-GCM open failure → 1.

Build: `desktop/scripts/build-touchid.sh` runs `swiftc -O` into `<repo root>/sshgate-touchid` (gitignored, beside `sshgate`), then `codesign --force --sign -`. `package-mac.sh` runs it and copies the binary to `Contents/Resources/sshgate-touchid` before its `codesign --deep`.

## Main process: `desktop/src/main/touchid.ts`

Darwin only. On any other platform every call answers `available: false` and nothing spawns.

Helper path, like `hubLaunch`: `SSHGATE_TOUCHID_HELPER` (dev/test knob, ignored when packaged, like every `SSHGATE_*`); else `<repo root>/sshgate-touchid` when unpackaged, `Contents/Resources/sshgate-touchid` when packaged. A missing helper means "not available".

File: `app.getPath('userData')/touchid.json`, mode 0600, written atomically (temp file and rename). Content: `{storePath, sealed}`, where `sealed` is the helper's JSON. `storePath` comes from the hub's `status`. A file whose `storePath` differs from the current `status.storePath` counts as "not enabled" and is left alone.

IPC (each through `guard(isTrusted, …)`, and exposed by `preload.ts` and `renderer/transport.ts` like the hub calls):

- `touchid:status` → `{available, enabled}`. Runs `available` at most once per call.
- `touchid:enable {password}`: refused unless `status.hasVault`. Calls the hub's `unlock(password)` to check it (while unlocked this only re-checks; a wrong password leaves the hub as it was). On success it runs `enroll` with the password on stdin and writes the file. A wrong password writes nothing and returns the hub's error.
- `touchid:disable`: deletes the file. Needs nothing.
- `touchid:unlock`: runs `unlock` with the file's `sealed` on stdin, sends stdout to the hub's `unlock`, and returns the hub's reply as is (`ranWhileLocked` included). The password never goes back to the renderer.

Rules:
- One helper at a time. A second `touchid:unlock` while one runs is refused with "Touch ID is already asking".
- A password `unlock` that succeeds through the relay, or a hub state other than `running`, kills a running helper (its dialog closes).
- Exit 3, or the hub answering `wrong master password` to a Touch ID unlock, deletes the file. The reply is the error `Touch ID was turned off because your fingerprints changed or your master password is different. Unlock with your password, then turn it on again in Settings.`
- Exit 2 returns a "cancelled" result, which the renderer shows as nothing. The file stays.
- Exit 4 returns `Touch ID is not available right now. Unlock with your password.` The file stays.
- Exit 1 returns `Touch ID failed.`; the helper's stderr goes to main's stderr, never to the renderer. The file stays.
- Password bytes are passed as stdin buffers, never on argv or in the environment, and main keeps no copy after the call.

## UI and UX

### Settings (`SettingsDialog.tsx`, `settings.ts`)

A new section **Unlock**, before Auto-allow, shown only on macOS. One row: a switch, "Unlock with Touch ID".
- Turning it on shows a "Master password" field under it. Save checks the password and enrolls, then saves the soft-lock limit if that changed too. On a Touch ID error the dialog stays open with the error, and the limit is not saved.
- Turning it off and saving deletes the file. No password needed.
- Touch ID not available: the switch is disabled, with "Touch ID is not available on this Mac right now." under it. An already-enabled setting can still be turned off.
- Save is enabled when the limit changed, or the Touch ID switch changed (turning on also needs a non-empty password). The 500 ms delay after a notice change stays as it is.
- Enter still means Cancel, in the password field too, as everywhere in this dialog.

### Unlock screen (`Unlock.tsx`, `App.tsx`)

When Touch ID is enabled and available: an "Unlock with Touch ID" button above the password field. The password field stays.

Auto-prompt: at most once per locked episode (from the lock until the next unlock), Touch ID is asked by itself:
- at app start, if the window has focus;
- on the first `focus` event of the window while locked.

It is never asked at the moment of locking. A cancel ends the auto-prompt for that episode; the button still works. This keeps a cancel, which hands focus back to the window, from opening the dialog again.

An error from Touch ID shows in the existing error line. A cancel shows nothing.

## Security

The exception to "no secret persistence outside the encrypted store": `touchid.json` holds the master password, sealed. What protects it:
- The private key lives in the Secure Enclave and never leaves it. The file holds only a blob the enclave wrapped for this Mac; it is useless on another machine or after the Mac is erased.
- `.biometryCurrentSet`: every open needs a fingerprint enrolled when the key was made. Adding or removing a fingerprint ends the key for good.
- `WhenUnlockedThisDeviceOnly`: no use while the Mac's screen is locked, and no backup or migration.
- The feature is off by default and turned on only in Settings, with the password typed again.

What it does not stop:
- Someone at the unlocked Mac who can get your finger on the sensor.
- Malware running as you. It can already read a typed password, and it can ask the enclave too; macOS shows the Touch ID dialog, but the reason text it shows is chosen by the caller.

Unchanged: the hub's policy, the AI's view, every check in `CLAUDE.md`. The hub cannot tell a Touch ID unlock from a typed one.

`CLAUDE.md`'s Conventions line gets the exception, by name.

## Known limits

- Idle lock while the window keeps focus (the author walked away with sshgate in front): coming back fires no `focus` event, so no auto-prompt; the button works.
- A hub crash restart does not auto-prompt unless the window is then focused.
- Changing the master password has no flow today. If one is added, it must delete `touchid.json`.
- Every rebuild re-signs the helper ad-hoc; the probe showed the enclave blob survives that.

## Testing

Unit (vitest, CI on Linux, a fake helper as a shell script and a fake hub):
- `touchid.ts`: enable with a wrong password writes nothing; with the right one writes the file, 0600, with `storePath`; unlock passes the helper's stdout to the hub's `unlock` byte for byte and returns its reply; exit 3 deletes the file; the hub's `wrong master password` deletes the file; exit 2 keeps it and returns cancelled; a second unlock while one runs is refused; a password unlock kills the running helper; another `storePath` reads as not enabled; non-darwin answers not available and spawns nothing; the knob is ignored when packaged.
- `settings.ts`: Save enabled by a Touch ID change alone; not when turning on with an empty password.
- The auto-prompt rule (once per episode, never at lock time, none after a cancel), as a pure function in `shell.ts` or a sibling.

E2E `desktop/e2e/touchid.spec.ts`, with `SSHGATE_TOUCHID_HELPER` set to the fake helper and a temporary `--user-data-dir`, so the real `touchid.json` is never touched:
- Turn on in Settings with the password, Lock, blur and focus the window: the vault unlocks with no typing.
- The fake helper set to exit 3: the message shows, the file is gone, the password unlocks.

Live, on the author's Mac (not in CI; needs a finger):
- The real helper: `available`, `enroll` then `unlock`, Cancel gives exit 2.
- Add a fingerprint, `unlock` gives exit 3; record the error CryptoKit raised and fix the mapping to it. Remove the fingerprint again.
- The packaged app: the exit gate above.

## Documentation

- `CLAUDE.md`: the helper and its build in Commands; `touchid.ts`, the Settings row and the unlock button in the `desktop/` section; the Conventions exception.
- `README.md`: Touch ID unlock, how to turn it on, what it stores.
- `.gitignore`: `/sshgate-touchid`.

## Decisions made in chat (2026-10-12)

- Touch ID is a shortcut: either the password or Touch ID unlocks.
- No Apple Developer account: the Secure Enclave route works ad-hoc (probe: create, seal, rebuild and re-sign, open from a `node` child process; the author saw the Touch ID dialog with the reason text).
- Approach A: the helper runs from Electron main; the hub does not change.
- The switch lives in Settings, off by default.
- The unlock screen asks Touch ID by itself, on app start and on the first focus while locked, once per episode.
