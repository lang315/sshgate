import { test, expect, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { launchLive, waitHumanUnlock, type LiveLaunched } from './launch'

// Opt-in: the slice 3a checklist (docs/superpowers/checklists/slice3a-manual.md)
// against a real host from a copy of the real vault. SSHGATE_LIVE_HOST names
// the server; a person unlocks the app when asked. Remote writes stay inside
// <home>/sshgate-e2e-<ms>, which the test creates and removes. Nothing is
// typed into a terminal: every remote step goes through the Files tab (SFTP),
// so this works against OpenSSH on Windows hosts too.
const host = process.env.SSHGATE_LIVE_HOST ?? ''
const bigMB = Number(process.env.SSHGATE_LIVE_BIG_MB ?? '1024')
test.skip(!host, 'set SSHGATE_LIVE_HOST to a server in your vault')
test.describe.configure({ mode: 'serial' })
test.setTimeout(30 * 60_000)

const scratch = `sshgate-e2e-${Date.now()}`
let l: LiveLaunched
let win: Page
let home = ''
let created = false
let portChanged = false
let cleaned = false

const stubOpen = (paths: string[]) => l.app.evaluate(({ dialog }, p) => {
  dialog.showOpenDialog = (async () => ({ canceled: false, filePaths: p })) as typeof dialog.showOpenDialog
}, paths)
const grid = () => win.getByRole('grid', { name: `Files on ${host}` })
const transfers = () => win.getByRole('list', { name: 'Transfers' })
const remote = () => `${home}/${scratch}`

// openFiles focuses the host's Files tab. The tab set has no dedup (TabSet.open
// always pushes a new tab), so the home tab's Files button would open a second
// tab on every call; switch through the tabbar to the one already open instead,
// so the transfer rows asserted later are the ones this spec started.
async function openFiles(): Promise<void> {
  await win.locator('.tabbar .hometab').click()
  const existing = win.locator('.tabbar .tab[data-kind="files"]')
  if (await existing.count() > 0) {
    await existing.first().locator('.tabname').click()
  } else {
    await win.getByRole('button', { name: `Files ${host}`, exact: true }).click()
  }
  await expect(grid()).toBeVisible()
}

async function goTo(dir: string): Promise<void> {
  await win.getByLabel('Path').fill(dir)
  await win.getByLabel('Path').press('Enter')
  await expect(win.getByLabel('Path')).toHaveValue(dir)
}

const uploadFileName = process.platform === 'darwin' ? 'Upload' : 'Upload files'
const uploadFolderName = process.platform === 'darwin' ? 'Upload' : 'Upload folder'

test.beforeAll(async () => {
  l = await launchLive()
  win = await l.app.firstWindow()
  await waitHumanUnlock(win, 'Start')

  // A remembered last folder (desktop/src/renderer/files.ts's lastFolder,
  // localStorage key "sshgate.files.last.<server>") would open Files there
  // instead of home; clear it before the tab is ever opened.
  await win.evaluate((k) => localStorage.removeItem(k), `sshgate.files.last.${host}`)
  await openFiles()
  // The grid renders as soon as the tab mounts, before files.list replies:
  // wait for the listing to settle so the Path input holds the real home,
  // not the '' load() starts from. aria-busy holds "false" on the very
  // first render too (loading starts false), so also wait for the Path
  // itself to become non-empty.
  await expect(grid()).toHaveAttribute('aria-busy', 'false', { timeout: 30_000 })
  await expect(win.getByLabel('Path')).not.toHaveValue('', { timeout: 30_000 })
  home = await win.getByLabel('Path').inputValue()
  expect(home, 'refusing to run on a home path with spaces or quotes').toMatch(/^\/[^\s'"\\$`]+$/)

  await win.getByRole('button', { name: 'New folder' }).click()
  await win.getByRole('dialog', { name: 'New folder' }).getByLabel('Name').fill(scratch)
  await win.getByRole('dialog', { name: 'New folder' }).getByRole('button', { name: 'Create' }).click()
  await expect(grid().locator(`[data-name="${scratch}"]`)).toBeVisible()
  created = true
  await goTo(remote())

  // A sparse local file of bigMB MiB, uploaded once for every "big"-download test.
  const bigPath = path.join(l.tmp, 'big')
  const fd = fs.openSync(bigPath, 'w')
  fs.ftruncateSync(fd, bigMB * 1024 * 1024)
  fs.closeSync(fd)
  await stubOpen([bigPath])
  await win.getByRole('button', { name: uploadFileName, exact: true }).click()
  // A slow link may take a while: this is the one upload nothing else depends on being fast.
  await expect(transfers()).toContainText('Uploaded 1', { timeout: 20 * 60_000 })
})

test.afterAll(async () => {
  if (created) {
    try {
      if (portChanged) {
        // The port change makes the first app's vault copy unreachable;
        // a fresh copy of the real vault has the original port back.
        await l.close()
        l = await launchLive()
        win = await l.app.firstWindow()
        await waitHumanUnlock(win, 'Cleanup')
      }
      await openFiles()
      await goTo(home)
      expect(scratch, 'refusing to delete anything but the scratch folder by name').toMatch(/^sshgate-e2e-\d+$/)
      await grid().locator(`[data-name="${scratch}"]`).click()
      await win.getByRole('button', { name: 'Delete', exact: true }).click()
      const del = win.getByRole('dialog', { name: 'Delete files' })
      await del.getByLabel('Type delete to confirm').fill('delete')
      await del.getByRole('button', { name: 'Delete' }).click()
      await expect(grid().locator(`[data-name="${scratch}"]`)).toHaveCount(0)
      cleaned = true
    } catch {
      // Fall through: the message below says what is left behind.
    }
  }
  if (created && !cleaned) console.log(`\n>>> Remote scratch folder left on ${host}: ${remote()}\n`)
  // Best-effort: a failed close here (e.g. the first close in the
  // portChanged branch already failed) must not throw out of afterAll
  // after the message above has run.
  await l?.close().catch(() => {})
})

test('exit gate: browse, upload, skip existing, download, rename, delete', async () => {
  await openFiles()
  await goTo(remote())

  const local = path.join(l.tmp, 'proj')
  fs.mkdirSync(path.join(local, 'sub'), { recursive: true })
  fs.writeFileSync(path.join(local, 'a.txt'), 'alpha')
  fs.writeFileSync(path.join(local, 'sub', 'b.txt'), 'beta')
  await stubOpen([local])
  await win.getByRole('button', { name: uploadFolderName, exact: true }).click()
  await expect(transfers()).toContainText('Uploaded 2')
  await expect(grid().locator('[data-name="proj"]')).toBeVisible()

  await stubOpen([local])
  await win.getByRole('button', { name: uploadFolderName, exact: true }).click()
  const conflict = win.getByRole('dialog', { name: 'Files already exist' })
  await expect(conflict).toContainText('2 files already exist')
  await conflict.getByRole('button', { name: 'Skip existing' }).click()
  await expect(transfers()).toContainText('skipped 2')

  const dl = path.join(l.tmp, 'dl')
  fs.mkdirSync(dl)
  await grid().locator('[data-name="proj"]').click()
  await stubOpen([dl])
  await win.getByRole('button', { name: 'Download' }).click()
  await expect(transfers()).toContainText('Downloaded 2')
  expect(fs.readFileSync(path.join(dl, 'proj', 'a.txt'), 'utf8')).toBe('alpha')
  expect(fs.readFileSync(path.join(dl, 'proj', 'sub', 'b.txt'), 'utf8')).toBe('beta')

  await grid().locator('[data-name="proj"]').click()
  await win.getByRole('button', { name: 'Rename' }).click()
  const rename = win.getByRole('dialog', { name: 'Rename' })
  await rename.getByLabel('Name').fill('proj2')
  await rename.getByRole('button', { name: 'Rename' }).click()
  await expect(grid().locator('[data-name="proj2"]')).toBeVisible()

  await grid().locator('[data-name="proj2"]').click()
  await win.getByRole('button', { name: 'Delete', exact: true }).click()
  const del = win.getByRole('dialog', { name: 'Delete files' })
  await expect(del.getByRole('button', { name: 'Delete' })).toBeDisabled()
  await del.getByLabel('Type delete to confirm').fill('delete')
  await del.getByRole('button', { name: 'Delete' }).click()
  await expect(grid().locator('[data-name="proj2"]')).toHaveCount(0)
})

// Item 6. Target: SSHGATE_LIVE_DENIED, else /root — unless home looks like a
// Windows path, where /root means nothing and there is no universal
// unreadable folder, so this is skipped without SSHGATE_LIVE_DENIED.
test('permission denied', async () => {
  const windowsHome = /^\/[A-Za-z]:/.test(home)
  const denied = process.env.SSHGATE_LIVE_DENIED ?? (windowsHome ? undefined : '/root')
  test.skip(denied === undefined, 'set SSHGATE_LIVE_DENIED to a folder you cannot read on this Windows host')
  await openFiles()
  await goTo(denied!)
  await expect(grid()).toHaveAttribute('aria-busy', 'false', { timeout: 30_000 })
  const errored = await win.locator('.files-note.error').isVisible()
  test.skip(!errored, `the remote user can read ${denied}; set SSHGATE_LIVE_DENIED to a folder it cannot`)
  await expect(win.locator('.files-note.error')).toContainText(/permission denied/i)
  await goTo(remote())
})

// Item 4: a download keeps running through a lock, and Cancel works after.
// While locked the Files tab is behind the Unlock screen, so a new transfer
// cannot start until unlock; the hub-side cancel-while-locked is
// TestJobCancelWhileRunningAndWhileLocked. Decoupled from timing: if the
// download already finished during the lock (a fast link), a second one is
// started and cancelled instead of failing the test.
test('lock during download', async () => {
  await openFiles()
  await goTo(remote())
  let dl = path.join(l.tmp, 'big-dl')
  fs.mkdirSync(dl)
  const part = (dir: string) => fs.readdirSync(dir).find((n) => n.endsWith('.sshgate-part'))
  const partSize = (dir: string) => { const p = part(dir); return p ? fs.statSync(path.join(dir, p)).size : -1 }

  await grid().locator('[data-name="big"]').click()
  await stubOpen([dl])
  await win.getByRole('button', { name: 'Download' }).click()
  let row = transfers().locator('li', { hasText: 'Download big' }).last()
  await expect(row.locator('progress')).toBeVisible()
  await expect.poll(() => partSize(dl)).toBeGreaterThan(0)

  await win.getByRole('button', { name: 'Lock' }).click()
  await expect(win.getByLabel('Master password')).toBeVisible()
  const before = partSize(dl)
  await expect.poll(() => partSize(dl), { message: `download finished or stalled; raise SSHGATE_LIVE_BIG_MB (${bigMB})`, timeout: 15_000 })
    .toBeGreaterThan(before)

  await waitHumanUnlock(win, 'Lock test')
  if (!(await row.locator('progress').isVisible())) {
    // Finished during the lock: start a fresh one to cancel instead.
    dl = path.join(l.tmp, 'big-dl2')
    fs.mkdirSync(dl)
    await grid().locator('[data-name="big"]').click()
    await stubOpen([dl])
    await win.getByRole('button', { name: 'Download' }).click()
    row = transfers().locator('li', { hasText: 'Download big' }).last()
    await expect(row.locator('progress')).toBeVisible()
  }
  await row.getByRole('button', { name: 'Cancel' }).click()
  await expect(row).toContainText('Cancelled')
  expect(part(dl)).toBeUndefined()
  expect(fs.existsSync(path.join(dl, 'big'))).toBe(false)
})

// Item 2: Playwright cannot make a real dropped File, so a person (or a
// computer-use agent) drags the folder from Finder.
test('finder drop', async () => {
  test.skip(process.env.SSHGATE_LIVE_DROP !== '1', 'set SSHGATE_LIVE_DROP=1 to drag from Finder')
  await openFiles()
  await goTo(remote())
  const drop = path.join(l.tmp, 'drop-me')
  fs.mkdirSync(drop)
  fs.writeFileSync(path.join(drop, 'x.txt'), 'x')
  execFileSync('open', ['-R', drop])
  console.log(`\n>>> Drag the folder "drop-me" from Finder onto the Files tab (3 min).\n`)
  await expect(grid().locator('[data-name="drop-me"]')).toBeVisible({ timeout: 3 * 60_000 })
  await expect(transfers()).toContainText('Uploaded 1')
})

// Item 5: saving the port while a transfer runs warns first, then ends it
// with "server changed". Only the vault copy changes; no cleanup here — the
// scratch folder is unreachable from this copy until afterAll relaunches
// against a fresh one.
test('server changed', async () => {
  await openFiles()
  await goTo(remote())
  const dl = path.join(l.tmp, 'big-dl3')
  fs.mkdirSync(dl)
  await grid().locator('[data-name="big"]').click()
  await stubOpen([dl])
  await win.getByRole('button', { name: 'Download' }).click()
  const row = transfers().locator('li', { hasText: 'Download big' }).last()
  await expect(row.locator('progress')).toBeVisible()

  await win.locator('.tabbar .hometab').click()
  await win.getByRole('button', { name: `Edit ${host}`, exact: true }).click()
  const port = win.getByLabel('Port')
  await port.fill(String(Number(await port.inputValue()) + 1))
  await expect(win.getByText(/cancel 1 transfer/)).toBeVisible()
  await win.getByRole('button', { name: 'Save' }).click()
  portChanged = true
  await openFiles()
  await expect(row).toContainText('Cancelled: server changed')
})
