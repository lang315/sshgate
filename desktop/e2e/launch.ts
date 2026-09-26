import { _electron as electron, type ElectronApplication, type Page } from '@playwright/test'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import * as fs from 'node:fs'
import * as path from 'node:path'
import * as readline from 'node:readline'

const repo = path.resolve(__dirname, '..', '..')

export interface Launched { app: ElectronApplication; tmp: string; socket: string; port: number; close(): Promise<void> }

// launch builds sshgate and sshtestd into <tmp>/bin, starts sshtestd, and
// launches the app against <tmp>/store/servers.json. By default sshtestd
// writes a ready vault there (password "pw", server "box"), as smoke.spec.ts
// does; emptyStore leaves no store, so the app opens at Create vault. port is
// sshtestd's. extraEnv is added to the app's environment.
export async function launch(extraEnv: Record<string, string> = {}, opts: { emptyStore?: boolean } = {}): Promise<Launched> {
  const tmp = fs.mkdtempSync('/tmp/sme')
  // Binaries go in bin/: the hub's socket directory is <tmp>/sshgate.
  const bin = path.join(tmp, 'bin')
  execFileSync('go', ['build', '-o', path.join(bin, 'sshgate'), './cmd/sshgate'], { cwd: repo, stdio: 'inherit' })
  execFileSync('go', ['build', '-o', path.join(bin, 'sshtestd'), './internal/sshx/sshtest/sshtestd'], { cwd: repo, stdio: 'inherit' })
  const store = path.join(tmp, 'store', 'servers.json')
  const args = opts.emptyStore ? [] : [`-write-store=${store}`, '-password=pw']
  const sshd: ChildProcess = spawn(path.join(bin, 'sshtestd'), args, { stdio: ['pipe', 'pipe', 'inherit'] })
  // sshtestd's first line is PORT=<port>.
  const port = await new Promise<number>((resolve) =>
    readline.createInterface({ input: sshd.stdout! }).once('line', (l) => resolve(Number(l.replace('PORT=', '')))))
  const app = await electron.launch({
    args: ['.'],
    cwd: path.resolve(__dirname, '..'),
    env: { ...process.env, SSHGATE_BIN: path.join(bin, 'sshgate'), SSHGATE_STORE: store, SSHGATE_RUNTIME_DIR: tmp, ...extraEnv },
  })
  return {
    app, tmp, socket: path.join(tmp, 'sshgate', 'hub.sock'), port,
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
  await win.locator('.xterm').click()
}
