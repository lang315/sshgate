# Unlock with Touch ID Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unlock the sshgate vault on macOS with a fingerprint, as an opt-in shortcut next to the master password.

**Architecture:** A Swift helper, `sshgate-touchid`, seals the master password to a Secure Enclave key that needs Touch ID. It never touches the disk. Electron main (`desktop/src/main/touchid.ts`) runs the helper, keeps `touchid.json` in `userData`, and sends the opened password to the hub's existing `unlock`. The renderer only gets results: the password never goes back to it. The hub and the UI-door protocol do not change.

**Tech Stack:** Swift (CryptoKit, LocalAuthentication, `swiftc`), Electron 44 main/preload, React 19 renderer, vitest, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-12-touchid-unlock-design.md`

## Global Constraints

- No change to Go code, `internal/hub`, or `PROTOCOL_VERSION` (stays 12).
- No Apple Developer account: the helper is ad-hoc signed (`codesign --force --sign -`).
- Helper exit codes: 0 ok; 1 other error; 2 cancelled; 3 key no longer works; 4 Touch ID not usable now.
- HKDF info string: `"sshgate touchid v1"`. LocalAuthentication reason: `"unlock the sshgate vault"`. Access control: `kSecAttrAccessibleWhenUnlockedThisDeviceOnly` with `[.privateKeyUsage, .biometryCurrentSet]`.
- File: `app.getPath('userData')/touchid.json`, mode 0600, written via a temp file and rename, content `{storePath, sealed}`.
- Dev/test knob `SSHGATE_TOUCHID_HELPER`, ignored when packaged (`app.isPackaged || !process.defaultApp`).
- The password goes renderer → main → helper stdin / hub `unlock`. It never goes on argv, in the env, back to the renderer, or into a log.
- Copy, verbatim:
  - `Touch ID was turned off because your fingerprints changed or your master password is different. Unlock with your password, then turn it on again in Settings.`
  - `Touch ID is not available right now. Unlock with your password.`
  - `Touch ID failed.`
  - `Touch ID is already asking`
  - `Touch ID is not turned on`
  - `Touch ID is not available on this Mac right now.`
  - Button `Unlock with Touch ID`; switch `Unlock with Touch ID`; section heading `Unlock`.
- Auto-prompt: at most once per locked episode; at app start only if the window has focus and the app was never unlocked in this renderer; else on the window's `focus` event while locked; never at lock time.

## Review Focus

1. A password with non-ASCII, spaces or a trailing newline must come back byte for byte. The test is in Task 2 (`'p w ü\n'`).
2. A hand-edited or corrupt `touchid.json` must read as "not turned on", with no crash. The test is in Task 2.
3. Pressing Touch ID while the hub is down or restarting must show an error, never hang or crash; `status()` must answer `enabled: false`. The test is in Task 2.
4. A wrong password typed into Settings, then the right one, must leave no file after the first try and a file after the second. The tests are in Task 2 (unit) and Task 6 (e2e).
5. If Touch ID fails while the soft-lock limit changes in the same Save, the limit must stay unsaved. The test is in Task 4 (`saveSettings`).

## File Structure

- Create `desktop/native/touchid/main.swift`: the helper (`available`, `enroll`, `unlock`).
- Create `desktop/scripts/build-touchid.sh`: builds `<repo root>/sshgate-touchid`, ad-hoc signed.
- Modify `desktop/scripts/package-mac.sh`: build the helper and copy it into `Contents/Resources/`.
- Modify `.gitignore`: add `/sshgate-touchid`.
- Create `desktop/src/shared/touchid.ts`: the `TouchStatus` and `TouchUnlockResult` types.
- Create `desktop/src/main/touchid.ts`: `touchIdHelper()` (path resolution), the `TouchId` class (status, enable, disable, unlock, cancel), and the copy constants.
- Create `desktop/test/fixtures/fake-touchid.sh`: the fake helper used by vitest and e2e.
- Create `desktop/test/touchid.test.ts`.
- Modify `desktop/src/main/ipc.ts`: the `touchid:*` handlers; cancel the helper after a password `unlock` and on hub stop.
- Modify `desktop/src/main/main.ts`: construct `TouchId` and pass it to `registerIpc`.
- Modify `desktop/src/preload/preload.ts` and `desktop/src/renderer/transport.ts`: the bridge methods and the `touchid` object.
- Modify `desktop/src/renderer/settings.ts`, `SettingsDialog.tsx`, `App.tsx`: the Settings row and Save.
- Modify `desktop/src/renderer/shell.ts`, `Unlock.tsx`, `App.tsx`: the button and the auto-prompt.
- Modify `desktop/e2e/launch.ts`: a `userDataDir` option.
- Create `desktop/e2e/touchid.spec.ts`.
- Modify `CLAUDE.md`, `README.md`, and the spec (two wording fixes, Task 7).

---

### Task 1: Swift helper and its build

**Files:**
- Create: `desktop/native/touchid/main.swift`
- Create: `desktop/scripts/build-touchid.sh`
- Modify: `desktop/scripts/package-mac.sh`
- Modify: `.gitignore`

**Interfaces:**
- Produces: the binary `<repo root>/sshgate-touchid`, with the CLI contract from Global Constraints. `enroll` prints `{"v":1,"key":…,"eph":…,"ct":…}` (base64 fields). `unlock` reads that JSON on stdin and prints the raw password with no newline.

This task cannot run in CI (Linux), and its check needs the author's finger. The executor asks the author before each step that shows a Touch ID dialog.

- [ ] **Step 1: Write the helper**

`desktop/native/touchid/main.swift`:

```swift
// sshgate-touchid: seals the vault's master password to a Secure Enclave key
// that only Touch ID opens (spec 2026-10-12-touchid-unlock-design.md). It never
// touches the disk: Electron main keeps the sealed JSON in touchid.json.
// Exit: 0 ok, 1 other error, 2 cancelled, 3 key no longer works (fingerprints
// changed), 4 Touch ID not usable now.
import CryptoKit
import CryptoTokenKit
import Foundation
import LocalAuthentication

struct Sealed: Codable { let v: Int; let key: String; let eph: String; let ct: String }

func fail(_ code: Int32, _ msg: String = "") -> Never {
  if !msg.isEmpty { FileHandle.standardError.write(Data((msg + "\n").utf8)) }
  exit(code)
}

func boxKey(_ s: SharedSecret) -> SymmetricKey {
  s.hkdfDerivedSymmetricKey(using: SHA256.self, salt: Data(), sharedInfo: Data("sshgate touchid v1".utf8), outputByteCount: 32)
}

func available() -> Bool {
  var err: NSError?
  return SecureEnclave.isAvailable && LAContext().canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &err)
}

// nil: not a cancel or an availability error, so the caller picks the code.
func knownCode(_ e: Error) -> Int32? {
  if let la = e as? LAError {
    switch la.code {
    case .userCancel, .appCancel, .systemCancel: return 2
    case .biometryNotAvailable, .biometryNotEnrolled, .biometryLockout: return 4
    default: return 1
    }
  }
  let ns = e as NSError
  if ns.domain == TKError.errorDomain && ns.code == TKError.Code.canceledByUser.rawValue { return 2 }
  return nil
}

let args = CommandLine.arguments
guard args.count == 2 else { fail(1, "usage: sshgate-touchid available|enroll|unlock") }

switch args[1] {
case "available":
  exit(available() ? 0 : 4)

case "enroll":
  guard available() else { fail(4) }
  let pw = FileHandle.standardInput.readDataToEndOfFile()
  guard !pw.isEmpty else { fail(1, "empty password") }
  guard let ac = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleWhenUnlockedThisDeviceOnly, [.privateKeyUsage, .biometryCurrentSet], nil) else {
    fail(1, "access control")
  }
  do {
    let key = try SecureEnclave.P256.KeyAgreement.PrivateKey(accessControl: ac)
    let eph = P256.KeyAgreement.PrivateKey()
    let box = try AES.GCM.seal(pw, using: boxKey(try eph.sharedSecretFromKeyAgreement(with: key.publicKey)))
    let out = Sealed(v: 1, key: key.dataRepresentation.base64EncodedString(),
                     eph: eph.publicKey.rawRepresentation.base64EncodedString(), ct: box.combined!.base64EncodedString())
    FileHandle.standardOutput.write(try JSONEncoder().encode(out))
  } catch { fail(knownCode(error) ?? 1, "enroll: \(String(reflecting: error))") }

