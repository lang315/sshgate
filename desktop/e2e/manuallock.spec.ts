import { test, expect } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { launch, type Launched } from './launch'
import { card, editAuto, enable, Mcp, unlockDone } from './autoHelpers'

// The Lock button keeps auto-allow running (spec 2026-10-03-manual-soft-lock-design.md),
// end to end through the real MCP bridge; the unlock screen's stop ends it.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')
test.describe.configure({ mode: 'serial' })

let l: Launched
let mcp: Mcp
test.beforeAll(async () => {
  l = await launch()
  mcp = new Mcp(path.join(l.tmp, 'bin', 'sshgate'), l.tmp)
  await mcp.call('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'e2e-manuallock', version: '1' } })
  mcp.notify('notifications/initialized', {})
})
test.afterAll(async () => { mcp?.close(); await l?.close() })

const exec = (server: string, command: string) => mcp.tool('exec', { server, command, description: 'manual lock e2e' })
type Rec = Record<string, any>
const audit = (): Rec[] => fs.readFileSync(path.join(l.tmp, 'store', 'audit.jsonl'), 'utf8').trim().split('\n').map((s) => JSON.parse(s))

test('the Lock button keeps auto-allow running', async () => {
  const win = await l.app.firstWindow()
  await unlockDone(win)
  await editAuto(win, 'box', 'forever')
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await d.getByLabel('Type box to confirm').fill('box')
  await enable(win, 'box')
  await expect(card(win, 'box').locator('.chip.auto')).toHaveText('Auto ∞')

  const lock = win.getByRole('button', { name: 'Lock', exact: true })
  await expect(lock).toHaveAttribute('title', 'Auto-allow keeps running while locked')
  await lock.click()
  await expect(win.getByLabel('Master password')).toBeVisible()
  await expect(win.getByText('AI auto-allow is still running on: box')).toBeVisible()

  expect(await exec('box', 'echo manual-1')).toContain('echo manual-1')
  const recs = audit()
  const soft = recs.findIndex((r) => r.kind === 'config' && r.action === 'softLock')
  const ran = recs.findIndex((r) => r.command === 'echo manual-1')
  expect(recs[soft]).toMatchObject({ reason: 'manual', servers: ['box'] })
  expect(ran).toBeGreaterThan(soft)
  expect(recs[ran]).toMatchObject({ outcome: 'allowed', approval: 'auto' })
})

test('the unlock screen stop still ends it', async () => {
  const win = await l.app.firstWindow()
  await win.getByRole('button', { name: 'Stop auto-allow and lock' }).click()
  await expect(win.getByText('AI auto-allow is still running on: box')).toHaveCount(0)
  await expect(exec('box', 'echo manual-2')).rejects.toThrow(/Vault is locked/)
  expect(audit().filter((r) => r.kind === 'config' && r.action === 'autoAllowOff' && r.server === 'box').pop()!.reason).toBe('locked')
})
