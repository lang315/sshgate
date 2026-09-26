import { _electron as electron, expect, type ElectronApplication, type Page } from '@playwright/test'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import * as fs from 'node:fs'
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