case "unlock":
  guard available() else { fail(4) }
  guard let s = try? JSONDecoder().decode(Sealed.self, from: FileHandle.standardInput.readDataToEndOfFile()), s.v == 1,
        let keyData = Data(base64Encoded: s.key), let ephData = Data(base64Encoded: s.eph), let ct = Data(base64Encoded: s.ct),
        let eph = try? P256.KeyAgreement.PublicKey(rawRepresentation: ephData),
        let box = try? AES.GCM.SealedBox(combined: ct) else { fail(1, "bad input") }
  let ctx = LAContext()
  ctx.localizedReason = "unlock the sshgate vault"
  let secret: SharedSecret
  do {
    let key = try SecureEnclave.P256.KeyAgreement.PrivateKey(dataRepresentation: keyData, authenticationContext: ctx)
    secret = try key.sharedSecretFromKeyAgreement(with: eph) // shows the Touch ID dialog
  } catch { fail(knownCode(error) ?? 3, "unlock: \(String(reflecting: error))") }
  guard let pw = try? AES.GCM.open(box, using: boxKey(secret)) else { fail(1, "open failed") }
  FileHandle.standardOutput.write(pw)

default:
  fail(1, "usage: sshgate-touchid available|enroll|unlock")
}
```

- [ ] **Step 2: Write the build script, ignore the binary, wire packaging**

`desktop/scripts/build-touchid.sh` (then `chmod +x` it):

```sh
#!/bin/sh
# Builds the Touch ID helper (spec 2026-10-12-touchid-unlock-design.md) into
# <repo root>/sshgate-touchid, ad-hoc signed: main finds it there when
# unpackaged, and package-mac.sh copies it into the bundle. macOS only.
set -eu
cd "$(dirname "$0")/../.."
swiftc -O desktop/native/touchid/main.swift -o sshgate-touchid
codesign --force --sign - sshgate-touchid
echo "built sshgate-touchid"
```

In `.gitignore`, under `/sshgate`, add:

```
/sshgate-touchid
```

In `desktop/scripts/package-mac.sh`, right after the `go build` block that ends `-o "desktop/$out/Contents/Resources/sshgate" ./cmd/sshgate)`, add:

```sh
./scripts/build-touchid.sh
cp ../sshgate-touchid "$out/Contents/Resources/sshgate-touchid"
```

The bundle's later `codesign --force --deep --sign - "$out"` re-signs it with the rest.

- [ ] **Step 3: Build and run the checks that need no finger**

```bash
cd /Users/lang/GolandProjects/github.com/lang315/ssh-mcp
desktop/scripts/build-touchid.sh
./sshgate-touchid available; echo "exit=$?"            # expected: exit=0
echo 'garbage' | ./sshgate-touchid unlock; echo "exit=$?"  # expected: bad input, exit=1
./sshgate-touchid nope; echo "exit=$?"                 # expected: usage, exit=1
```

- [ ] **Step 4: Live round trip (ask the author first: two Touch ID dialogs)**

Use the session scratchpad, never the repo:

```bash
S=<scratchpad>/touchid-live; mkdir -p "$S"
printf 'p w ü' | ./sshgate-touchid enroll > "$S/sealed.json"; echo "exit=$?"   # exit=0, no dialog
./sshgate-touchid unlock < "$S/sealed.json" | od -c | head -2; echo "exit=${PIPESTATUS[0]}"
# author touches the sensor → bytes "p w ü" exactly, no trailing \n, exit=0
./sshgate-touchid unlock < "$S/sealed.json"; echo " exit=$?"
# author presses Cancel → expected exit=2
```

If Cancel gives an exit code other than 2, read the stderr line (it prints `String(reflecting: error)`) and add that error's domain and code to `knownCode` as a 2. Rebuild, then repeat the Cancel check.

- [ ] **Step 5: Commit**

```bash
git add desktop/native/touchid/main.swift desktop/scripts/build-touchid.sh desktop/scripts/package-mac.sh .gitignore
git commit -m "feat(desktop): sshgate-touchid helper seals the master password to the Secure Enclave"
```

---

### Task 2: Main-process `TouchId` and the fake helper

**Files:**
- Create: `desktop/src/shared/touchid.ts`
- Create: `desktop/src/main/touchid.ts`
- Create: `desktop/test/fixtures/fake-touchid.sh`
- Test: `desktop/test/touchid.test.ts`

**Interfaces:**
- Consumes: `HubLike` from `src/main/ipc.ts` (`call(method, params?, timeoutMs?)`), and `HubReplyError` from `src/main/hubProcess.ts`.
- Produces:
  - `src/shared/touchid.ts`: `interface TouchStatus { supported: boolean; available: boolean; enabled: boolean }` and `type TouchUnlockResult = { cancelled: true } | { ranWhileLocked?: RanWhileLocked[] }`.
  - `src/main/touchid.ts`: `touchIdHelper(o: { env; packaged; appPath; platform; exists }): string | undefined`; `class TouchId` with `constructor(o: { helper?: string; file: string; hub: Pick<HubLike, 'call'> })`, `status(): Promise<TouchStatus>`, `enable(password: unknown): Promise<void>`, `disable(): void`, `unlock(): Promise<TouchUnlockResult>`, `cancel(): void`; constants `TOUCHID_RESET`, `TOUCHID_UNAVAILABLE`, `TOUCHID_FAILED`, `TOUCHID_BUSY`, `TOUCHID_OFF`.
  - The fake helper, `test/fixtures/fake-touchid.sh`. Its mode comes from the file named by `FAKE_TOUCHID_MODE`: `ok` (the default), `cancel`, `invalid`, `unavailable`, `fail`, `slow`.

- [ ] **Step 1: Write the fake helper**

`desktop/test/fixtures/fake-touchid.sh` (then `chmod +x`; git keeps the mode):

```sh
#!/bin/sh
# Fake sshgate-touchid for vitest and e2e: "seals" with base64 and asks for no
# finger. FAKE_TOUCHID_MODE names a file holding the mode: ok (default),
# cancel, invalid, unavailable, fail, slow (blocks until killed).
mode=ok
if [ -n "${FAKE_TOUCHID_MODE:-}" ] && [ -f "$FAKE_TOUCHID_MODE" ]; then mode=$(cat "$FAKE_TOUCHID_MODE"); fi
case "$1" in
  available) [ "$mode" = unavailable ] && exit 4; exit 0 ;;
  enroll) printf '{"v":1,"fake":"%s"}' "$(base64 | tr -d '\n')" ;;
  unlock)
    case "$mode" in
      cancel) exit 2 ;;
      invalid) exit 3 ;;
      unavailable) exit 4 ;;
      fail) echo boom >&2; exit 1 ;;
      slow) exec sleep 30 ;;
    esac
    sed -n 's/.*"fake":"\([^"]*\)".*/\1/p' | base64 -d ;;
  *) exit 1 ;;
esac
```

- [ ] **Step 2: Write the shared types**

`desktop/src/shared/touchid.ts`:

```ts
import type { RanWhileLocked } from './protocol'

// Unlock with Touch ID (spec 2026-10-12-touchid-unlock-design.md): what main's
// touchid:* IPC answers. supported: a helper exists; available: Touch ID can
// be used now; enabled: touchid.json holds a password for the current vault.
export interface TouchStatus { supported: boolean; available: boolean; enabled: boolean }
export type TouchUnlockResult = { cancelled: true } | { ranWhileLocked?: RanWhileLocked[] }
```

- [ ] **Step 3: Write the failing tests**

`desktop/test/touchid.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as fs from 'node:fs'
import * as os from 'node:os'
import * as path from 'node:path'
import { HubReplyError } from '../src/main/hubProcess'
import { TOUCHID_BUSY, TOUCHID_FAILED, TOUCHID_OFF, TOUCHID_RESET, TOUCHID_UNAVAILABLE, TouchId, touchIdHelper } from '../src/main/touchid'

const fake = path.join(__dirname, 'fixtures', 'fake-touchid.sh')
let dir: string
let file: string
let modeFile: string
const setMode = (m: string) => fs.writeFileSync(modeFile, m)

// A hub with one vault at /v.json whose password is `pw`.
function fakeHub(o: { pw?: string; hasVault?: boolean; storePath?: string } = {}) {
  const st = { pw: 'pw', hasVault: true, storePath: '/v.json', ...o }
  return {
    st,
    call: vi.fn(async (m: string, p?: unknown) => {
      if (m === 'status') return { hasVault: st.hasVault, storePath: st.storePath }
      if (m === 'unlock') {
        if ((p as { password: string }).password !== st.pw) throw new HubReplyError('wrong master password')
        return { ranWhileLocked: [{ server: 'box', count: 2 }] }
      }
      throw new Error(`unexpected ${m}`)
    }),
  }
}

