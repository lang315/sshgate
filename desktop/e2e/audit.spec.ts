import { test, expect, type Page } from '@playwright/test'
import { launch, unlock, type Launched } from './launch'
import { doorCall } from './doorClient'

// Spec 2026-09-30-slice4b-audit-viewer-design.md.
test.skip(process.platform === 'win32', 'door client uses a Unix socket')

let l: Launched
test.beforeAll(async () => { l = await launch() })
test.afterAll(async () => { await l?.close() })

const exec = (id: string, command: string) =>
  doorCall(l.socket, 'exec', { requestId: id, client: 'e2e', server: 'box', command, description: 'audit e2e' })

// An AI request the human denies from the AI column.
async function denied(win: Page, id: string, command: string) {
  const p = exec(id, command)
  p.catch(() => {}) // denied below; avoids a transient unhandled-rejection warning
  await expect(win.locator('.approval')).toContainText(command)
  await win.locator('.approval input').press('Enter') // deny
  await expect(p).rejects.toThrow(/Denied/)
}

// Copied from autoallow.spec.ts: opens the Host editor on box, expands AI
// access, picks a duration, and Saves. The caller clicks Enable.
async function chooseAutoAllow(win: Page, mode: string) {
  await win.locator('.tabbar .hometab').click()
  await win.locator('nav.hosts').getByRole('button', { name: 'Edit box' }).click()
  const editor = win.getByRole('dialog', { name: 'Host editor' })
  await editor.locator('summary', { hasText: 'AI access' }).click()
  await editor.getByLabel('Auto-allow', { exact: true }).selectOption(mode)
  await editor.getByRole('button', { name: 'Save' }).click()
}

test('the Audit tab lists, filters, expands, updates live, and reloads after a lock', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  await denied(win, 'd1', 'echo audit-denied')

  // Auto-allow box (the editor's Save also writes a save record), run one exec, stop.
  await chooseAutoAllow(win, '15m')
  const d = win.getByRole('dialog', { name: 'Auto-allow AI commands on box' })
  await win.waitForTimeout(600)
  await d.getByRole('button', { name: 'Enable' }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveText(/Auto 1[45]m/)
  expect(JSON.stringify(await exec('a1', 'echo audit-auto'))).toContain('echo audit-auto')
  await win.getByRole('button', { name: 'Stop auto-allow box', exact: true }).click()
  await expect(win.locator('.hostcard .chip.auto')).toHaveCount(0)

  // Everything is listed, newest first.
  await win.locator('.tabbar .audittab').click()
  const region = win.getByRole('region', { name: 'Audit log' })
  const rows = region.locator('tr.auditrow')
  await expect(rows.filter({ hasText: 'echo audit-denied' })).toHaveCount(1)
  await expect(rows.filter({ hasText: 'echo audit-auto' })).toHaveCount(1)
  await expect(rows.filter({ hasText: 'autoAllowOn 15m' })).toHaveCount(1)
  await expect(region.locator('tr.auditrow td.badges', { hasText: /^save$/ })).toHaveCount(1)
  const seqs = await rows.evaluateAll((els) => els.map((e) => Number(e.getAttribute('data-seq'))))
  expect(seqs).toEqual([...seqs].sort((a, b) => b - a))
  const texts = await rows.allTextContents()
  expect(texts.findIndex((t) => t.includes('echo audit-auto'))).toBeLessThan(texts.findIndex((t) => t.includes('echo audit-denied')))
  await expect(region.locator('footer')).toContainText('audit.jsonl')

  // Auto shows only the auto-allowed run.
  const autoChip = region.getByRole('button', { name: 'Auto', exact: true })
  await autoChip.click()
  await expect(rows).toHaveCount(1)
  await expect(rows.first()).toContainText('echo audit-auto')
  await autoChip.click()
  await expect(rows.filter({ hasText: 'echo audit-denied' })).toHaveCount(1)

  // Expanding a row shows the full command and the AI's description.
  await rows.filter({ hasText: 'echo audit-denied' }).click()
  const detail = region.locator('tr.auditdetail')
  await expect(detail).toContainText('echo audit-denied')
  await expect(detail).toContainText("AI's description · unverified")
  await expect(detail).toContainText('audit e2e')

  // With the tab open, a new record appears without Refresh.
  await denied(win, 'l1', 'echo audit-live')
  await expect(rows.first()).toContainText('echo audit-live')

  // Lock drops the records; unlocking reads them again.
  await win.getByRole('button', { name: 'Lock' }).click()
  await expect(win.getByLabel('Master password')).toBeVisible()
  await expect(win.locator('tr.auditrow')).toHaveCount(0)
  await unlock(win)
  await expect(rows.first()).toContainText('echo audit-live')
  await expect(rows.filter({ hasText: 'echo audit-denied' })).toHaveCount(1)
})
