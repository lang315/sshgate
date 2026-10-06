import { test, expect } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { launch, type Launched } from './launch'
import { card, editAuto, enable, home, Mcp, unlockDone } from './autoHelpers'

// Soft lock end to end (spec 2026-10-02-soft-lock-design.md): with the idle
// period cut to 5 s, a host on auto-allow keeps running through the real MCP
// bridge while the app shows the unlock screen. Its own launch: a short idle
// lock would soft-lock every test in autoallow-mcp.spec.ts.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')
test.describe.configure({ mode: 'serial' })

let l: Launched
let mcp: Mcp
test.beforeAll(async () => {
  l = await launch({ SSHGATE_IDLE_LOCK: '5s' }) // 3 s can fire while the grant is being armed
  mcp = new Mcp(path.join(l.tmp, 'bin', 'sshgate'), l.tmp)
  await mcp.call('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'e2e-softlock', version: '1' } })
  mcp.notify('notifications/initialized', {})
})
test.afterAll(async () => { mcp?.close(); await l?.close() })

const exec = (server: string, command: string) => mcp.tool('exec', { server, command, description: 'softlock e2e' })
type Rec = Record<string, any>
const audit = (): Rec[] => fs.readFileSync(path.join(l.tmp, 'store', 'audit.jsonl'), 'utf8').trim().split('\n').map((s) => JSON.parse(s))

test('a second visible host with no grant', async () => {
  const win = await l.app.firstWindow()
  await unlockDone(win)
  const hosts = win.locator('nav.hosts')
  const editor = win.getByRole('dialog', { name: 'Host editor' })
  await home(win)
  await hosts.getByRole('button', { name: 'New host' }).click()
  await editor.getByLabel('Label', { exact: true }).fill('other')
  await editor.getByLabel('Address', { exact: true }).fill('127.0.0.1')
  await editor.getByLabel('Port', { exact: true }).fill(String(l.port))
  await editor.getByLabel('User', { exact: true }).fill('u2')
  await editor.getByLabel('Password', { exact: true }).fill('x')
  await editor.locator('summary', { hasText: 'AI access' }).click()
  await editor.getByLabel('Visible to AI').check()
  await editor.getByRole('button', { name: 'Save' }).click()
  await expect(editor).toBeHidden()
  // Pin its key through the terminal's trust prompt.
  await hosts.getByRole('button', { name: 'other', exact: true }).locator('.tile').click()
  const prompt = win.getByRole('dialog', { name: 'Unknown host key' })
  const trust = prompt.getByRole('button', { name: 'Trust' })
  await expect(trust).toBeEnabled({ timeout: 2000 })
  await trust.click()
  await expect(prompt).toBeHidden()
})

test('idle with a grant locks the app and keeps the AI running on that host', async () => {
  const win = await l.app.firstWindow()
  await editAuto(win, 'box', 'forever')
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await d.getByLabel('Type box to confirm').fill('box')
  await enable(win, 'box')
  await expect(card(win, 'box').locator('.chip.auto')).toHaveText('Auto ∞')

  // No input: DOM waits are not hub calls, so the idle clock runs.
  await expect(win.getByLabel('Master password')).toBeVisible({ timeout: 20000 })
  await expect(win.getByText('Locked after inactivity.')).toBeVisible()
  await expect(win.getByText('AI auto-allow is still running on: box')).toBeVisible()

  expect(await exec('box', 'echo soft-1')).toContain('echo soft-1')
  const recs = audit()
  const soft = recs.findIndex((r) => r.kind === 'config' && r.action === 'softLock')
  const ran = recs.findIndex((r) => r.command === 'echo soft-1')
  expect(soft).toBeGreaterThanOrEqual(0)
  expect(recs[soft].servers).toEqual(['box'])
  expect(ran).toBeGreaterThan(soft)
  expect(recs[ran]).toMatchObject({ outcome: 'allowed', approval: 'auto' })

  await expect(exec('other', 'echo soft-other')).rejects.toThrow(/Vault is locked/)
  expect(audit().some((r) => r.command === 'echo soft-other')).toBe(false)
  const list = await mcp.tool('list-servers', {})
  expect(list).toMatch(/- other \(127\.0\.0\.1\) \[locked: unlock the app\]/)
  expect(list).toMatch(/- box \([^)]+\)\n/)
  expect(list).not.toMatch(/box \([^)]+\) \[locked/)
})

test('unlock keeps the grant and says what ran', async () => {
  const win = await l.app.firstWindow()
  await unlockDone(win)
  await expect(win.getByText(/While the app was locked the AI ran 1 command on box/)).toBeVisible()
  await expect(win.getByText(/Auto-allow is paused/)).toHaveCount(0)
  await home(win)
  await expect(card(win, 'box').locator('.chip.auto')).toHaveText('Auto ∞')
  expect(await exec('box', 'echo soft-2')).toContain('echo soft-2')
  expect(audit().filter((r) => r.command === 'echo soft-2').pop()).toMatchObject({ outcome: 'allowed', approval: 'auto' })
})

test('the unlock screen stops auto-allow without the password', async () => {
  const win = await l.app.firstWindow()
  await expect(win.getByLabel('Master password')).toBeVisible({ timeout: 20000 })
  await expect(win.getByText('AI auto-allow is still running on: box')).toBeVisible()
  await win.getByRole('button', { name: 'Stop auto-allow and lock' }).click()
  await expect(win.getByText('AI auto-allow is still running on: box')).toHaveCount(0)
  await expect(exec('box', 'echo soft-3')).rejects.toThrow(/Vault is locked/)
  expect(audit().filter((r) => r.kind === 'config' && r.action === 'autoAllowOff' && r.server === 'box').pop()!.reason).toBe('locked')
})