beforeEach(() => {
  dir = fs.mkdtempSync(path.join(os.tmpdir(), 'touchid-'))
  file = path.join(dir, 'touchid.json')
  modeFile = path.join(dir, 'mode')
  process.env.FAKE_TOUCHID_MODE = modeFile
})
afterEach(() => { delete process.env.FAKE_TOUCHID_MODE; fs.rmSync(dir, { recursive: true, force: true }) })

describe('touchIdHelper', () => {
  const appPath = '/Applications/sshgate.app/Contents/Resources/app'
  const bundled = path.join(appPath, '..', 'sshgate-touchid')
  it('takes the knob only when unpackaged', () => {
    const env = { SSHGATE_TOUCHID_HELPER: '/tmp/fake' }
    expect(touchIdHelper({ env, packaged: false, appPath, platform: 'linux', exists: () => false })).toBe('/tmp/fake')
    expect(touchIdHelper({ env, packaged: true, appPath, platform: 'darwin', exists: (p) => p === bundled })).toBe(bundled)
  })
  it('finds nothing off macOS, or when the binary is missing', () => {
    expect(touchIdHelper({ env: {}, packaged: true, appPath, platform: 'win32', exists: () => true })).toBeUndefined()
    expect(touchIdHelper({ env: {}, packaged: true, appPath, platform: 'darwin', exists: () => false })).toBeUndefined()
  })
})

describe('TouchId', () => {
  it('reports unsupported with no helper and spawns nothing', async () => {
    const hub = fakeHub()
    const t = new TouchId({ file, hub })
    expect(await t.status()).toEqual({ supported: false, available: false, enabled: false })
    await expect(t.unlock()).rejects.toThrow(TOUCHID_UNAVAILABLE)
    expect(hub.call).not.toHaveBeenCalled()
  })

  it('enable refuses a wrong password and writes nothing; the right one writes a 0600 file for this vault', async () => {
    const hub = fakeHub()
    const t = new TouchId({ helper: fake, file, hub })
    await expect(t.enable('nope')).rejects.toThrow('wrong master password')
    expect(fs.existsSync(file)).toBe(false)
    await t.enable('pw')
    expect(fs.statSync(file).mode & 0o777).toBe(0o600)
    expect(JSON.parse(fs.readFileSync(file, 'utf8')).storePath).toBe('/v.json')
    expect(await t.status()).toEqual({ supported: true, available: true, enabled: true })
  })

  it('enable refuses an empty password and a store with no vault', async () => {
    await expect(new TouchId({ helper: fake, file, hub: fakeHub() }).enable('')).rejects.toThrow()
    await expect(new TouchId({ helper: fake, file, hub: fakeHub({ hasVault: false }) }).enable('pw')).rejects.toThrow('create a vault first')
    expect(fs.existsSync(file)).toBe(false)
  })

  it('unlock sends the opened password to the hub byte for byte and returns its reply', async () => {
    const pw = 'p w ü\n'
    const hub = fakeHub({ pw })
    const t = new TouchId({ helper: fake, file, hub })
    await t.enable(pw)
    hub.call.mockClear()
    expect(await t.unlock()).toEqual({ ranWhileLocked: [{ server: 'box', count: 2 }] })
    expect(hub.call).toHaveBeenCalledWith('unlock', { password: pw })
  })

  it('a key that no longer works deletes the file', async () => {
    const t = new TouchId({ helper: fake, file, hub: fakeHub() })
    await t.enable('pw')
    setMode('invalid')
    await expect(t.unlock()).rejects.toThrow(TOUCHID_RESET)
    expect(fs.existsSync(file)).toBe(false)
  })

  it('the hub refusing the stored password deletes the file', async () => {
    const hub = fakeHub()
    const t = new TouchId({ helper: fake, file, hub })
    await t.enable('pw')
    hub.st.pw = 'new vault password'
    await expect(t.unlock()).rejects.toThrow(TOUCHID_RESET)
    expect(fs.existsSync(file)).toBe(false)
  })

  it('cancel, unavailable and other failures keep the file', async () => {
    const t = new TouchId({ helper: fake, file, hub: fakeHub() })
    await t.enable('pw')
    setMode('cancel')
    expect(await t.unlock()).toEqual({ cancelled: true })
    setMode('unavailable')
    await expect(t.unlock()).rejects.toThrow(TOUCHID_UNAVAILABLE)
    expect((await t.status()).available).toBe(false)
    setMode('fail')
    await expect(t.unlock()).rejects.toThrow(TOUCHID_FAILED)
    expect(fs.existsSync(file)).toBe(true)
  })

  it('refuses a second unlock while one asks; cancel() ends the first as cancelled', async () => {
    const t = new TouchId({ helper: fake, file, hub: fakeHub() })
    await t.enable('pw')
    setMode('slow')
    const first = t.unlock()
    await expect(t.unlock()).rejects.toThrow(TOUCHID_BUSY)
    await vi.waitFor(() => expect((t as unknown as { running?: unknown }).running).toBeDefined())
    t.cancel()
    expect(await first).toEqual({ cancelled: true })
  })

  it('a file for another vault, or a corrupt file, reads as not turned on', async () => {
    const hub = fakeHub()
    const t = new TouchId({ helper: fake, file, hub })
    await t.enable('pw')
    hub.st.storePath = '/other.json'
    expect((await t.status()).enabled).toBe(false)
    await expect(t.unlock()).rejects.toThrow(TOUCHID_OFF)
    fs.writeFileSync(file, '{not json')
    hub.st.storePath = '/v.json'
    expect((await t.status()).enabled).toBe(false)
  })

  it('a hub that is down reads as not enabled, and unlock rejects', async () => {
    const t = new TouchId({ helper: fake, file, hub: { call: vi.fn(async () => { throw new Error('hub not running') }) } })
    expect(await t.status()).toEqual({ supported: true, available: true, enabled: false })
    await expect(t.unlock()).rejects.toThrow('hub not running')
  })

  it('disable deletes the file, and is fine with none', async () => {
    const t = new TouchId({ helper: fake, file, hub: fakeHub() })
    await t.enable('pw')
    t.disable()
    expect(fs.existsSync(file)).toBe(false)
    t.disable()
  })
})
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `cd desktop && npx vitest run test/touchid.test.ts`
Expected: FAIL, `Cannot find module '../src/main/touchid'`.

- [ ] **Step 5: Implement `touchid.ts`**

`desktop/src/main/touchid.ts`:

