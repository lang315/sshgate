import { test, expect } from '@playwright/test'
import { launch, unlock, type Launched } from './launch'
import { doorCall } from './doorClient'

// Spec 2026-09-28-auto-allow-design.md.
test.skip(process.platform === 'win32', 'door client uses a Unix socket')

let l: Launched
test.beforeAll(async () => { l = await launch() })
test.afterAll(async () => { await l?.close() })

const exec = (id: string, command: string) =>
  doorCall(l.socket, 'exec', { requestId: id, client: 'e2e', server: 'box', command, description: 'auto e2e' })

test('timed auto-allow runs exec without a click, and Stop and Lock end it', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  await win.getByRole('button', { name: 'Auto-allow box', exact: true }).click()
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await d.getByRole('radio', { name: '15 min' }).check()
  await win.waitForTimeout(600)
  await d.getByRole('button', { name: 'Enable' }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveText(/Auto 1[45]m/)

  const out = await exec('a1', 'echo auto-one') // no decision is made anywhere
  expect(JSON.stringify(out)).toContain('echo auto-one')
  await win.getByRole('button', { name: 'AI requests' }).click()
  await expect(win.getByRole('region', { name: 'Auto-allowed' })).toContainText('echo auto-one')

  await win.getByRole('button', { name: 'Stop auto-allow box', exact: true }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveCount(0)
  const waiting = exec('a2', 'echo must-ask')
  waiting.catch(() => {}) // denied below; avoids a transient unhandled-rejection warning
  await expect(win.locator('.approval')).toContainText('echo must-ask')
  await win.locator('.approval input').press('Enter') // deny
  await expect(waiting).rejects.toThrow(/Denied/)

  await win.getByRole('button', { name: 'Auto-allow box', exact: true }).click()
  await win.waitForTimeout(600)
  await win.getByRole('dialog').getByRole('button', { name: 'Enable' }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveCount(1)
  await win.getByRole('button', { name: 'Lock' }).click()
  await unlock(win)
  await expect(win.locator('.hostcard .chip.auto')).toHaveCount(0)
})

test('forever auto-allow pauses after unlock until Resume', async () => {
  const win = await l.app.firstWindow()
  await win.locator('.tabbar .hometab').click()
  await win.getByRole('button', { name: 'Auto-allow box', exact: true }).click()
  const d = win.getByRole('dialog')
  await d.getByRole('radio', { name: 'Until turned off' }).check()
  await d.getByLabel('Type box to confirm').fill('box')
  await win.waitForTimeout(600)
  await d.getByRole('button', { name: 'Enable' }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveText('Auto ∞')

  await win.getByRole('button', { name: 'Lock' }).click()
  await unlock(win)
  await expect(win.getByText('Auto-allow is paused on box.').first()).toBeVisible()
  const waiting = exec('f1', 'echo paused-asks')
  waiting.catch(() => {}) // denied below; avoids a transient unhandled-rejection warning
  await expect(win.locator('.approval')).toContainText('echo paused-asks')
  await win.locator('.approval input').press('Enter')
  await expect(waiting).rejects.toThrow(/Denied/)

  await win.waitForTimeout(600)
  await win.getByRole('button', { name: 'Resume' }).first().click()
  const out = await exec('f2', 'echo resumed')
  expect(JSON.stringify(out)).toContain('echo resumed')
  await win.getByRole('button', { name: 'Stop auto-allow box', exact: true }).click()
})
