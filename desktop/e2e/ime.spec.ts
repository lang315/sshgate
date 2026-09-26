import { test, expect } from '@playwright/test'
import { launch, openBox, unlock, type Launched } from './launch'

// Checklist item 1, automated part: an IME composition (as Telex produces
// for "chào") reaches the shell once, as the committed text, with user: true.
// Real OS input methods are still checked by hand.
test.skip(process.platform === 'win32', 'the launcher is Unix-only')

let l: Launched
test.beforeAll(async () => { l = await launch() })
test.afterAll(async () => { await l?.close() })

test('a composed Vietnamese word is sent once, committed, as user input', async () => {
  const win = await l.app.firstWindow()
  await l.app.evaluate(({ ipcMain }) => {
    const g = globalThis as unknown as { __writes: { data: string; user: boolean }[] }
    g.__writes = []
    ipcMain.on('hub:notify', (_e, method, params) => {
      const p = params as { data: string; user?: boolean }
      if (method === 'term.write') g.__writes.push({ data: Buffer.from(p.data, 'base64').toString(), user: p.user === true })
    })
  })
  const writes = () => l.app.evaluate(() => (globalThis as unknown as { __writes: { data: string; user: boolean }[] }).__writes.splice(0))
  await unlock(win)
  await openBox(win)
  const rows = win.locator('.xterm-rows')
  await win.keyboard.type('xin ')
  await expect(rows).toContainText('xin')
  await writes()

  // Telex builds the word in steps: each is a composition update, then one commit.
  const cdp = await l.app.context().newCDPSession(win)
  for (const text of ['c', 'ch', 'cha', 'chà', 'chào']) {
    await cdp.send('Input.imeSetComposition', { text, selectionStart: text.length, selectionEnd: text.length })
  }
  await cdp.send('Input.insertText', { text: 'chào' })

  await expect(rows).toContainText('xin chào')
  const sent = await writes()
  expect(sent.map((w) => w.data).join('')).toBe('chào')
  expect(sent.every((w) => w.user)).toBe(true)
})