```ts
import { spawn, type ChildProcess } from 'node:child_process'
import * as fs from 'node:fs'
import * as path from 'node:path'
import type { TouchStatus, TouchUnlockResult } from '../shared/touchid'
import { HubReplyError } from './hubProcess'
import type { HubLike } from './ipc'

// Unlock with Touch ID (spec 2026-10-12-touchid-unlock-design.md). main owns
// touchid.json; sshgate-touchid seals and opens the password and never touches
// the disk. The password goes renderer → main → helper stdin / hub unlock, and
// never back to the renderer.

export const TOUCHID_RESET = 'Touch ID was turned off because your fingerprints changed or your master password is different. Unlock with your password, then turn it on again in Settings.'
export const TOUCHID_UNAVAILABLE = 'Touch ID is not available right now. Unlock with your password.'
export const TOUCHID_FAILED = 'Touch ID failed.'
export const TOUCHID_BUSY = 'Touch ID is already asking'
export const TOUCHID_OFF = 'Touch ID is not turned on'

// Like hubLaunch: SSHGATE_TOUCHID_HELPER is a dev/test knob, ignored when
// packaged, since whoever sets the environment could otherwise swap the
// helper that receives the master password.
export function touchIdHelper(o: {
  env: NodeJS.ProcessEnv; packaged: boolean; appPath: string; platform: NodeJS.Platform; exists: (p: string) => boolean
}): string | undefined {
  if (!o.packaged && o.env.SSHGATE_TOUCHID_HELPER) return o.env.SSHGATE_TOUCHID_HELPER
  if (o.platform !== 'darwin') return undefined
  const local = path.join(o.appPath, '..', 'sshgate-touchid')
  return o.exists(local) ? local : undefined
}

interface Run { code: number; stdout: Buffer; stderr: string }

export class TouchId {
  private running?: ChildProcess
  private busy = false

  constructor(private o: { helper?: string; file: string; hub: Pick<HubLike, 'call'> }) {}

  // A helper that cannot start counts as 4 (not available); one killed by
  // cancel() counts as 2 (cancelled).
  private run(args: string[], input?: Buffer, track = false): Promise<Run> {
    return new Promise((resolve) => {
      const p = spawn(this.o.helper!, args, { stdio: ['pipe', 'pipe', 'pipe'] })
      if (track) this.running = p
      const out: Buffer[] = []
      let err = ''
      let done = false
      const finish = (code: number) => {
        if (done) return
        done = true
        if (this.running === p) this.running = undefined
        resolve({ code, stdout: Buffer.concat(out), stderr: err })
      }
      p.stdout.on('data', (b: Buffer) => out.push(b))
      p.stderr.on('data', (b: Buffer) => { err += b.toString() })
      p.stdin.on('error', () => {}) // the helper may exit before reading stdin
      p.stdin.end(input)
      p.on('error', () => finish(4))
      p.on('close', (code, signal) => finish(signal ? 2 : code ?? 1))
    })
  }

  private async store(): Promise<{ hasVault: boolean; storePath: string }> {
    return (await this.o.hub.call('status')) as { hasVault: boolean; storePath: string }
  }

  // The sealed blob for this vault, or undefined: no file, a corrupt one, or one for another store.
  private read(storePath: string): unknown {
    try {
      const f = JSON.parse(fs.readFileSync(this.o.file, 'utf8')) as { storePath?: unknown; sealed?: unknown }
      return f.storePath === storePath && f.sealed ? f.sealed : undefined
    } catch { return undefined }
  }

  async status(): Promise<TouchStatus> {
    if (!this.o.helper) return { supported: false, available: false, enabled: false }
    const available = (await this.run(['available'])).code === 0
    let enabled = false
    try { enabled = this.read((await this.store()).storePath) !== undefined } catch { /* hub not running */ }
    return { supported: true, available, enabled }
  }

  async enable(password: unknown): Promise<void> {
    if (typeof password !== 'string' || password === '') throw new Error('Type your master password')
    if (!this.o.helper) throw new Error(TOUCHID_UNAVAILABLE)
    const s = await this.store()
    // With no vault the hub's unlock accepts any password, so it proves nothing.
    if (!s.hasVault) throw new Error('create a vault first')
    await this.o.hub.call('unlock', { password }) // checks it; a wrong one leaves the hub as it was
    const r = await this.run(['enroll'], Buffer.from(password, 'utf8'))
    if (r.code === 4) throw new Error(TOUCHID_UNAVAILABLE)
    let sealed: unknown
    try {
      if (r.code !== 0) throw new Error()
      sealed = JSON.parse(r.stdout.toString('utf8'))
    } catch {
      console.error('sshgate: touchid enroll failed:', r.code, r.stderr)
      throw new Error(TOUCHID_FAILED)
    }
    fs.mkdirSync(path.dirname(this.o.file), { recursive: true })
    const tmp = `${this.o.file}.${process.pid}.tmp`
    fs.rmSync(tmp, { force: true })
    fs.writeFileSync(tmp, JSON.stringify({ storePath: s.storePath, sealed }), { mode: 0o600 })
    fs.renameSync(tmp, this.o.file)
  }

  disable(): void {
    fs.rmSync(this.o.file, { force: true })
  }

  async unlock(): Promise<TouchUnlockResult> {
    if (this.busy) throw new Error(TOUCHID_BUSY)
    if (!this.o.helper) throw new Error(TOUCHID_UNAVAILABLE)
    this.busy = true
    try {
      const sealed = this.read((await this.store()).storePath)
      if (sealed === undefined) throw new Error(TOUCHID_OFF)
      const r = await this.run(['unlock'], Buffer.from(JSON.stringify(sealed)), true)
      switch (r.code) {
        case 0: break
        case 2: return { cancelled: true }
        case 3: this.disable(); throw new Error(TOUCHID_RESET)
        case 4: throw new Error(TOUCHID_UNAVAILABLE)
        default:
          console.error('sshgate: touchid unlock failed:', r.code, r.stderr)
          throw new Error(TOUCHID_FAILED)
      }
      try {
        return (await this.o.hub.call('unlock', { password: r.stdout.toString('utf8') })) as TouchUnlockResult
      } catch (e) {
        if (e instanceof HubReplyError && e.message === 'wrong master password') { this.disable(); throw new Error(TOUCHID_RESET) }
        throw e
      }
    } finally { this.busy = false }
  }

  // Closes a Touch ID dialog still up: a typed password won, or the hub stopped.
  cancel(): void {
    this.running?.kill()
  }
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd desktop && npx vitest run test/touchid.test.ts`
Expected: PASS (all tests).

- [ ] **Step 7: Commit**

```bash
git add desktop/src/shared/touchid.ts desktop/src/main/touchid.ts desktop/test/fixtures/fake-touchid.sh desktop/test/touchid.test.ts
git commit -m "feat(desktop): TouchId keeps touchid.json and unlocks the hub through the helper"
```

---

### Task 3: IPC, preload, transport, main wiring

**Files:**
- Modify: `desktop/src/main/ipc.ts`
- Modify: `desktop/src/main/main.ts`
- Modify: `desktop/src/preload/preload.ts`
- Modify: `desktop/src/renderer/transport.ts`
- Test: `desktop/test/ipc.test.ts`, `desktop/test/preload.test.ts`

**Interfaces:**
- Consumes: `TouchId` and `touchIdHelper` (Task 2), and the `TouchStatus`/`TouchUnlockResult` types.
- Produces:
  - IPC channels `touchid:status`, `touchid:enable` (`{password}`), `touchid:disable`, `touchid:unlock`.
  - `registerIpc(hub, getWindow, isTrusted, files, touch)`, where `touch: Pick<TouchId, 'status' | 'enable' | 'disable' | 'unlock' | 'cancel'>`.
  - Renderer `touchid` object exported from `transport.ts`: `status(): Promise<TouchStatus>`, `enable(password: string): Promise<void>`, `disable(): Promise<void>`, `unlock(): Promise<TouchUnlockResult>`.

- [ ] **Step 1: Write the failing tests**

In `desktop/test/ipc.test.ts`, add `const fakeTouch = () => ({ status: vi.fn(async () => ({ supported: true, available: true, enabled: true })), enable: vi.fn(async () => {}), disable: vi.fn(), unlock: vi.fn(async () => ({})), cancel: vi.fn() })` after `fakeHub`. Pass `fakeTouch()` as a 5th argument to each of the existing `registerIpc(...)` calls (4 call sites). Then append:

```ts
describe('registerIpc touchid', () => {
  const setup = () => {
    vi.mocked(ipcMain.handle).mockClear()
    const h = { ...fakeHub(), on: vi.fn(), state: { kind: 'running' } }
    const touch = fakeTouch()
    const trusted = { sender: 'app' }
    registerIpc(h as never, () => undefined, (e) => e === (trusted as never), fakeFiles(), touch)
    const handler = (ch: string) => vi.mocked(ipcMain.handle).mock.calls.find(([c]) => c === ch)![1] as (e: unknown, ...a: unknown[]) => Promise<unknown>
    return { h, touch, trusted, handler }
  }
  it('rejects an untrusted sender on every touchid channel', async () => {
    const { touch, handler } = setup()
    for (const ch of ['touchid:status', 'touchid:enable', 'touchid:disable', 'touchid:unlock']) {
      await expect(handler(ch)({ sender: 'evil' }, { password: 'pw' })).rejects.toThrow('untrusted sender')
    }
    expect(touch.status).not.toHaveBeenCalled()
    expect(touch.enable).not.toHaveBeenCalled()
    expect(touch.disable).not.toHaveBeenCalled()
    expect(touch.unlock).not.toHaveBeenCalled()
  })
  it('routes a trusted call', async () => {
    const { touch, trusted, handler } = setup()
    await handler('touchid:enable')(trusted, { password: 'pw' })
    expect(touch.enable).toHaveBeenCalledWith('pw')
    await handler('touchid:unlock')(trusted)
    expect(touch.unlock).toHaveBeenCalled()
  })
  it('a password unlock that succeeds closes a Touch ID dialog; a failed one does not', async () => {
    const { h, touch, trusted, handler } = setup()
    h.call.mockRejectedValueOnce(new Error('wrong master password'))
    await expect(handler('hub:call')(trusted, 'unlock', { password: 'x' })).rejects.toThrow()
    expect(touch.cancel).not.toHaveBeenCalled()
    await handler('hub:call')(trusted, 'unlock', { password: 'pw' })
    expect(touch.cancel).toHaveBeenCalledTimes(1)
  })
  it('a hub that stops closes a Touch ID dialog', () => {
    const { h, touch } = setup()
    const onState = h.on.mock.calls.find(([ev]) => ev === 'state')![1] as (s: unknown) => void
    onState({ kind: 'running' })
    expect(touch.cancel).not.toHaveBeenCalled()
    onState({ kind: 'restarting', attempt: 1 })
    expect(touch.cancel).toHaveBeenCalledTimes(1)
  })
})
```

