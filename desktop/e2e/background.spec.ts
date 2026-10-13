import { test, expect } from '@playwright/test'
import { doorCall } from './doorClient'
import { launch, unlock, type Launched } from './launch'

// macOS: closing the window hides it and the Dock icon; the app, the hub and
// the unlocked vault keep running behind the menu bar icon until Quit.
test.skip(process.platform !== 'darwin', 'macOS only: elsewhere closing the window quits the app')

let l: Launched
test.beforeAll(async () => { l = await launch() })
test.afterAll(async () => { await l?.close() })

const state = () => l.app.evaluate(({ app, BrowserWindow }) => {
  const w = BrowserWindow.getAllWindows()
  return { windows: w.length, visible: w[0]?.isVisible(), focused: w[0]?.isFocused(), dock: app.dock!.isVisible() }
})

test('closing the window keeps the hub serving; reopening the app shows the window again', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  await expect(win.getByRole('button', { name: 'Lock', exact: true })).toBeVisible()

  await l.app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].close())
  await expect.poll(state).toEqual({ windows: 1, visible: false, focused: false, dock: false })
  expect(await doorCall(l.socket, 'listServers', {})).toEqual([expect.objectContaining({ name: 'box', locked: false })])

  // What Finder/Spotlight sends when the running app is opened again.
  await l.app.evaluate(({ app }) => { app.emit('activate') })
  await expect.poll(state).toEqual({ windows: 1, visible: true, focused: true, dock: true })
  await expect(win.getByRole('button', { name: 'Lock', exact: true })).toBeVisible()
})
