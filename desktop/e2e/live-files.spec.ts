import { test, expect, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { launchLive, waitHumanUnlock, waitOpen, type LiveLaunched } from './launch'

// Opt-in: the slice 3a checklist (docs/superpowers/checklists/slice3a-manual.md)
// against a real host from a copy of the real vault. SSHGATE_LIVE_HOST names
// the server; a person unlocks the app when asked. Remote writes stay inside
// ~/sshgate-e2e-<ms>, which the test creates and removes.
const host = process.env.SSHGATE_LIVE_HOST ?? ''
const bigMB = Number(process.env.SSHGATE_LIVE_BIG_MB ?? '1024')
test.skip(!host, 'set SSHGATE_LIVE_HOST to a server in your vault')
test.describe.configure({ mode: 'serial' })
test.setTimeout(15 * 60_000)

const scratch = `sshgate-e2e-${Date.now()}`
let l: LiveLaunched
let win: Page
let home = ''
let uid = -1
let cleaned = false

const stubOpen = (paths: string[]) => l.app.evaluate(({ dialog }, p) => {
  dialog.showOpenDialog = (async () => ({ canceled: false, filePaths: p })) as typeof dialog.showOpenDialog
}, paths)
const grid = () => win.getByRole('grid', { name: `Files on ${host}` })
const transfers = () => win.getByRole('list', { name: 'Transfers' })
const remote = () => `${home}/${scratch}`

async function openTerminal(): Promise<void> {
  await win.locator('.tabbar .hometab').click()
  await win.locator('nav.hosts').getByRole('button', { name: host, exact: true }).click()
  await waitOpen(win)
  await win.locator('.xterm:visible').click()
}

// shell types cmd into the visible terminal and waits for marker in its
// output. Callers split the marker in the command ("SG""OK") so the echoed
// command line never matches it.
async function shell(cmd: string, marker: string): Promise<string> {
  await win.keyboard.type(cmd)
  await win.keyboard.press('Enter')
  const rows = win.locator('.xterm-rows:visible')
  await expect(rows).toContainText(marker, { timeout: 5 * 60_000 })
  return (await rows.textContent()) ?? ''
}

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
    await win.getByRole('button', { name: `Files ${host}` }).click()
  }
  await expect(grid()).toBeVisible()
}

async function goTo(dir: string): Promise<void> {
  await win.getByLabel('Path').fill(dir)
  await win.getByLabel('Path').press('Enter')
  await expect(win.getByLabel('Path')).toHaveValue(dir)
}

test.beforeAll(async () => {
  l = await launchLive()
  win = await l.app.firstWindow()
  await waitHumanUnlock(win, 'Start')
  await openTerminal()
  const out = await shell(
    `d="$HOME/${scratch}"; mkdir "$d" && mkdir "$d/locked" && chmod 000 "$d/locked" && ` +
    `dd if=/dev/zero of="$d/big" bs=1048576 count=${bigMB} 2>/dev/null && echo "SG""OK home=$HOME uid=$(id -u)"`,
    'SGOK home=')
  const m = /SGOK home=(\S+) uid=(\d+)/.exec(out)
  expect(m, 'setup output').not.toBeNull()
  home = m![1]
  uid = Number(m![2])
  expect(remote()).toMatch(/^\/.+\/sshgate-e2e-\d+$/)
})

test.afterAll(async () => {
  if (!cleaned) console.log(`\n>>> Remote scratch folder left on ${host}: ${remote()}\n`)
  await l?.close()
})

test('exit gate: browse, upload, skip existing, download, rename, delete', async () => {
  await openFiles()
  await goTo(remote())

  const local = path.join(l.tmp, 'proj')
  fs.mkdirSync(path.join(local, 'sub'), { recursive: true })
  fs.writeFileSync(path.join(local, 'a.txt'), 'alpha')
  fs.writeFileSync(path.join(local, 'sub', 'b.txt'), 'beta')
  const uploadName = process.platform === 'darwin' ? 'Upload' : 'Upload folder'
  await stubOpen([local])
  await win.getByRole('button', { name: uploadName, exact: true }).click()
  await expect(transfers()).toContainText('Uploaded 2')
  await expect(grid().locator('[data-name="proj"]')).toBeVisible()

  await stubOpen([local])
  await win.getByRole('button', { name: uploadName, exact: true }).click()
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

test('permission denied', async () => {
  test.skip(uid === 0, 'root can read any folder')
  await openFiles()
  await goTo(`${remote()}/locked`)
  await expect(win.locator('.files-note.error')).toContainText(/permission denied/i)
  await goTo(remote())
})

// Item 4: a download keeps running through a lock, and Cancel works after.
// While locked the Files tab is behind the Unlock screen, so a new transfer
// cannot start until unlock; the hub-side cancel-while-locked is
// TestJobCancelWhileRunningAndWhileLocked.
test('lock during download', async () => {
  await openFiles()
  await goTo(remote())
  const dl = path.join(l.tmp, 'big-dl')
  fs.mkdirSync(dl)
  const part = () => fs.readdirSync(dl).find((n) => n.endsWith('.sshgate-part'))
  const partSize = () => { const p = part(); return p ? fs.statSync(path.join(dl, p)).size : -1 }

  await grid().locator('[data-name="big"]').click()
  await stubOpen([dl])
  await win.getByRole('button', { name: 'Download' }).click()
  const row = transfers().locator('li', { hasText: 'Download big' })
  await expect(row.locator('progress')).toBeVisible()
  await expect.poll(partSize).toBeGreaterThan(0)

  await win.getByRole('button', { name: 'Lock' }).click()
  await expect(win.getByLabel('Master password')).toBeVisible()
  const before = partSize()
  await expect.poll(partSize, { message: `download finished or stalled; raise SSHGATE_LIVE_BIG_MB (${bigMB})`, timeout: 15_000 })
    .toBeGreaterThan(before)

  await waitHumanUnlock(win, 'Lock test')
  await expect(row, `download finished during the lock; raise SSHGATE_LIVE_BIG_MB (${bigMB})`).not.toContainText('Downloaded')
  await row.getByRole('button', { name: 'Cancel' }).click()
  await expect(row).toContainText('Cancelled')
  expect(part()).toBeUndefined()
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
