import { test, expect } from '@playwright/test'
import { doorCall } from './doorClient'
import { launch, openBox, unlock, type Launched } from './launch'

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
      return a.tagName === 'INPUT' ? 'reason' : (a.getAttribute('aria-label') ?? a.textContent ?? '').replace('↵', '').trim()
    })
    if (s !== null) stops.add(s)
    await win.keyboard.press('Tab')
  }
  expect([...stops].sort()).toEqual(['Close AI requests', 'Deny', 'reason'])

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

test('an arriving request never takes focus; the shortcut and Esc move between terminal and Reason', async () => {
  const win = await l.app.firstWindow()
  const mod = process.platform === 'darwin' ? 'Meta' : 'Control'
  await openBox(win)
  const rows = win.locator('.xterm-rows')
  await win.keyboard.type('abc')
  const result = doorCall(l.socket, 'exec', { requestId: 'k2', client: 'e2e', server: 'box', command: 'echo k2', description: '' })
  const reason = win.locator('.approvals').getByPlaceholder('Reason (optional)')
  await expect(reason).toBeVisible()
  await win.keyboard.type('def')
  await expect(rows).toContainText('abcdef')
  await expect(reason).toHaveValue('')

  await win.keyboard.press(`${mod}+Shift+A`)
  await expect(reason).toBeFocused()
  await win.keyboard.press('Escape')
  await expect(win.locator('.xterm-helper-textarea')).toBeFocused()
  await win.keyboard.type('g')
  await expect(rows).toContainText('abcdefg')

  await win.keyboard.press(`${mod}+Shift+A`)
  const denied = expect(result).rejects.toThrow('Denied by user')
  await win.keyboard.press('Enter')
  await denied
})

test('the shortcut opens a collapsed AI column and focuses Reason', async () => {
  const win = await l.app.firstWindow()
  const mod = process.platform === 'darwin' ? 'Meta' : 'Control'
  // The box tab from the previous test is still the active one.
  const result = doorCall(l.socket, 'exec', { requestId: 'k3', client: 'e2e', server: 'box', command: 'echo k3', description: '' })
  const reason = win.locator('.approvals').getByPlaceholder('Reason (optional)')
  await expect(reason).toBeVisible()
  await win.getByRole('button', { name: 'Close AI requests' }).click()
  await expect(win.locator('.approvals')).toHaveCount(0)
  await win.locator('.xterm:visible').click()

  await win.keyboard.press(`${mod}+Shift+A`)
  await expect(reason).toBeFocused()
  const denied = expect(result).rejects.toThrow('Denied by user')
  await win.keyboard.press('Enter')
  await denied
})

test('arrow keys move the Auth choice in the host editor', async () => {
  const win = await l.app.firstWindow()
  await win.locator('.tabbar .hometab').click()
  await win.locator('nav.hosts').getByRole('button', { name: 'New host' }).click()
  const editor = win.getByRole('dialog', { name: 'Host editor' })
  await editor.getByRole('button', { name: '+ Key or agent' }).click()
  const radio = (name: string) => editor.getByRole('radio', { name })
  await radio('password').focus()
  await win.keyboard.press('ArrowRight')
  await expect(radio('key')).toBeFocused()
  await expect(radio('key')).toHaveAttribute('aria-checked', 'true')
  await win.keyboard.press('ArrowLeft')
  await win.keyboard.press('ArrowLeft')
  await expect(radio('agent')).toBeFocused()
  await expect(radio('agent')).toHaveAttribute('aria-checked', 'true')
  await win.keyboard.press('Escape')
  await expect(editor).toHaveCount(0)
})