If `fakeFiles` does not exist in `ipc.test.ts` yet, find the helper the existing `hub:notify` test passes as its 4th argument and use it. Every new test needs one.

In `desktop/test/preload.test.ts`, append:

```ts
describe('preload touchid', () => {
  it('invokes the touchid channels, the password only as {password}', async () => {
    await exposed.sshmcp.touchEnable('pw')
    expect(invoke).toHaveBeenCalledWith('touchid:enable', { password: 'pw' })
    await exposed.sshmcp.touchUnlock()
    expect(invoke).toHaveBeenCalledWith('touchid:unlock')
    await exposed.sshmcp.touchStatus()
    expect(invoke).toHaveBeenCalledWith('touchid:status')
    await exposed.sshmcp.touchDisable()
    expect(invoke).toHaveBeenCalledWith('touchid:disable')
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd desktop && npx vitest run test/ipc.test.ts test/preload.test.ts`
Expected: FAIL. The `touchid:*` handlers are not found, and `exposed.sshmcp.touchEnable is not a function`.

- [ ] **Step 3: Implement**

`desktop/src/main/ipc.ts`: add `import type { TouchId } from './touchid'`. Change `registerIpc`'s signature and body:

```ts
export function registerIpc(
  hub: HubProcess,
  getWindow: () => BrowserWindow | undefined,
  isTrusted: IsTrusted,
  files: Pick<FilesRelay, 'call' | 'onNotification' | 'onState' | 'pickUpload' | 'pickDownloadDir' | 'grantDropped'>,
  touch: Pick<TouchId, 'status' | 'enable' | 'disable' | 'unlock' | 'cancel'>,
): void {
  ipcMain.handle('hub:call', (e, method: unknown, params: unknown) =>
    guard(isTrusted, e, async () => {
      if (typeof method === 'string' && relayed.has(method)) return files.call(method, params)
      const r = await relayCall(hub, method, params)
      if (method === 'unlock') touch.cancel() // a typed password won: close a Touch ID dialog still up
      return r
    }))
```

Keep the existing `hub:notify`, `hub:get-state` and `files:*` handlers unchanged. After them, add:

```ts
  ipcMain.handle('touchid:status', (e) => guard(isTrusted, e, () => touch.status()))
  ipcMain.handle('touchid:enable', (e, p: unknown) =>
    guard(isTrusted, e, () => touch.enable((p as { password?: unknown } | undefined)?.password)))
  ipcMain.handle('touchid:disable', (e) => guard(isTrusted, e, async () => touch.disable()))
  ipcMain.handle('touchid:unlock', (e) => guard(isTrusted, e, () => touch.unlock()))
```

Change the `state` listener to:

```ts
  hub.on('state', (s: HubState) => {
    files.onState(s)
    if (s.kind !== 'running') touch.cancel()
    getWindow()?.webContents.send('hub:state', s)
  })
```

`desktop/src/main/main.ts`: add `import { TouchId, touchIdHelper } from './touchid'`. Replace the `const launch = …` line with:

```ts
const packaged = app.isPackaged || !process.defaultApp
const launch = hubLaunch({ env: process.env, packaged, appPath: app.getAppPath(), platform: process.platform, exists: fs.existsSync })
```

After `const files = …`, add:

```ts
const touch = new TouchId({
  helper: touchIdHelper({ env: process.env, packaged, appPath: app.getAppPath(), platform: process.platform, exists: fs.existsSync }),
  file: path.join(app.getPath('userData'), 'touchid.json'),
  hub,
})
```

Change `registerIpc(hub, getWindow, isTrusted, files)` to `registerIpc(hub, getWindow, isTrusted, files, touch)`.

`desktop/src/preload/preload.ts`: inside the exposed object, after `grantDropped`, add:

```ts
  touchStatus: () => ipcRenderer.invoke('touchid:status'),
  touchEnable: (password: string) => ipcRenderer.invoke('touchid:enable', { password }),
  touchDisable: () => ipcRenderer.invoke('touchid:disable'),
  touchUnlock: () => ipcRenderer.invoke('touchid:unlock'),
```

`desktop/src/renderer/transport.ts`: add `import type { TouchStatus, TouchUnlockResult } from '../shared/touchid'`. Add these to `interface Bridge`:

```ts
  touchStatus(): Promise<TouchStatus>
  touchEnable(password: string): Promise<void>
  touchDisable(): Promise<void>
  touchUnlock(): Promise<TouchUnlockResult>
```

At the end of the file, add:

```ts
async function viaBridge<T>(f: () => Promise<T>): Promise<T> {
  try { return await f() } catch (e) { throw cleanError(e) }
}

// Touch ID unlock (spec 2026-10-12-touchid-unlock-design.md): handled by main,
// never relayed to the hub as is. The password only ever goes this way.
export const touchid = {
  status: () => viaBridge(() => bridge().touchStatus()),
  enable: (password: string) => viaBridge(() => bridge().touchEnable(password)),
  disable: () => viaBridge(() => bridge().touchDisable()),
  unlock: () => viaBridge(() => bridge().touchUnlock()),
}
```

- [ ] **Step 4: Run the tests and the typecheck**

Run: `cd desktop && npx vitest run test/ipc.test.ts test/preload.test.ts && npm run typecheck`
Expected: PASS, typecheck exit 0.

- [ ] **Step 5: Commit**

```bash
git add desktop/src/main/ipc.ts desktop/src/main/main.ts desktop/src/preload/preload.ts desktop/src/renderer/transport.ts desktop/test/ipc.test.ts desktop/test/preload.test.ts
git commit -m "feat(desktop): touchid IPC; a typed unlock or a hub stop closes the Touch ID dialog"
```

---

### Task 4: Settings row

**Files:**
- Modify: `desktop/src/renderer/settings.ts`
- Modify: `desktop/src/renderer/SettingsDialog.tsx`
- Modify: `desktop/src/renderer/App.tsx`
- Test: `desktop/test/settings.test.ts`, `desktop/test/SettingsDialog.test.ts`

**Interfaces:**
- Consumes: `touchid` from `transport.ts` (Task 3), and `TouchStatus`.
- Produces:
  - `settings.ts`: `type TouchChange = 'same' | 'on' | 'off'`; `touchChange(enabled: boolean, chosen: boolean): TouchChange`; `saveAllowed({change, touch?, password?, busy, openedAt, changedAt, now})`; `settingsChangeKey(chosen, hosts, error?, touchOn = false)`; `saveSettings(o: {change, limit, touch, password}, api: {enable, disable, setLimit}): Promise<void>`.
  - `SettingsDialog` props gain `touch?: TouchStatus`, and `onSave` becomes `(limit: SoftLockLimit, touch: TouchChange, password: string) => Promise<void>`.
  - `App` state `touch: TouchStatus | undefined` (Task 5 uses it too).

- [ ] **Step 1: Write the failing tests**

In `desktop/test/settings.test.ts`, add `touchChange` and `saveSettings` to the import from `../src/renderer/settings`. Append:

```ts
describe('Touch ID in Settings', () => {
  const base = { change: 'same' as const, busy: false, openedAt: 1000, changedAt: 1000, now: 9999 }
  it('touchChange', () => {
    expect(touchChange(false, false)).toBe('same')
    expect(touchChange(false, true)).toBe('on')
    expect(touchChange(true, false)).toBe('off')
  })
  it('a Touch ID change alone enables Save; turning on needs a password', () => {
    expect(saveAllowed({ ...base })).toBe(false)
    expect(saveAllowed({ ...base, touch: 'off' })).toBe(true)
    expect(saveAllowed({ ...base, touch: 'on', password: '' })).toBe(false)
    expect(saveAllowed({ ...base, touch: 'on', password: 'pw' })).toBe(true)
    expect(saveAllowed({ ...base, touch: 'on', password: 'pw', busy: true })).toBe(false)
  })
  it('turning Touch ID on moves Save, so it restarts the delay', () => {
    expect(settingsChangeKey('24h', [], undefined, true)).not.toBe(settingsChangeKey('24h', [], undefined, false))
  })
  it('saveSettings does Touch ID first and leaves the limit unsaved when it fails', async () => {
    const calls: string[] = []
    const api = {
      enable: async (pw: string) => { calls.push(`enable ${pw}`); if (pw === 'bad') throw new Error('wrong master password') },
      disable: async () => { calls.push('disable') },
      setLimit: async (l: string) => { calls.push(`limit ${l}`) },
    }
    await expect(saveSettings({ change: 'higher', limit: '7d', touch: 'on', password: 'bad' }, api)).rejects.toThrow('wrong master password')
    expect(calls).toEqual(['enable bad'])
    calls.length = 0
    await saveSettings({ change: 'higher', limit: '7d', touch: 'on', password: 'pw' }, api)
    expect(calls).toEqual(['enable pw', 'limit 7d'])
    calls.length = 0
    await saveSettings({ change: 'same', limit: '24h', touch: 'off', password: '' }, api)
    expect(calls).toEqual(['disable'])
  })
})
```

