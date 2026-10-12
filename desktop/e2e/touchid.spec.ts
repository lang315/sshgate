import { test, expect } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { launch, unlock, type Launched } from './launch'

// Unlock with Touch ID (spec 2026-10-12-touchid-unlock-design.md) against the
// fake helper: no finger, the "seal" is base64. The real helper is the live
// check, not an e2e test. --user-data-dir keeps the real touchid.json out.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket; the fake helper is a shell script')
test.describe.configure({ mode: 'serial' })

const fake = path.resolve(__dirname, '..', 'test', 'fixtures', 'fake-touchid.sh')
const modeDir = fs.mkdtempSync('/tmp/smt')
const modeFile = path.join(modeDir, 'mode')
let l: Launched
const touchFile = () => path.join(l.tmp, 'userdata', 'touchid.json')
const lockBtn = () => l.app.firstWindow().then((w) => w.getByRole('button', { name: 'Lock', exact: true }))

test.beforeAll(async () => { l = await launch({ SSHGATE_TOUCHID_HELPER: fake, FAKE_TOUCHID_MODE: modeFile }, { userDataDir: true }) })
test.afterAll(async () => { await l?.close(); fs.rmSync(modeDir, { recursive: true, force: true }) })

test('Settings turns Touch ID on; a focus while locked unlocks with no typing', async () => {
  const win = await l.app.firstWindow()
  expect(await l.app.evaluate(({ app }) => app.getPath('userData'))).toBe(path.join(fs.realpathSync(l.tmp), 'userdata')) // /tmp is /private/tmp on macOS
  await unlock(win)
  await expect(await lockBtn()).toBeVisible()

  await win.getByRole('button', { name: 'Settings' }).click()
  const d = win.getByRole('dialog', { name: 'Settings' })
  await d.getByRole('switch', { name: 'Unlock with Touch ID' }).check()
  const save = d.getByRole('button', { name: 'Save' })
  await expect(save).toBeDisabled()
  await d.getByLabel('Master password').fill('wrong')
  await expect(save).toBeEnabled()
  await save.click()
  await expect(d.locator('.error')).toContainText('wrong master password')
  expect(fs.existsSync(touchFile())).toBe(false)
  await d.getByLabel('Master password').fill('pw')
  await expect(save).toBeEnabled()
  await save.click()
  await expect(d).toBeHidden()
  expect(fs.statSync(touchFile()).mode & 0o777).toBe(0o600)

  // Reopened without a lock in between, Settings shows Touch ID on and has nothing to save.
  await win.getByRole('button', { name: 'Settings' }).click()
  await expect(d.getByRole('switch', { name: 'Unlock with Touch ID' })).toBeChecked()
  await win.waitForTimeout(700)
  await expect(d.getByRole('switch', { name: 'Unlock with Touch ID' })).toBeChecked()
  await expect(save).toBeDisabled()
  await win.keyboard.press('Escape')
  await expect(d).toBeHidden()

  await (await lockBtn()).click()
  await expect(win.getByRole('button', { name: 'Unlock with Touch ID' })).toBeVisible()
  await win.waitForTimeout(500)
  await expect(win.getByLabel('Master password')).toBeVisible() // locking alone never asks
  await win.evaluate(() => window.dispatchEvent(new Event('focus')))
  await expect(await lockBtn()).toBeVisible()
})

test('a changed fingerprint set turns Touch ID off, and the password still unlocks', async () => {
  const win = await l.app.firstWindow()
  fs.writeFileSync(modeFile, 'invalid')
  await (await lockBtn()).click()
  await win.getByRole('button', { name: 'Unlock with Touch ID' }).click()
  await expect(win.locator('.lockcard .error')).toContainText('Touch ID was turned off because your fingerprints changed')
  expect(fs.existsSync(touchFile())).toBe(false)
  await expect(win.getByRole('button', { name: 'Unlock with Touch ID' })).toHaveCount(0)
  await unlock(win)
  await expect(await lockBtn()).toBeVisible()
})

test('a renderer crash closes a Touch ID dialog still up', async () => {
  const win = await l.app.firstWindow()
  fs.rmSync(modeFile, { force: true })
  await win.getByRole('button', { name: 'Settings' }).click()
  const d = win.getByRole('dialog', { name: 'Settings' })
  await d.getByRole('switch', { name: 'Unlock with Touch ID' }).check()
  await d.getByLabel('Master password').fill('pw')
  await expect(d.getByRole('button', { name: 'Save' })).toBeEnabled()
  await d.getByRole('button', { name: 'Save' }).click()
  await expect(d).toBeHidden()

  fs.writeFileSync(modeFile, 'slow')
  await (await lockBtn()).click()
  await win.getByRole('button', { name: 'Unlock with Touch ID' }).click()
  await expect.poll(() => fs.existsSync(`${modeFile}.pid`)).toBe(true)
  const pid = Number(fs.readFileSync(`${modeFile}.pid`, 'utf8'))
  const alive = () => { try { process.kill(pid, 0); return true } catch { return false } }
  expect(alive()).toBe(true)
  await l.app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].webContents.forcefullyCrashRenderer())
  await expect.poll(alive, { timeout: 10_000 }).toBe(false)
})

