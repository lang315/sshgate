import { _electron as electron, type ElectronApplication, type Page } from '@playwright/test'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import * as fs from 'node:fs'
import * as path from 'node:path'
import * as readline from 'node:readline'

const repo = path.resolve(__dirname, '..', '..')

export interface Launched { app: ElectronApplication; tmp: string; socket: string; close(): Promise<void> }

// launch builds ssh-mcp and sshtestd into <tmp>/bin, starts sshtestd with a
// ready vault (password "pw", server "box"), and launches the app against it,
// as smoke.spec.ts does. extraEnv is added to the app's environment.
export async function launch(extraEnv: Record<string, string> = {}): Promise<Launched> {
  const tmp = fs.mkdtempSync('/tmp/sme')
  // Binaries go in bin/: the hub's socket directory is <tmp>/ssh-mcp.
  const bin = path.join(tmp, 'bin')
  execFileSync('go', ['build', '-o', path.join(bin, 'ssh-mcp'), './cmd/ssh-mcp'], { cwd: repo, stdio: 'inherit' })
  execFileSync('go', ['build', '-o', path.join(bin, 'sshtestd'), './internal/sshx/sshtest/sshtestd'], { cwd: repo, stdio: 'inherit' })
  const store = path.join(tmp, 'store', 'servers.json')
  const sshd: ChildProcess = spawn(path.join(bin, 'sshtestd'), [`-write-store=${store}`, '-password=pw'], { stdio: ['pipe', 'pipe', 'inherit'] })
  await new Promise<void>((resolve) => readline.createInterface({ input: sshd.stdout! }).once('line', () => resolve()))
  const app = await electron.launch({
    args: ['.'],
    cwd: path.resolve(__dirname, '..'),
    env: { ...process.env, SSH_MCP_BIN: path.join(bin, 'ssh-mcp'), SSH_MCP_STORE: store, SSH_MCP_RUNTIME_DIR: tmp, ...extraEnv },
  })
  return {
    app, tmp, socket: path.join(tmp, 'ssh-mcp', 'hub.sock'),
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
  await win.locator('nav.hosts').getByRole('button', { name: 'box', exact: true }).click()
  await win.locator('.xterm').click()
}
