import { expect, type Page } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import * as readline from 'node:readline'
import { unlock } from './launch'

// Mcp speaks newline-delimited JSON-RPC to one bridge process, as an AI client does.
export class Mcp {
  private p: ChildProcess
  private id = 0
  private waiting = new Map<number, { resolve: (m: any) => void; reject: (e: Error) => void }>()
  private dead?: Error
  constructor(bin: string, runtimeDir: string) {
    this.p = spawn(bin, [], { env: { ...process.env, SSHGATE_RUNTIME_DIR: runtimeDir }, stdio: ['pipe', 'pipe', 'inherit'] })
    readline.createInterface({ input: this.p.stdout! }).on('line', (line) => {
      let m: any
      try { m = JSON.parse(line) } catch { return this.fail(new Error(`bridge wrote a non-JSON line: ${line}`)) }
      this.waiting.get(m.id)?.resolve(m)
      this.waiting.delete(m.id)
    })
    // A bridge that dies or cannot start fails every waiting call at once,
    // instead of leaving it to the test timeout.
    this.p.on('error', (e) => this.fail(e))
    this.p.on('exit', (code, sig) => this.fail(new Error(`bridge exited (code ${code}, signal ${sig})`)))
  }
  private fail(e: Error) {
    this.dead ??= e
    for (const w of this.waiting.values()) w.reject(e)
    this.waiting.clear()
  }
  call(method: string, params: unknown): Promise<any> {
    if (this.dead) return Promise.reject(this.dead)
    const id = ++this.id
    return new Promise((resolve, reject) => {
      this.waiting.set(id, { resolve: (m) => (m.error ? reject(new Error(m.error.message)) : resolve(m.result)), reject })
      this.p.stdin!.write(JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n')
    })
  }
  notify(method: string, params: unknown) { this.p.stdin!.write(JSON.stringify({ jsonrpc: '2.0', method, params }) + '\n') }
  // tool returns the tool's text, or throws it when the tool reports an error.
  async tool(name: string, args: Record<string, unknown>): Promise<string> {
    const r = await this.call('tools/call', { name, arguments: args })
    const text = (r.content ?? []).map((c: any) => c.text ?? '').join('')
    if (r.isError) throw new Error(text)
    return text
  }
  close() { this.p.stdin?.end(); this.p.kill() }
}

// unlockDone waits until the unlock has landed: an AI exec sent before it is refused as locked.
export async function unlockDone(win: Page) {
  await unlock(win)
  await expect(win.getByLabel('Master password')).toBeHidden()
}

export async function home(win: Page) { await win.locator('.tabbar .hometab').click() }

// Opens the editor on server, picks mode in AI access with the given opt-ins, and Saves.
export async function editAuto(win: Page, server: string, mode: string, opts: { root?: boolean; sudo?: boolean; visible?: boolean } = {}) {
  await home(win)
  await win.locator('nav.hosts').getByRole('button', { name: `Edit ${server}` }).click()
  const editor = win.getByRole('dialog', { name: 'Host editor' })
  await editor.locator('summary', { hasText: 'AI access' }).click()
  if (opts.visible) await editor.getByLabel('Visible to AI').check()
  await editor.getByLabel('Auto-allow', { exact: true }).selectOption(mode)
  if (opts.root !== undefined) await editor.getByLabel('Allow on root hosts').setChecked(opts.root)
  if (opts.sudo !== undefined) await editor.getByLabel('Also auto-allow sudo-exec').setChecked(opts.sudo)
  await editor.getByRole('button', { name: 'Save' }).click()
}

export async function enable(win: Page, server: string) {
  const d = win.getByRole('dialog', { name: `Auto-allow AI commands on ${server}` })
  await win.waitForTimeout(600)
  await d.getByRole('button', { name: 'Enable' }).click()
  await expect(win.getByRole('dialog', { name: 'Host editor' })).toBeHidden()
}

export const card = (win: Page, server: string) => win.locator('.hostcard').filter({ has: win.getByRole('button', { name: server, exact: true }) })
