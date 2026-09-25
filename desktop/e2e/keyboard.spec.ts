import { test, expect } from '@playwright/test'
import { doorCall } from './doorClient'
import { launch, unlock, type Launched } from './launch'

// Checklist item 6: Tab, Enter and Space alone never allow an AI request.
test.skip(process.platform === 'win32', 'the launcher and door client are Unix-only')

let l: Launched
test.beforeAll(async () => { l = await launch() })
test.afterAll(async () => { await l?.close() })

// settledWithin reports whether p settles (either way) within ms, without leaking a rejection.
function settledWithin(p: Promise<unknown>, ms: number): Promise<boolean> {
  return Promise.race([p.then(() => true, () => true), new Promise<boolean>((r) => setTimeout(() => r(false), ms))])
}

test('Tab never reaches Allow; the panel\'s only keyboard action is Deny', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  // No terminal tab is open, so no xterm swallows Tab.
  await expect(win.locator('nav.hosts').getByRole('button', { name: 'box', exact: true })).toBeVisible()
  const result = doorCall(l.socket, 'exec', { requestId: 'k1', client: 'e2e', server: 'box', command: 'echo k1', description: '' })
  const panel = win.locator('.approvals')
  await expect(panel.getByRole('button', { name: 'Allow' })).toBeEnabled({ timeout: 2000 })

  // Tab around the whole window (twice its stop count is plenty) and record
  // every stop inside the panel.
  const reason = panel.getByPlaceholder('Reason (optional)')
  await reason.focus()
  const stops = new Set<string>()
  for (let i = 0; i < 30; i++) {
    const s = await win.evaluate(() => {
      const a = document.activeElement as HTMLElement | null
      if (!a?.closest('.approvals')) return null
      return a.tagName === 'INPUT' ? 'reason' : (a.textContent ?? '')
    })
    if (s !== null) stops.add(s)
    await win.keyboard.press('Tab')
  }
  expect([...stops].sort()).toEqual(['Deny', 'reason'])

  // Space and Enter in the reason field: Space types, Enter would deny (covered by
  // smoke.spec.ts), so only Space here; the request stays pending.
  await reason.focus()
  await win.keyboard.press('Space')
  expect(await settledWithin(result, 1000)).toBe(false)

  // Space on Deny denies; nothing on the keyboard allows.
  const denied = expect(result).rejects.toThrow('Denied by user')
  await win.keyboard.press('Tab')
  await expect(panel.getByRole('button', { name: 'Deny', exact: true })).toBeFocused()
  await win.keyboard.press('Space')
  await denied
})
