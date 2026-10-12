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
