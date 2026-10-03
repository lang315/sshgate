import { test, expect } from '@playwright/test'
import { launch, unlock, type Launched } from './launch'
import { doorCall } from './doorClient'

// Spec 2026-09-28-auto-allow-design.md, amended 2026-09-29 (control in the Host editor).
test.skip(process.platform === 'win32', 'door client uses a Unix socket')

let l: Launched
test.beforeAll(async () => { l = await launch() })
test.afterAll(async () => { await l?.close() })

const exec = (id: string, command: string) =>
  doorCall(l.socket, 'exec', { requestId: id, client: 'e2e', server: 'box', command, description: 'auto e2e' })
const sudoExec = (id: string, command: string) =>
  doorCall(l.socket, 'sudoExec', { requestId: id, client: 'e2e', server: 'box', command, description: 'auto e2e' })

// Opens the Host editor on box, expands AI access, picks a duration, and Saves.
// The caller waits for the confirm dialog and clicks Enable (or Cancel).
async function chooseAutoAllow(win: import('@playwright/test').Page, mode: string, opts: { sudo?: boolean } = {}) {
  await win.locator('.tabbar .hometab').click()
  await win.locator('nav.hosts').getByRole('button', { name: 'Edit box' }).click()
  const editor = win.getByRole('dialog', { name: 'Host editor' })
  await editor.locator('summary', { hasText: 'AI access' }).click()
  await editor.getByLabel('Auto-allow', { exact: true }).selectOption(mode)
  if (opts.sudo) await editor.getByLabel('Also auto-allow sudo-exec').check()
  await editor.getByRole('button', { name: 'Save' }).click()
}

test('timed auto-allow runs exec without a click, and Stop auto-allow and lock ends it', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  const editor = win.getByRole('dialog', { name: 'Host editor' })
  await chooseAutoAllow(win, '15m')
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await win.waitForTimeout(600)
  await d.getByRole('button', { name: 'Enable' }).click()
  await expect(editor).toBeHidden()
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

  await chooseAutoAllow(win, '15m')
  await win.waitForTimeout(600)
  await d.getByRole('button', { name: 'Enable' }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveCount(1)
  await win.getByRole('button', { name: 'Lock', exact: true }).click()
  await win.getByRole('button', { name: 'Stop auto-allow and lock' }).click()
  await unlock(win)
  await expect(win.locator('.hostcard .chip.auto')).toHaveCount(0)
  const afterLock = exec('a3', 'echo after-lock')
  afterLock.catch(() => {}) // denied below; avoids a transient unhandled-rejection warning
  await expect(win.locator('.approval')).toContainText('echo after-lock')
  await win.locator('.approval input').press('Enter') // deny
  await expect(afterLock).rejects.toThrow(/Denied/)
})

test('forever auto-allow is paused after Stop auto-allow and lock until Resume', async () => {
  const win = await l.app.firstWindow()
  await chooseAutoAllow(win, 'forever')
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await d.getByLabel('Type box to confirm').fill('box')
  await win.waitForTimeout(600)
  await d.getByRole('button', { name: 'Enable' }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveText('Auto ∞')

  await win.getByRole('button', { name: 'Lock', exact: true }).click()
  await win.getByRole('button', { name: 'Stop auto-allow and lock' }).click()
  await unlock(win)
  await expect(win.getByText('Auto-allow is paused on box.').first()).toBeVisible()
  const waiting = exec('f1', 'echo paused-asks')
  waiting.catch(() => {}) // denied below; avoids a transient unhandled-rejection warning
  await expect(win.locator('.approval')).toContainText('echo paused-asks')
  await win.locator('.approval input').press('Enter')
  await expect(waiting).rejects.toThrow(/Denied/)

  await win.waitForTimeout(600)
  await win.getByRole('button', { name: 'Resume' }).first().click()
  // Resume's setAutoAllow round trip must land before exec reaches the hub,
  // or the grant isn't armed yet and the request waits the full 30 s for a
  // click that never comes.
  await expect(win.locator('.hostcard .chip.auto')).toHaveText('Auto ∞')
  const out = await exec('f2', 'echo resumed')
  expect(JSON.stringify(out)).toContain('echo resumed')
  await win.getByRole('button', { name: 'Stop auto-allow box', exact: true }).click()
  // Let the off round trip land before the next test.
  await expect(win.locator('.hostcard .chip.auto')).toHaveCount(0)
})

test('opting a granted host into sudo-exec runs sudoExec without a click', async () => {
  const win = await l.app.firstWindow()
  await chooseAutoAllow(win, '15m', { sudo: true })
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await expect(d).toContainText('sudo-exec will run without asking: the AI has full root on this host')
  await win.waitForTimeout(600)
  await d.getByRole('button', { name: 'Enable' }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveText(/Auto 1[45]m/)

  const out = await sudoExec('s1', 'echo sudo-auto')
  expect(JSON.stringify(out)).toContain('echo sudo-auto')

  await win.getByRole('button', { name: 'Stop auto-allow box', exact: true }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveCount(0)
})