In `desktop/test/SettingsDialog.test.ts`, append:

```ts
describe('SettingsDialog Touch ID', () => {
  it('has no Unlock section when Touch ID is not supported', () => {
    expect(render()).not.toContain('>Unlock</h4>')
    expect(render({ touch: { supported: false, available: false, enabled: false } })).not.toContain('Unlock with Touch ID')
  })
  it('shows the switch, off, with no password field', () => {
    const html = render({ touch: { supported: true, available: true, enabled: false } })
    expect(html).toContain('>Unlock</h4>')
    expect(html).toMatch(/<input[^>]*role="switch"[^>]*>/)
    expect(html).toContain('Unlock with Touch ID')
    expect(html).not.toContain('Master password')
  })
  it('disables the switch with a reason when Touch ID is not available', () => {
    const html = render({ touch: { supported: true, available: false, enabled: false } })
    expect(html).toContain('Touch ID is not available on this Mac right now.')
    expect(html.match(/<input[^>]*role="switch"[^>]*>/)![0]).toMatch(/disabled=""/)
  })
  it('keeps an enabled switch usable when Touch ID is not available, so it can be turned off', () => {
    const sw = render({ touch: { supported: true, available: false, enabled: true } }).match(/<input[^>]*role="switch"[^>]*>/)![0]
    expect(sw).toMatch(/checked=""/)
    expect(sw).not.toMatch(/disabled=""/)
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd desktop && npx vitest run test/settings.test.ts test/SettingsDialog.test.ts`
Expected: FAIL, because `touchChange` and `saveSettings` are not exported and there is no `>Unlock</h4>`.

- [ ] **Step 3: Implement `settings.ts`**

Replace `settingsChangeKey` and `saveAllowed` with:

```ts
// Anything that moves Save restarts its delay: the choice (it swaps the
// notice), the host list in the notice, an error line appearing, and the
// Touch ID password field.
export const settingsChangeKey = (chosen: SoftLockLimit, hosts: string[], error?: string, touchOn = false) =>
  JSON.stringify([chosen, hosts, error ?? null, touchOn])

export type TouchChange = 'same' | 'on' | 'off'
export const touchChange = (enabled: boolean, chosen: boolean): TouchChange => (chosen === enabled ? 'same' : chosen ? 'on' : 'off')

export function saveAllowed(o: { change: LimitChange; touch?: TouchChange; password?: string; busy: boolean; openedAt: number; changedAt: number; now: number }): boolean {
  const touch = o.touch ?? 'same'
  if (o.busy || (o.change === 'same' && touch === 'same')) return false
  if (touch === 'on' && !o.password) return false
  return delayElapsed(o.openedAt, o.changedAt, o.now)
}

// Touch ID first: when it fails the limit is not saved, and the dialog stays
// open with the error (spec 2026-10-12-touchid-unlock-design.md).
export async function saveSettings(
  o: { change: LimitChange; limit: SoftLockLimit; touch: TouchChange; password: string },
  api: { enable(password: string): Promise<void>; disable(): Promise<void>; setLimit(l: SoftLockLimit): Promise<void> },
): Promise<void> {
  if (o.touch === 'on') await api.enable(o.password)
  if (o.touch === 'off') await api.disable()
  if (o.change !== 'same') await api.setLimit(o.limit)
}
```

- [ ] **Step 4: Implement the dialog**

In `SettingsDialog.tsx`, add `import type { TouchStatus } from '../shared/touchid'`, and add `touchChange` and `type TouchChange` to the `./settings` import. Change the props and state:

```tsx
export function SettingsDialog({ limit, hosts, touch, onSave, onCancel }: {
  limit: SoftLockLimit; hosts: string[]; touch?: TouchStatus
  onSave: (limit: SoftLockLimit, touch: TouchChange, password: string) => Promise<void>; onCancel: () => void
}) {
```

After `const [chosen, setChosen] = useState(limit)`, add:

```tsx
  const [touchOn, setTouchOn] = useState(touch?.enabled ?? false)
  const [password, setPassword] = useState('')
  const tc = touchChange(touch?.enabled ?? false, touchOn)
```

Change the key and the Save rule to:

```tsx
  const key = settingsChangeKey(chosen, hosts, error, tc === 'on')
  …
  const ok = saveAllowed({ change, touch: tc, password, busy, openedAt, changedAt: changes.current.at, now })
```

Inside `save`, change `await onSave(chosen)` to `await onSave(chosen, tc, password)`.

Insert the section before the Auto-allow `<section>`:

```tsx
        {touch?.supported && (
          <section className="settings-section" aria-labelledby={`${id}-unlock`}>
            <h4 id={`${id}-unlock`}>Unlock</h4>
            <div className="switch">
              <label htmlFor={`${id}-touch`} className="switch-title">Unlock with Touch ID</label>
              <input id={`${id}-touch`} type="checkbox" role="switch" checked={touchOn}
                disabled={busy || (!touch.available && !touchOn)}
                onChange={(e) => { setTouchOn(e.target.checked); setPassword('') }} />
            </div>
            {!touch.available && <p className="muted">Touch ID is not available on this Mac right now.</p>}
            {tc === 'on' && (
              <div className="field">
                <label htmlFor={`${id}-pw`}>Master password</label>
                <input id={`${id}-pw`} type="password" autoFocus value={password}
                  onChange={(e) => setPassword(e.target.value)} disabled={busy} />
              </div>
            )}
          </section>
        )}
```

Enter in the password field submits the form, which is Cancel, as everywhere in this dialog (spec, "Settings").

- [ ] **Step 5: Wire `App.tsx`**

Add `touchid` to the `./transport` import, `saveSettings` and `limitChange` from `./settings`, and `import type { TouchStatus } from '../shared/touchid'`. After `const [settingsOpen, setSettingsOpen] = useState(false)`, add:

```tsx
  const [touch, setTouch] = useState<TouchStatus>()
```

After the `screen` constant, add:

```tsx
  // Touch ID's state, read when the unlock screen or Settings shows. status()
  // spawns the helper but never calls the hub beyond `status`, so it is not UI activity.
  const locked = screen.kind === 'locked'
  useEffect(() => {
    if (!locked && !settingsOpen) return
    touchid.status().then(setTouch).catch(() => setTouch(undefined))
  }, [locked, settingsOpen])
```

Replace the `SettingsDialog` element with:

```tsx
        <SettingsDialog limit={status.softLockLimit} hosts={[...autoHostsSet].sort()} touch={touch}
          onSave={async (l, t, pw) => {
            await saveSettings({ change: limitChange(status.softLockLimit!, l), limit: l, touch: t, password: pw },
              { enable: touchid.enable, disable: touchid.disable, setLimit: hub.setSoftLockLimit })
            await refresh(); setSettingsOpen(false)
          }}
          onCancel={() => setSettingsOpen(false)} />
```

- [ ] **Step 6: Run the tests and the typecheck**

Run: `cd desktop && npx vitest run && npm run typecheck`
Expected: every test passes, and typecheck exits 0. The existing `saveAllowed` and `settingsChangeKey` tests still pass, because they only compare keys with each other.

- [ ] **Step 7: Commit**

```bash
git add desktop/src/renderer/settings.ts desktop/src/renderer/SettingsDialog.tsx desktop/src/renderer/App.tsx desktop/test/settings.test.ts desktop/test/SettingsDialog.test.ts
git commit -m "feat(desktop): Settings turns Touch ID unlock on and off"
```

---

### Task 5: Unlock button and auto-prompt

**Files:**
- Modify: `desktop/src/renderer/shell.ts`
- Modify: `desktop/src/renderer/Unlock.tsx`
- Modify: `desktop/src/renderer/App.tsx`
- Test: `desktop/test/shell.test.ts`, `desktop/test/Unlock.test.ts`

