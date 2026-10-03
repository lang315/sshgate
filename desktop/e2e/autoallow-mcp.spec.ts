import { test, expect, type Dialog, type Page } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { launch, type Launched } from './launch'
import { card, editAuto, enable, home, Mcp, unlockDone } from './autoHelpers'

// Auto-allow end to end through the real MCP bridge (the `sshgate` stdio
// server an AI client runs), checked against the hub's audit log: the
// approval path, timed and forever grants from the Host editor, the sudo-exec
// and root-host opt-ins, Stop, and lock. Spec 2026-09-28-auto-allow-design.md,
// amended 2026-09-29.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')
// One app for the whole file: each test builds on the previous one's state.
test.describe.configure({ mode: 'serial' })

let l: Launched
let mcp: Mcp
test.beforeAll(async () => {
  l = await launch()
  mcp = new Mcp(path.join(l.tmp, 'bin', 'sshgate'), l.tmp)
  await mcp.call('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'e2e-mcp', version: '1' } })
  mcp.notify('notifications/initialized', {})
})
test.afterAll(async () => { mcp?.close(); await l?.close() })

const exec = (server: string, command: string) => mcp.tool('exec', { server, command, description: 'mcp e2e' })
const sudoExec = (server: string, command: string) => mcp.tool('sudo-exec', { server, command, description: 'mcp e2e' })

type Rec = Record<string, any>
const audit = (): Rec[] => fs.readFileSync(path.join(l.tmp, 'store', 'audit.jsonl'), 'utf8').trim().split('\n').map((s) => JSON.parse(s))
const execRec = (command: string): Rec | undefined => audit().filter((r) => r.command === command).pop()
const configRecs = (action: string, server: string): Rec[] => audit().filter((r) => r.kind === 'config' && r.action === action && r.server === server)

// waitThenDecide checks a request is waiting in the AI column, then allows or denies it.
async function waitThenDecide(win: Page, command: string, decision: 'allow' | 'deny') {
  const row = win.locator('.approval').filter({ hasText: command })
  await expect(row).toBeVisible()
  if (decision === 'deny') return row.locator('input').press('Enter')
  const allow = row.getByRole('button', { name: 'Allow', exact: true })
  await expect(allow).toBeEnabled({ timeout: 2000 })
  await allow.click()
}

test('baseline: an MCP exec waits for a human decision', async () => {
  const win = await l.app.firstWindow()
  await unlockDone(win)
  const out = exec('box', 'echo base')
  await waitThenDecide(win, 'echo base', 'allow')
  expect(await out).toContain('echo base')
  const r = execRec('echo base')!
  expect(r.outcome).toBe('allowed')
  expect(r.approval).toBeUndefined()
  expect(r.waitMs).toBeGreaterThan(0)
})

test('timed grant from the editor: MCP execs run with no decision; sudo-exec still asks', async () => {
  const win = await l.app.firstWindow()
  await editAuto(win, 'box', '15m')
  await enable(win, 'box')
  await expect(card(win, 'box').locator('.chip.auto')).toHaveText(/Auto 1[45]m/)
  const on = configRecs('autoAllowOn', 'box').pop()!
  expect(on.until).toBeTruthy()
  expect(on.forever).toBeFalsy()

  expect(await exec('box', 'echo auto-1')).toContain('echo auto-1')
  expect(await exec('box', 'echo auto-2')).toContain('echo auto-2')
  for (const c of ['echo auto-1', 'echo auto-2']) {
    expect(execRec(c)).toMatchObject({ outcome: 'allowed', approval: 'auto' })
  }
  // The AI column opened itself for the baseline request and stays open.
  await expect(win.getByRole('region', { name: 'Auto-allowed' })).toContainText('echo auto-2')

  const sudo = sudoExec('box', 'echo sudo-asks')
  sudo.catch(() => {})
  await waitThenDecide(win, 'echo sudo-asks', 'deny')
  await expect(sudo).rejects.toThrow(/Denied/)
  expect(execRec('echo sudo-asks')).toMatchObject({ outcome: 'denied', sudo: true })
})

test('sudo-exec opt-in: sudo-exec runs with no decision and is tagged', async () => {
  const win = await l.app.firstWindow()
  await editAuto(win, 'box', '15m', { sudo: true })
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await expect(d).toContainText('sudo-exec will run without asking: the AI has full root on this host')
  await enable(win, 'box')
  expect(configRecs('save', 'box').pop()!.changed.join(' ')).toContain('autoAllowSudo: false → true')

  expect(await sudoExec('box', 'echo sudo-auto')).toContain('echo sudo-auto')
  expect(execRec('echo sudo-auto')).toMatchObject({ outcome: 'allowed', approval: 'auto', sudo: true })
  // The tag, not the text: the command itself contains "sudo".
  await expect(win.getByRole('region', { name: 'Auto-allowed' }).locator('li', { hasText: 'echo sudo-auto' }).locator('.chip.danger')).toHaveText('sudo')
})

test('Stop on the card ends the grant', async () => {
  const win = await l.app.firstWindow()
  await home(win)
  await win.getByRole('button', { name: 'Stop auto-allow box', exact: true }).click()
  await expect(card(win, 'box').locator('.chip.auto')).toHaveCount(0)
  expect(configRecs('autoAllowOff', 'box').pop()!.reason).toBe('turned off')
  const out = exec('box', 'echo after-stop')
  out.catch(() => {})
  await waitThenDecide(win, 'echo after-stop', 'deny')
  await expect(out).rejects.toThrow(/Denied/)
})

test('root host: refused until "Allow on root hosts" is ticked, then auto', async () => {
  const win = await l.app.firstWindow()
  const hosts = win.locator('nav.hosts')
  const editor = win.getByRole('dialog', { name: 'Host editor' })
  await home(win)
  await hosts.getByRole('button', { name: 'New host' }).click()
  await editor.getByLabel('Label', { exact: true }).fill('rootbox')
  await editor.getByLabel('Address', { exact: true }).fill('127.0.0.1')
  await editor.getByLabel('Port', { exact: true }).fill(String(l.port))
  await editor.getByLabel('User', { exact: true }).fill('root')
  await editor.getByLabel('Password', { exact: true }).fill('rootpass')
  await editor.getByRole('button', { name: 'Save' }).click()
  await expect(editor).toBeHidden()
  // Pin its key through the terminal's trust prompt.
  await hosts.getByRole('button', { name: 'rootbox', exact: true }).locator('.tile').click()
  const prompt = win.getByRole('dialog', { name: 'Unknown host key' })
  const trust = prompt.getByRole('button', { name: 'Trust' })
  await expect(trust).toBeEnabled({ timeout: 2000 })
  await trust.click()
  await expect(prompt).toBeHidden()

  // First save makes rootbox visible and asks for 15 min. The hub listed it as
  // "not visible to AI", so the app can't foresee the root refusal: the hub
  // refuses, the save stands, and the app says so.
  const alerts: string[] = []
  const onDialog = (a: Dialog) => { alerts.push(a.message()); void a.accept() }
  win.on('dialog', onDialog)
  await editAuto(win, 'rootbox', '15m', { visible: true, root: false })
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on rootbox' })
  await enable(win, 'rootbox')
  await expect.poll(() => alerts).toEqual(['Saved, but auto-allow was not turned on: auto-allow refused: root login'])
  expect(configRecs('autoAllowOn', 'rootbox')).toHaveLength(0)
  expect(configRecs('save', 'rootbox').pop()!.changed.join(' ')).toContain('aiVisible: false → true')
  await expect(card(win, 'rootbox').locator('.chip.auto')).toHaveCount(0)
  win.off('dialog', onDialog)

  // Now visible, the app knows the refusal up front and Enable stays disabled.
  await editAuto(win, 'rootbox', '15m', { root: false })
  await expect(d).toContainText('root login')
  await win.waitForTimeout(600)
  await expect(d.getByRole('button', { name: 'Enable' })).toBeDisabled()
  await d.getByRole('button', { name: 'Cancel' }).click()
  await editor.getByRole('button', { name: 'Close', exact: true }).click()
  expect(configRecs('autoAllowOn', 'rootbox')).toHaveLength(0)

  await editAuto(win, 'rootbox', '15m', { root: true })
  await expect(d).toContainText('Allowing root')
  await enable(win, 'rootbox')
  await expect(card(win, 'rootbox').locator('.chip.auto')).toHaveText(/Auto 1[45]m/)
  expect(await exec('rootbox', 'echo root-auto')).toContain('echo root-auto')
  expect(execRec('echo root-auto')).toMatchObject({ outcome: 'allowed', approval: 'auto' })
  await win.getByRole('button', { name: 'Stop auto-allow rootbox', exact: true }).click()
  await expect(card(win, 'rootbox').locator('.chip.auto')).toHaveCount(0)
})

test('Stop auto-allow and lock ends a timed grant', async () => {
  const win = await l.app.firstWindow()
  await editAuto(win, 'box', '15m')
  await enable(win, 'box')
  await expect(card(win, 'box').locator('.chip.auto')).toHaveCount(1)
  await win.getByRole('button', { name: 'Lock', exact: true }).click()
  await win.getByRole('button', { name: 'Stop auto-allow and lock' }).click()
  await unlockDone(win)
  await expect(card(win, 'box').locator('.chip.auto')).toHaveCount(0)
  expect(configRecs('autoAllowOff', 'box').pop()!.reason).toBe('locked')
  const out = exec('box', 'echo after-lock')
  out.catch(() => {})
  await waitThenDecide(win, 'echo after-lock', 'deny')
  await expect(out).rejects.toThrow(/Denied/)
  expect(execRec('echo after-lock')!.approval).toBeUndefined()
})

test('forever grant is paused after Stop auto-allow and lock until Resume', async () => {
  const win = await l.app.firstWindow()
  await editAuto(win, 'box', 'forever')
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await d.getByLabel('Type box to confirm').fill('box')
  await enable(win, 'box')
  await expect(card(win, 'box').locator('.chip.auto')).toHaveText('Auto ∞')
  expect(configRecs('autoAllowOn', 'box').pop()!.forever).toBe(true)

  await win.getByRole('button', { name: 'Lock', exact: true }).click()
  await win.getByRole('button', { name: 'Stop auto-allow and lock' }).click()
  await unlockDone(win)
  await expect(win.getByText('Auto-allow is paused on box.').first()).toBeVisible()
  const out = exec('box', 'echo paused-asks')
  out.catch(() => {})
  await waitThenDecide(win, 'echo paused-asks', 'deny')
  await expect(out).rejects.toThrow(/Denied/)

  await win.waitForTimeout(600)
  await win.getByRole('button', { name: 'Resume' }).first().click()
  await expect(card(win, 'box').locator('.chip.auto')).toHaveText('Auto ∞')
  expect(await exec('box', 'echo resumed')).toContain('echo resumed')
  expect(execRec('echo resumed')).toMatchObject({ outcome: 'allowed', approval: 'auto' })
  await win.getByRole('button', { name: 'Stop auto-allow box', exact: true }).click()
  await expect(card(win, 'box').locator('.chip.auto')).toHaveCount(0)
})
