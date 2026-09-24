import { test, expect, _electron as electron, type ElectronApplication } from '@playwright/test'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import * as fs from 'node:fs'
import * as path from 'node:path'
import * as readline from 'node:readline'
import { doorCall } from './doorClient'

const repo = path.resolve(__dirname, '..', '..')
const exe = process.platform === 'win32' ? '.exe' : ''
let tmp: string, sshd: ChildProcess, app: ElectronApplication

test.skip(process.platform === 'win32', 'door client uses a Unix socket')

test.beforeAll(async () => {
  tmp = fs.mkdtempSync('/tmp/sme')
  // Binaries go in bin/: the hub's socket directory is <tmp>/ssh-mcp.
  const bin = path.join(tmp, 'bin')
  execFileSync('go', ['build', '-o', path.join(bin, 'ssh-mcp' + exe), './cmd/ssh-mcp'], { cwd: repo, stdio: 'inherit' })
  execFileSync('go', ['build', '-o', path.join(bin, 'sshtestd' + exe), './internal/sshx/sshtest/sshtestd'], { cwd: repo, stdio: 'inherit' })
  const store = path.join(tmp, 'store', 'servers.json')
  sshd = spawn(path.join(bin, 'sshtestd' + exe), [`-write-store=${store}`, '-password=pw'], { stdio: ['pipe', 'pipe', 'inherit'] })
  await new Promise<void>((resolve) => readline.createInterface({ input: sshd.stdout! }).once('line', () => resolve()))
  app = await electron.launch({
    args: ['.'],
    cwd: path.resolve(__dirname, '..'),
    env: { ...process.env, SSH_MCP_BIN: path.join(bin, 'ssh-mcp' + exe), SSH_MCP_STORE: store, SSH_MCP_RUNTIME_DIR: tmp },
  })
})

test.afterAll(async () => {
  await app?.close()
  sshd?.stdin?.end()
  sshd?.kill()
  if (tmp) fs.rmSync(tmp, { recursive: true, force: true })
})

// settledWithin reports whether p settles (either way) within ms, without leaking a rejection.
function settledWithin(p: Promise<unknown>, ms: number): Promise<boolean> {
  return Promise.race([p.then(() => true, () => true), new Promise<boolean>((r) => setTimeout(() => r(false), ms))])
}

test('unlock, open a terminal, approve an AI command', async () => {
  const win = await app.firstWindow()
  const unlock = async () => {
    await win.getByLabel('Master password').fill('pw')
    await win.getByRole('button', { name: 'Unlock' }).click()
  }
  await unlock()
  await win.locator('nav.hosts').getByRole('button', { name: 'box', exact: true }).click()
  await expect(win.locator('.xterm')).toBeVisible()
  await win.locator('.xterm').click()
  await win.keyboard.type('echo smoke-ok')
  await expect(win.locator('.xterm-rows')).toContainText('echo smoke-ok')

  // R15(b): the terminal survives lock and unlock.
  await win.getByRole('button', { name: 'Lock' }).click()
  await expect(win.getByLabel('Master password')).toBeVisible()
  await unlock()
  const tab = win.locator('.tabbar .tab').first().getByRole('button').first()
  await expect(tab).toHaveText('box')
  await expect(win.locator('.xterm')).toBeVisible()
  await win.locator('.xterm').click()
  await win.keyboard.type(' after-lock')
  await expect(win.locator('.xterm-rows')).toContainText('echo smoke-ok after-lock')
  await expect(tab).not.toContainText('exited')

  const socket = path.join(tmp, 'ssh-mcp', 'hub.sock')
  const result = doorCall(socket, 'exec', { requestId: 'r1', client: 'e2e', server: 'box', command: 'echo approved', description: 'smoke' })
  const allow = win.getByRole('button', { name: 'Allow' })
  await expect(allow).toBeVisible()
  await expect(allow).toBeEnabled({ timeout: 2000 })

  // R15(a): Allow has no keyboard activation, even when focused.
  await allow.focus()
  await win.keyboard.press('Space')
  await win.keyboard.press('Enter')
  expect(await settledWithin(result, 1000)).toBe(false)

  await allow.click()
  await expect(result).resolves.toMatchObject({ exitCode: 0, stdout: 'echo approved' })
  await expect(win.getByText('Nothing waiting.')).toBeVisible()

  const denied = doorCall(socket, 'exec', { requestId: 'r2', client: 'e2e', server: 'box', command: 'rm -rf /', description: '' })
  denied.catch(() => {}) // it rejects before the expect below attaches; avoid an unhandled rejection
  await win.getByPlaceholder('Reason (optional)').fill('not today')
  await win.getByPlaceholder('Reason (optional)').press('Enter')
  await expect(denied).rejects.toThrow('Denied by user: not today')
})