**Interfaces:**
- Consumes: the `touch` state and `locked` from Task 4, `touchid.unlock()` from Task 3, and `everReady`, `refresh`, `setUnlockError`, `setLockReason`, `setRanLocked` already in `App`.
- Produces:
  - `shell.ts`: `autoTouch(o: { locked: boolean; touch?: TouchStatus; asked: boolean; trigger: 'start' | 'focus'; everReady: boolean; focused: boolean }): boolean`.
  - `Unlock` prop `touchId?: { onUnlock: () => void; busy: boolean }`.

- [ ] **Step 1: Write the failing tests**

In `desktop/test/shell.test.ts`, add `autoTouch` to the import from `../src/renderer/shell`. Append:

```ts
describe('autoTouch', () => {
  const on = { supported: true, available: true, enabled: true }
  const base = { locked: true, touch: on, asked: false, everReady: false, focused: true }
  it('asks at app start with the window focused, and on a focus while locked', () => {
    expect(autoTouch({ ...base, trigger: 'start' })).toBe(true)
    expect(autoTouch({ ...base, trigger: 'focus', everReady: true })).toBe(true)
  })
  it('never at the moment of locking: no start trigger once the app has been unlocked', () => {
    expect(autoTouch({ ...base, trigger: 'start', everReady: true })).toBe(false)
  })
  it('not at start without focus, and once per episode', () => {
    expect(autoTouch({ ...base, trigger: 'start', focused: false })).toBe(false)
    expect(autoTouch({ ...base, trigger: 'focus', asked: true })).toBe(false)
  })
  it('only when locked, enabled and available', () => {
    expect(autoTouch({ ...base, trigger: 'focus', locked: false })).toBe(false)
    expect(autoTouch({ ...base, trigger: 'focus', touch: { ...on, enabled: false } })).toBe(false)
    expect(autoTouch({ ...base, trigger: 'focus', touch: { ...on, available: false } })).toBe(false)
    expect(autoTouch({ ...base, trigger: 'focus', touch: undefined })).toBe(false)
  })
})
```

In `desktop/test/Unlock.test.ts`, append inside the `describe('Unlock', …)` block:

```ts
  it('offers Touch ID above the password only when given', () => {
    expect(render({})).not.toContain('Unlock with Touch ID')
    const html = render({ touchId: { onUnlock: () => {}, busy: false } })
    expect(html).toContain('Unlock with Touch ID')
    expect(html.indexOf('Unlock with Touch ID')).toBeLessThan(html.indexOf('Master password'))
    expect(render({ touchId: { onUnlock: () => {}, busy: true } }).match(/<button[^>]*>Unlock with Touch ID<\/button>/)![0]).toMatch(/disabled=""/)
  })
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd desktop && npx vitest run test/shell.test.ts test/Unlock.test.ts`
Expected: FAIL, because `autoTouch` is not exported and there is no `Unlock with Touch ID`.

- [ ] **Step 3: Implement `autoTouch`**

Append to `shell.ts`, adding `import type { TouchStatus } from '../shared/touchid'` at the top:

```ts
// Touch ID asks by itself at most once per locked episode: at app start with
// the window focused (everReady is false until the first unlock), or on the
// window's focus while locked, so never at the moment of locking. A cancel
// counts as asked: the dialog hands focus back, which must not ask again.
export function autoTouch(o: { locked: boolean; touch?: TouchStatus; asked: boolean; trigger: 'start' | 'focus'; everReady: boolean; focused: boolean }): boolean {
  if (!o.locked || !o.touch?.enabled || !o.touch.available || o.asked) return false
  return o.trigger === 'focus' || (!o.everReady && o.focused)
}
```

- [ ] **Step 4: Implement the button**

In `Unlock.tsx`, add `touchId` to the props:

```tsx
export function Unlock({ onUnlock, error, lockReason, storePath, autoHosts = [], onStop, touchId }: {
  onUnlock: (pw: string) => Promise<void>; error?: string; lockReason?: 'idle' | 'manual'; storePath?: string
  autoHosts?: string[]; onStop?: () => void; touchId?: { onUnlock: () => void; busy: boolean }
}) {
```

Insert this right before `<div className="field">`:

```tsx
        {touchId && (
          <button type="button" className="btn block" onClick={touchId.onUnlock} disabled={busy || touchId.busy}>Unlock with Touch ID</button>
        )}
```

- [ ] **Step 5: Wire `App.tsx`**

Add `autoTouch` to the `./shell` import. After the Task 4 `touch` effect, add:

```tsx
  const [touchBusy, setTouchBusy] = useState(false)
  const touchAsked = useRef(false) // per locked episode; reset by an unlock
  useEffect(() => { if (ready) touchAsked.current = false }, [ready])
  const touchUnlock = useCallback(async () => {
    touchAsked.current = true
    setTouchBusy(true)
    try {
      const r = await touchid.unlock()
      if ('cancelled' in r) return
      setUnlockError(undefined); setLockReason(undefined); setRanLocked(r.ranWhileLocked ?? [])
    } catch (e) {
      setUnlockError((e as Error).message)
      touchid.status().then(setTouch).catch(() => setTouch(undefined)) // a reset deleted the file
    } finally { setTouchBusy(false) }
    await refresh()
  }, [refresh])
  useEffect(() => {
    if (autoTouch({ locked, touch, asked: touchAsked.current, trigger: 'start', everReady, focused: document.hasFocus() })) void touchUnlock()
  }, [locked, touch, everReady, touchUnlock])
  useEffect(() => {
    if (!locked) return
    const onFocus = () => {
      if (autoTouch({ locked: true, touch, asked: touchAsked.current, trigger: 'focus', everReady, focused: true })) void touchUnlock()
    }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [locked, touch, everReady, touchUnlock])
```

These effects use `ready`, which is declared further down (`const ready = screen.kind === 'ready'`). Move that `const ready` line up to just after the `screen` constant, and delete its old line.

Pass the button to `Unlock`:

```tsx
        <Unlock onUnlock={unlock} error={screen.error} lockReason={lockReason} storePath={status?.storePath} autoHosts={lockHosts} onStop={stopAndLock}
          touchId={touch?.enabled && touch.available ? { onUnlock: () => void touchUnlock(), busy: touchBusy } : undefined} />
```

- [ ] **Step 6: Run the tests and the typecheck**

Run: `cd desktop && npx vitest run && npm run typecheck`
Expected: every test passes, and typecheck exits 0.

- [ ] **Step 7: Commit**

```bash
git add desktop/src/renderer/shell.ts desktop/src/renderer/Unlock.tsx desktop/src/renderer/App.tsx desktop/test/shell.test.ts desktop/test/Unlock.test.ts
git commit -m "feat(desktop): unlock screen asks Touch ID by button, at start, and on focus"
```

---

### Task 6: E2E against the fake helper

**Files:**
- Modify: `desktop/e2e/launch.ts`
- Create: `desktop/e2e/touchid.spec.ts`

**Interfaces:**
- Consumes: `launch`, `unlock` and `Launched` from `e2e/launch.ts`; the fake helper from Task 2; the UI from Tasks 4–5.
- Produces: `launch(extraEnv, { userDataDir?: boolean })`. With `userDataDir`, the app runs with `--user-data-dir=<tmp>/userdata`. On 2026-10-12, Electron 44 was checked to make `app.getPath('userData')` follow this flag.

- [ ] **Step 1: Add the launch option**

In `e2e/launch.ts`, change the `opts` type to `{ emptyStore?: boolean; sshConfig?: …; sftp?: boolean; userDataDir?: boolean }`. Document it in the comment above with one sentence: `userDataDir runs the app with --user-data-dir=<tmp>/userdata, so nothing touches the real userData (touchid.json).` Change the `electron.launch` args to:

```ts
    args: ['.', ...(opts.userDataDir ? [`--user-data-dir=${path.join(tmp, 'userdata')}`] : [])],
```

- [ ] **Step 2: Write the spec**

`desktop/e2e/touchid.spec.ts`:

