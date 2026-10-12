import { test, expect } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { launch, type Launched } from './launch'
import { editAuto, enable, unlockDone } from './autoHelpers'

// The soft-lock limit setting (spec 2026-10-12-soft-lock-limit-design.md).
// The 24-hour wait itself is the exit gate, not an e2e test.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')
test.describe.configure({ mode: 'serial' })

let l: Launched
test.beforeAll(async () => { l = await launch() })
test.afterAll(async () => { await l?.close() })

type Rec = Record<string, any>
const store = (): Rec => JSON.parse(fs.readFileSync(path.join(l.tmp, 'store', 'servers.json'), 'utf8'))
const audit = (): Rec[] => fs.readFileSync(path.join(l.tmp, 'store', 'audit.jsonl'), 'utf8').trim().split('\n').map((s) => JSON.parse(s))

test('Settings sets No limit, stored in the vault', async () => {
  const win = await l.app.firstWindow()
  await expect(win.getByLabel('Master password')).toBeVisible()
  await expect(win.getByRole('button', { name: 'Settings' })).toHaveCount(0)
  await unlockDone(win)
  await editAuto(win, 'box', 'forever')
  await win.getByRole('dialog', { name: 'Auto-allow AI commands on box' }).getByLabel('Type box to confirm').fill('box')
  await enable(win, 'box')

  const gear = win.getByRole('button', { name: 'Settings' })
  await expect(gear).toBeEnabled()
  await gear.click()
  let d = win.getByRole('dialog', { name: 'Settings' })
  await expect(d.getByLabel('Keep running while locked')).toHaveValue('24h')
  await win.keyboard.press('Enter') // Cancel has focus and is the submit
  await expect(d).toBeHidden()

  await gear.click()
  d = win.getByRole('dialog', { name: 'Settings' })
  await d.getByLabel('Keep running while locked').selectOption('7d')
  await expect(d.locator('.banner-wait')).toContainText('for up to 7 days')
  await expect(d.locator('.banner-wait li')).toHaveText(['box'])
  await expect(d.locator('.banner-danger')).toHaveCount(0)
  await expect(d.getByRole('button', { name: 'Save' })).toHaveClass(/danger-outline/)

  await d.getByLabel('Keep running while locked').selectOption('none')
  const save = d.getByRole('button', { name: 'Save' })
  await expect(save).toBeDisabled()
  await expect(d.locator('.banner-danger')).toContainText('for as long as the app runs')
  await expect(d.locator('.banner-danger li')).toHaveText(['box'])
  await expect(save).toBeEnabled()
  await save.click()
  await expect(d).toBeHidden()

  expect(store().softLockLimit).toBe('none')
  expect(audit().filter((r) => r.kind === 'config' && r.action === 'softLockLimit').pop()).toMatchObject({ limit: 'none' })
})

test('the value survives a lock, and the consent text follows it', async () => {
  const win = await l.app.firstWindow()
  await win.getByRole('button', { name: 'Lock', exact: true }).click()
  await expect(win.getByRole('button', { name: 'Settings' })).toBeHidden()
  await unlockDone(win)

  await win.getByRole('button', { name: 'Settings' }).click()
  const d = win.getByRole('dialog', { name: 'Settings' })
  await expect(d.getByLabel('Keep running while locked')).toHaveValue('none')
  await win.keyboard.press('Escape')
  await expect(d).toBeHidden()

  await editAuto(win, 'box', 'off')
  await expect(win.getByRole('dialog', { name: 'Host editor' })).toBeHidden()
  await editAuto(win, 'box', 'forever')
  const consent = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await expect(consent).toContainText('with no time limit')
  await expect(consent).not.toContainText('24 hours')
  await consent.getByRole('button', { name: 'Cancel' }).click()
})
