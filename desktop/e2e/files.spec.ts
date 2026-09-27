import { test, expect } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { launch, unlock, type Launched } from './launch'

// Spec 3a §Testing: list, mkdir, upload a folder, conflict (skip), download it back
// with the native conflict dialog, rename, recursive delete with the typed word.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')

let l: Launched
test.beforeAll(async () => { l = await launch({}, { sftp: true }) })
test.afterAll(async () => { await l?.close() })

const stubOpen = (paths: string[]) => l.app.evaluate(({ dialog }, p) => {
  dialog.showOpenDialog = (async () => ({ canceled: false, filePaths: p })) as typeof dialog.showOpenDialog
}, paths)
const stubMessage = (response: number) => l.app.evaluate(({ dialog }, r) => {
  dialog.showMessageBox = (async () => ({ response: r, checkboxChecked: false })) as typeof dialog.showMessageBox
}, response)

test('browse, upload, download, rename, and delete over SFTP', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  await win.locator('.tabbar .hometab').click()
  await win.getByRole('button', { name: 'Files box' }).click()
  const grid = win.getByRole('grid', { name: 'Files on box' })
  await expect(win.getByLabel('Path')).toHaveValue('/home')

  // New folder.
  await win.getByRole('button', { name: 'New folder' }).click()
  await win.getByRole('dialog', { name: 'New folder' }).getByLabel('Name').fill('made')
  await win.getByRole('dialog', { name: 'New folder' }).getByRole('button', { name: 'Create' }).click()
  await expect(grid.locator('[data-name="made"]')).toBeVisible()

  // Upload a folder.
  const local = path.join(l.tmp, 'proj')
  fs.mkdirSync(path.join(local, 'sub'), { recursive: true })
  fs.writeFileSync(path.join(local, 'a.txt'), 'alpha')
  fs.writeFileSync(path.join(local, 'sub', 'b.txt'), 'beta')
  await stubOpen([local])
  const uploadName = process.platform === 'darwin' ? 'Upload' : 'Upload folder'
  await win.getByRole('button', { name: uploadName, exact: true }).click()
  const transfers = win.getByRole('list', { name: 'Transfers' })
  await expect(transfers).toContainText('Uploaded 2')
  await expect(grid.locator('[data-name="proj"]')).toBeVisible()
  expect(fs.readFileSync(path.join(l.sftp, 'home', 'proj', 'sub', 'b.txt'), 'utf8')).toBe('beta')

  // Again: the conflict dialog; Skip existing.
  await stubOpen([local])
  await win.getByRole('button', { name: uploadName, exact: true }).click()
  const conflict = win.getByRole('dialog', { name: 'Files already exist' })
  await expect(conflict).toContainText('2 files already exist')
  await conflict.getByRole('button', { name: 'Skip existing' }).click()
  await expect(transfers).toContainText('skipped 2')

  // Download it back, twice: the second time main's native dialog answers Skip existing.
  const dl = path.join(l.tmp, 'dl')
  fs.mkdirSync(dl)
  await grid.locator('[data-name="proj"]').click()
  await stubOpen([dl])
  await win.getByRole('button', { name: 'Download' }).click()
  await expect(transfers).toContainText('Downloaded 2')
  expect(fs.readFileSync(path.join(dl, 'proj', 'a.txt'), 'utf8')).toBe('alpha')
  await stubMessage(1)
  await stubOpen([dl])
  await win.getByRole('button', { name: 'Download' }).click()
  await expect(transfers).toContainText('Downloaded 0 · skipped 2')

  // Rename.
  await grid.locator('[data-name="proj"]').click()
  await win.getByRole('button', { name: 'Rename' }).click()
  const rename = win.getByRole('dialog', { name: 'Rename' })
  await rename.getByLabel('Name').fill('proj2')
  await rename.getByRole('button', { name: 'Rename' }).click()
  await expect(grid.locator('[data-name="proj2"]')).toBeVisible()

  // Recursive delete needs the typed word.
  await grid.locator('[data-name="proj2"]').click()
  await win.getByRole('button', { name: 'Delete', exact: true }).click()
  const del = win.getByRole('dialog', { name: 'Delete files' })
  await expect(del.getByRole('button', { name: 'Delete' })).toBeDisabled()
  await del.getByLabel('Type delete to confirm').fill('delete')
  await del.getByRole('button', { name: 'Delete' }).click()
  await expect(grid.locator('[data-name="proj2"]')).toHaveCount(0)
  expect(fs.existsSync(path.join(l.sftp, 'home', 'proj2'))).toBe(false)
})

// The New folder/Rename dialog has no focus trap, so a Shift+Tab (or, as
// here, any navigation) out to the Path box while it's open must not let the
// eventual submit act on wherever that navigation landed: the folder is
// captured when the dialog opens, not read again at submit time.
test('New folder creates in the folder it was opened in, not one navigated to meanwhile', async () => {
  const win = await l.app.firstWindow()
  await win.locator('.tabbar .hometab').click()
  await win.locator('.tabbar .tab[data-kind="files"] .tabname').first().click()
  const grid = win.getByRole('grid', { name: 'Files on box' })
  await win.getByLabel('Path').fill('/home')
  await win.getByLabel('Path').press('Enter')
  await expect(win.getByLabel('Path')).toHaveValue('/home')

  fs.mkdirSync(path.join(l.sftp, 'home', 'elsewhere'), { recursive: true })

  await win.getByRole('button', { name: 'New folder' }).click()
  const dialog = win.getByRole('dialog', { name: 'New folder' })
  await dialog.getByLabel('Name').fill('trap')

  // Navigate away without closing the dialog (no click needed: fill()
  // doesn't require the Path box to be unobscured by the dialog's overlay).
  // Wait for the navigation to actually settle (aria-busy false), so the
  // captured-folder fix is checked against shown.current having genuinely
  // moved on, not against a navigation still silently in flight.
  await win.getByLabel('Path').fill('/home/elsewhere')
  await win.getByLabel('Path').press('Enter')
  await expect(win.getByLabel('Path')).toHaveValue('/home/elsewhere')
  await expect(grid).toHaveAttribute('aria-busy', 'false', { timeout: 30_000 })

  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect.poll(() => fs.existsSync(path.join(l.sftp, 'home', 'trap'))).toBe(true)
  expect(fs.existsSync(path.join(l.sftp, 'home', 'elsewhere', 'trap'))).toBe(false)
  // The auto-relist after creating stays put too: acting elsewhere doesn't
  // pull the view back to the folder the dialog opened in, or show 'trap'
  // (created in /home) in the /home/elsewhere listing shown now.
  await expect(win.getByLabel('Path')).toHaveValue('/home/elsewhere')
  await expect(grid.locator('[data-name="trap"]')).toHaveCount(0)
})