```ts
import { test, expect } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { launch, unlock, type Launched } from './launch'

// Unlock with Touch ID (spec 2026-10-12-touchid-unlock-design.md) against the
// fake helper: no finger, the "seal" is base64. The real helper is the live
// check, not an e2e test. --user-data-dir keeps the real touchid.json out.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket; the fake helper is a shell script')
test.describe.configure({ mode: 'serial' })

const fake = path.resolve(__dirname, '..', 'test', 'fixtures', 'fake-touchid.sh')
const modeDir = fs.mkdtempSync('/tmp/smt')
const modeFile = path.join(modeDir, 'mode')
let l: Launched
const touchFile = () => path.join(l.tmp, 'userdata', 'touchid.json')
const lockBtn = () => l.app.firstWindow().then((w) => w.getByRole('button', { name: 'Lock', exact: true }))

test.beforeAll(async () => { l = await launch({ SSHGATE_TOUCHID_HELPER: fake, FAKE_TOUCHID_MODE: modeFile }, { userDataDir: true }) })
test.afterAll(async () => { await l?.close(); fs.rmSync(modeDir, { recursive: true, force: true }) })

test('Settings turns Touch ID on; a focus while locked unlocks with no typing', async () => {
  const win = await l.app.firstWindow()
  expect(await l.app.evaluate(({ app }) => app.getPath('userData'))).toBe(path.join(l.tmp, 'userdata'))
  await unlock(win)
  await expect(await lockBtn()).toBeVisible()

  await win.getByRole('button', { name: 'Settings' }).click()
  const d = win.getByRole('dialog', { name: 'Settings' })
  await d.getByRole('switch', { name: 'Unlock with Touch ID' }).check()
  const save = d.getByRole('button', { name: 'Save' })
  await expect(save).toBeDisabled()
  await d.getByLabel('Master password').fill('wrong')
  await expect(save).toBeEnabled()
  await save.click()
  await expect(d.locator('.error')).toContainText('wrong master password')
  expect(fs.existsSync(touchFile())).toBe(false)
  await d.getByLabel('Master password').fill('pw')
  await expect(save).toBeEnabled()
  await save.click()
  await expect(d).toBeHidden()
  expect(fs.statSync(touchFile()).mode & 0o777).toBe(0o600)

  await (await lockBtn()).click()
  await expect(win.getByRole('button', { name: 'Unlock with Touch ID' })).toBeVisible()
  await win.waitForTimeout(500)
  await expect(win.getByLabel('Master password')).toBeVisible() // locking alone never asks
  await win.evaluate(() => window.dispatchEvent(new Event('focus')))
  await expect(await lockBtn()).toBeVisible()
})

test('a changed fingerprint set turns Touch ID off, and the password still unlocks', async () => {
  const win = await l.app.firstWindow()
  fs.writeFileSync(modeFile, 'invalid')
  await (await lockBtn()).click()
  await win.getByRole('button', { name: 'Unlock with Touch ID' }).click()
  await expect(win.locator('.lockcard .error')).toContainText('Touch ID was turned off because your fingerprints changed')
  expect(fs.existsSync(touchFile())).toBe(false)
  await expect(win.getByRole('button', { name: 'Unlock with Touch ID' })).toHaveCount(0)
  await unlock(win)
  await expect(await lockBtn()).toBeVisible()
})
```

- [ ] **Step 3: Run it**

Run: `cd desktop && npm run build && npx playwright test e2e/touchid.spec.ts`
Expected: 2 passed.

- [ ] **Step 4: Run the whole e2e suite**

Run: `cd desktop && npm run e2e`
Expected: every spec passes. On a Mac with `<repo>/sshgate-touchid` built, the other specs still see no Touch ID button: their vault's `storePath` differs from any real `touchid.json`.

- [ ] **Step 5: Commit**

```bash
git add desktop/e2e/launch.ts desktop/e2e/touchid.spec.ts
git commit -m "test(desktop): e2e Touch ID unlock against a fake helper"
```

---

### Task 7: Docs, then the live check

**Files:**
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-10-12-touchid-unlock-design.md`

- [ ] **Step 1: Fix the two spec lines the plan changed**

In the spec's "Main process" section, change "Darwin only. On any other platform every call answers `available: false` and nothing spawns." to:

"With no helper (any platform but macOS, or a missing binary), every call answers `supported: false` and nothing spawns. The dev knob also works off macOS, which is how CI runs the e2e."

Change "Password bytes are passed as stdin buffers, never on argv or in the environment, and main keeps no copy after the call." to:

"Password bytes are passed as stdin buffers, never on argv or in the environment, and main does not store them; a JS string cannot be zeroed."

- [ ] **Step 2: `CLAUDE.md`**

- In the Commands block, after the `make-icon.sh` line, add:

  `(cd desktop && ./scripts/build-touchid.sh)   # macOS: build <repo root>/sshgate-touchid (Touch ID helper, ad-hoc signed; gitignored); package-mac.sh runs it and copies it into Contents/Resources`

- In the `desktop/` section, add a bullet after the Auto-allow one:

  "- Touch ID unlock (`main/touchid.ts`, `native/touchid/main.swift`, spec `2026-10-12-touchid-unlock-design.md`): opt-in from Settings' Unlock section, which takes the master password again, checks it with the hub's `unlock`, and has `sshgate-touchid enroll` seal it to a Secure Enclave key (`.biometryCurrentSet`). main writes `userData/touchid.json` (`{storePath, sealed}`, 0600). The unlock screen's **Unlock with Touch ID** button (and `autoTouch`: once per locked episode, at start with focus or on the window's focus) calls `touchid:unlock`: main runs the helper and sends the password to the hub's `unlock` itself, so it never reaches the renderer. Exit 3, or the hub's `wrong master password`, deletes the file. A typed unlock or a hub stop kills a running helper. Helper path: `SSHGATE_TOUCHID_HELPER` (ignored when packaged), else `sshgate-touchid` beside the app dir. The hub knows nothing of it."

- In Conventions, after the first bullet, add:

  "- One exception to "no secret persistence outside the encrypted store": `touchid.json` holds the master password sealed to this Mac's Secure Enclave, behind Touch ID, opt-in (spec `2026-10-12-touchid-unlock-design.md`, Security). Changing the master password, if a flow is ever added, must delete it."

- [ ] **Step 3: `README.md`**

In "## The desktop app", after the Auto-allow subsection ends (before "## Safety model"), add:

```markdown
### Unlock with Touch ID (macOS)

Open **Settings**, turn on **Unlock with Touch ID**, and type your master password once. From then on the unlock screen has an **Unlock with Touch ID** button and asks by itself when you open the app or come back to its window. The password field always works.

The app keeps your master password in `~/Library/Application Support/sshgate/touchid.json`, encrypted to a key inside your Mac's Secure Enclave that only opens with a fingerprint enrolled today. The file is useless on another machine. Adding or removing a fingerprint turns Touch ID off; unlock with the password and turn it on again. Turning it off in Settings deletes the file. It does not protect against someone at your unlocked Mac who can use your finger.
```

In "## Files and environment variables", add a line for `~/Library/Application Support/sshgate/touchid.json` in the same format as the neighbouring entries: "Touch ID unlock's sealed master password (macOS, only when turned on)".

- [ ] **Step 4: Full verification**

```bash
cd /Users/lang/GolandProjects/github.com/lang315/ssh-mcp
go vet ./... && go test -short ./...
cd desktop && npm run typecheck && npm test && npm run e2e
```

Expected: every command exits 0. Go is unchanged, but the commands confirm nothing else broke.

- [ ] **Step 5: Commit the docs**

```bash
git add CLAUDE.md README.md docs/superpowers/specs/2026-10-12-touchid-unlock-design.md
git commit -m "docs: Touch ID unlock"
```

- [ ] **Step 6: Live check with the packaged app (ask the author; needs their finger)**

1. Quit sshgate. Run `(cd desktop && ./scripts/package-mac.sh)`, then `rm -rf /Applications/sshgate.app && ditto desktop/out/sshgate.app /Applications/sshgate.app`. Confirm with the author first: this replaces the installed app.
2. The author opens the app, unlocks with the password, turns on Touch ID in Settings, and quits.
3. The author reopens the app. Expected: the Touch ID dialog appears by itself ("unlock the sshgate vault"), a finger unlocks it, nothing is typed.
4. Lock, switch to another app, switch back. Expected: Touch ID asks once; Cancel leaves the unlock screen with no error, and switching away and back again does not ask a second time.
5. Fingerprint change: the author adds a fingerprint in System Settings → Touch ID. Then Lock, and click **Unlock with Touch ID**. Expected: the "Touch ID was turned off…" message, and `touchid.json` gone. If the message is something else, read main's stderr (`/Applications/sshgate.app/Contents/MacOS/Electron` run from a terminal), map that error to exit 3 in `knownCode` in `main.swift`, and commit `fix(desktop): map <error> to exit 3`. The author removes the extra fingerprint afterwards.
6. Record each result (pass/fail with what was seen) for the PR body's evidence.

- [ ] **Step 7: Final whole-branch review**

Use superpowers:requesting-code-review on `main..feat/touchid-unlock`.
