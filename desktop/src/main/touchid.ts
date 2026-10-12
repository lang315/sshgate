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
