import { _electron as electron, expect, type ElectronApplication, type Page } from '@playwright/test'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import * as fs from 'node:fs'
import * as os from 'node:os'
import * as path from 'node:path'
import * as readline from 'node:readline'

const repo = path.resolve(__dirname, '..', '..')

export interface Launched { app: ElectronApplication; tmp: string; socket: string; port: number; sftp: string; close(): Promise<void> }

// launch builds sshgate and sshtestd into <tmp>/bin, starts sshtestd, and
// launches the app against <tmp>/store/servers.json. By default sshtestd
// writes a ready vault there (password "pw", server "box"), as smoke.spec.ts
// does; emptyStore leaves no store, so the app opens at Create vault. port is
// sshtestd's. extraEnv is added to the app's environment. sshConfig, given
// sshtestd's port, returns an SSH config written to <tmp>/ssh_config for the
// hub (SSHGATE_SSH_CONFIG); sshtestd then writes its host key to
// <tmp>/known_hosts. sftp starts sshtestd with -sftp-root=<tmp>/sftp; sftp
// is that root on disk (the server's home is <sftp>/home).
export async function launch(extraEnv: Record<string, string> = {},
  opts: { emptyStore?: boolean; sshConfig?: (port: number, tmp: string) => string; sftp?: boolean } = {}): Promise<Launched> {
  const tmp = fs.mkdtempSync('/tmp/sme')
  // Binaries go in bin/: the hub's socket directory is <tmp>/sshgate.
  const bin = path.join(tmp, 'bin')
  execFileSync('go', ['build', '-o', path.join(bin, 'sshgate'), './cmd/sshgate'], { cwd: repo, stdio: 'inherit' })
  execFileSync('go', ['build', '-o', path.join(bin, 'sshtestd'), './internal/sshx/sshtest/sshtestd'], { cwd: repo, stdio: 'inherit' })
  const store = path.join(tmp, 'store', 'servers.json')
  const sftp = path.join(tmp, 'sftp')
  const args = opts.emptyStore ? [] : [`-write-store=${store}`, '-password=pw']
  if (opts.sshConfig) args.push(`-write-known-hosts=${path.join(tmp, 'known_hosts')}`)
  if (opts.sftp) args.push(`-sftp-root=${sftp}`)
  const sshd: ChildProcess = spawn(path.join(bin, 'sshtestd'), args, { stdio: ['pipe', 'pipe', 'inherit'] })
  // sshtestd's first line is PORT=<port>.
  const port = await new Promise<number>((resolve) =>
    readline.createInterface({ input: sshd.stdout! }).once('line', (l) => resolve(Number(l.replace('PORT=', '')))))
  const env = { ...extraEnv }
  if (opts.sshConfig) {
    const cfg = path.join(tmp, 'ssh_config')
    fs.writeFileSync(cfg, opts.sshConfig(port, tmp), { mode: 0o600 })
    env.SSHGATE_SSH_CONFIG = cfg
  }
  const app = await electron.launch({
    args: ['.'],
    cwd: path.resolve(__dirname, '..'),
    env: { ...process.env, SSHGATE_BIN: path.join(bin, 'sshgate'), SSHGATE_STORE: store, SSHGATE_RUNTIME_DIR: tmp, ...env },
  })
  return {
    app, tmp, socket: path.join(tmp, 'sshgate', 'hub.sock'), port, sftp,
    async close() {
      await app.close()
      sshd.stdin?.end()
      sshd.kill()
      fs.rmSync(tmp, { recursive: true, force: true })
    },
  }
}

export async function unlock(win: Page): Promise<void> {
  await win.getByLabel('Master password').fill('pw')
  await win.getByRole('button', { name: 'Unlock' }).click()
}

export async function openBox(win: Page): Promise<void> {
  await win.locator('.tabbar .hometab').click()
  await win.locator('nav.hosts').getByRole('button', { name: 'box', exact: true }).click()
  await waitOpen(win)
  await win.locator('.xterm').click()
}

// Keys typed before term.open replies are dropped, so wait for the active tab to open.
export async function waitOpen(win: Page): Promise<void> {
  await expect(win.locator('.tabbar .tab.active')).toHaveAttribute('data-state', 'open')
}

export interface LiveLaunched { app: ElectronApplication; tmp: string; close(): Promise<void> }

// launchLive launches the app against a copy of the real vault
// (SSHGATE_STORE, else ~/.config/sshgate/servers.json), so the real one is
// never written. No test server: the hosts are real.
export async function launchLive(): Promise<LiveLaunched> {
  const tmp = fs.mkdtempSync('/tmp/sml')
  const bin = path.join(tmp, 'bin')
  execFileSync('go', ['build', '-o', path.join(bin, 'sshgate'), './cmd/sshgate'], { cwd: repo, stdio: 'inherit' })
  const real = process.env.SSHGATE_STORE || path.join(os.homedir(), '.config', 'sshgate', 'servers.json')
  const store = path.join(tmp, 'store', 'servers.json')
  fs.mkdirSync(path.dirname(store), { recursive: true, mode: 0o700 })
  fs.copyFileSync(real, store)
  fs.chmodSync(store, 0o600)
  const app = await electron.launch({
    args: ['.'],
    cwd: path.resolve(__dirname, '..'),
    env: { ...process.env, SSHGATE_BIN: path.join(bin, 'sshgate'), SSHGATE_STORE: store, SSHGATE_RUNTIME_DIR: tmp },
  })
  return {
    app, tmp,
    async close() {
      await app.close()
      fs.rmSync(tmp, { recursive: true, force: true })
    },
  }
}

// waitHumanUnlock waits for a person to type the master password into the
// app window: the test never sees it.
export async function waitHumanUnlock(win: Page, why: string): Promise<void> {
  const field = win.getByLabel('Master password')
  await expect(field).toBeVisible({ timeout: 30_000 })
  console.log(`\n>>> ${why}: type the master password in the sshgate window and press Unlock (5 min).\n`)
  await expect(field).toBeHidden({ timeout: 5 * 60_000 })
}
