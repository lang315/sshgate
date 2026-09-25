import { test, expect } from '@playwright/test'
import { launch, openBox, unlock, type Launched } from './launch'

// Ctrl+Shift+C/V in a terminal (Windows/Linux; macOS uses the Edit menu's Cmd+C/V).
test.skip(process.platform === 'darwin' || process.platform === 'win32', 'Ctrl+Shift+C/V is the Windows/Linux binding; the launcher is Unix-only')

let l: Launched
test.beforeAll(async () => { l = await launch() })
test.afterAll(async () => { await l?.close() })

test('Ctrl+Shift+C copies the selection and Ctrl+Shift+V pastes', async () => {
  const win = await l.app.firstWindow()
  // Every term.write main relays, decoded, with its user flag.
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
  await win.keyboard.type('clipword')
  await expect(rows).toContainText('clipword')
  await writes()

  const line = rows.locator('div', { hasText: 'clipword' }).first()
  const box = (await line.boundingBox())!
  await win.mouse.move(box.x + 1, box.y + box.height / 2)
  await win.mouse.down()
  await win.mouse.move(box.x + box.width - 1, box.y + box.height / 2, { steps: 5 })
  await win.mouse.up()
  await l.app.evaluate(({ clipboard }) => clipboard.writeText('stale'))
  await win.keyboard.press('Control+Shift+C')
  await expect.poll(() => l.app.evaluate(({ clipboard }) => clipboard.readText())).toContain('clipword')
  expect(await writes()).toEqual([])

  await l.app.evaluate(({ clipboard }) => clipboard.writeText(' pasted-text'))
  await win.keyboard.press('Control+Shift+V')
  await expect(rows).toContainText('clipword pasted-text')
  expect(await writes()).toEqual([{ data: ' pasted-text', user: true }])

  // Without Shift, Ctrl+C/V/D/W still reach the shell as control characters.
  for (const k of ['C', 'V', 'D', 'W']) await win.keyboard.press(`Control+${k.toLowerCase()}`)
  let sent = ''
  await expect.poll(async () => (sent += (await writes()).map((w) => w.data).join(''))).toBe('\x03\x16\x04\x17')
})
