# Slice 3a live e2e Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn `docs/superpowers/checklists/slice3a-manual.md` into an opt-in Playwright e2e that runs the Files tab against a real host from a copy of the real vault, so the slice 3a exit gate is run by a test, not by hand.

**Architecture:** A new spec `desktop/e2e/live-files.spec.ts` is skipped unless `SSHGATE_LIVE_HOST` names a server in the vault. A new `launchLive()` in `desktop/e2e/launch.ts` copies the real vault into a temp dir and launches the app against the copy; no test server. The human types the master password into the app window; the test only waits for the unlock screen to go away. All remote writes happen inside one scratch folder `~/sshgate-e2e-<ms>` created by the test through a terminal tab, and removed by it.

**Tech Stack:** Playwright `_electron`, Node `fs`, the existing Files tab UI.

**Spec:** `docs/superpowers/specs/2026-09-26-slice3a-sftp-design.md` (exit gate and §Testing); checklist `docs/superpowers/checklists/slice3a-manual.md`.

## Global Constraints

- The master password never goes through the test: not env, not a file, not test output. The human types it into the app's Unlock screen (same rule as `TestLiveVaultConnect`, which reads it only from /dev/tty).
- The real vault is never written: the app runs against a copy in the temp dir (`SSHGATE_STORE`, else `~/.config/sshgate/servers.json`, copied mode 0600).
- Remote writes and deletes only inside `<home>/sshgate-e2e-<digits>`. Before any `rm`, the path is checked against `/^\/.+\/sshgate-e2e-\d+$/`.
- `npm run e2e` without `SSHGATE_LIVE_HOST` must stay green and must not touch a real host (the spec skips).
- Checklist item 3 (Windows/Linux Upload files and Upload folder) cannot run on macOS: CI's Linux `desktop` job already runs `files.spec.ts` with "Upload folder"; Windows stays manual.
- Checklist item 2 (a real Finder drop) is a human (or computer-use) step: behind `SSHGATE_LIVE_DROP=1`, the test reveals a folder in Finder and waits for it to be dropped.
- Commits end with the session trailer; author "Lãng".

## Checklist coverage

| Item | Where |
|---|---|
| 1 exit gate | Task 1, test `exit gate` |
| 2 Finder drop | Task 2, test `finder drop` (only with `SSHGATE_LIVE_DROP=1`) |
| 3 Windows/Linux | CI Linux (`files.spec.ts`); Windows manual — noted in checklist |
| 4 lock during a large download | Task 2, test `lock during download` |
| 5 port save during a transfer | Task 3, test `server changed` |
| 6 Permission denied | Task 2, test `permission denied` |

---

### Task 1: launchLive, setup, and the exit gate

**Files:**
- Modify: `desktop/e2e/launch.ts` (add `launchLive`, `waitHumanUnlock`)
- Create: `desktop/e2e/live-files.spec.ts`

**Interfaces:**
- Produces: `launchLive(): Promise<LiveLaunched>`, `LiveLaunched { app: ElectronApplication; tmp: string; close(): Promise<void> }`, `waitHumanUnlock(win: Page, why: string): Promise<void>`; in the spec, module-level `host`, `home`, `scratch`, `uid`, `bigMB`, and helpers `shell`, `openTerminal`, `openFiles`, `goTo`, `stubOpen`.

- [ ] **Step 1: add `launchLive` and `waitHumanUnlock` to `launch.ts`**

```ts
import * as os from 'node:os'

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
```

- [ ] **Step 2: write `live-files.spec.ts` with setup and the exit gate**

```ts
import { test, expect, type Page } from '@playwright/test'
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

async function openFiles(): Promise<void> {
  await win.locator('.tabbar .hometab').click()
  await win.getByRole('button', { name: `Files ${host}` }).click()
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
```

- [ ] **Step 3: check it skips without the env var**

Run: `cd desktop && npx playwright test e2e/live-files.spec.ts`
Expected: `1 skipped` (or all skipped), nothing launched.

- [ ] **Step 4: typecheck**

Run: `cd desktop && npx tsc --noEmit -p tsconfig.json` (or `npm run typecheck` if it covers `e2e/`)
Expected: exit 0.

- [ ] **Step 5: commit**

```bash
git add desktop/e2e/launch.ts desktop/e2e/live-files.spec.ts
git commit -m "test(desktop): opt-in live e2e for the slice 3a exit gate"
```

---

### Task 2: permission denied, lock during a download, Finder drop

**Files:**
- Modify: `desktop/e2e/live-files.spec.ts` (append three tests after the exit gate)

**Interfaces:**
- Consumes: everything Task 1 produces.

- [ ] **Step 1: append the tests**

```ts
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
```

Add `import { execFileSync } from 'node:child_process'` at the top.

- [ ] **Step 2: typecheck and skip check** (same commands as Task 1 Steps 3–4). Expected: exit 0; skipped.

- [ ] **Step 3: commit**

```bash
git add desktop/e2e/live-files.spec.ts
git commit -m "test(desktop): live e2e for permission denied, lock during download, Finder drop"
```

---

### Task 3: server changed, cleanup, docs

**Files:**
- Modify: `desktop/e2e/live-files.spec.ts` (append the last test)
- Modify: `docs/superpowers/checklists/slice3a-manual.md`
- Modify: `CLAUDE.md` (Commands block)

A port change drops the pin and the password in the vault copy, so the host cannot be reached after it. The scratch folder is therefore removed from the terminal **while** the download runs: the open file handle keeps `big` readable. The port is saved after that.

- [ ] **Step 1: append the test**

```ts
// Item 5: saving the port while a transfer runs warns first, then ends it
// with "server changed". Only the vault copy changes.
test('server changed', async () => {
  await openFiles()
  await goTo(remote())
  const dl = path.join(l.tmp, 'big-dl2')
  fs.mkdirSync(dl)
  await grid().locator('[data-name="big"]').click()
  await stubOpen([dl])
  await win.getByRole('button', { name: 'Download' }).click()
  const row = transfers().locator('li', { hasText: 'Download big' }).last()
  await expect(row.locator('progress')).toBeVisible()

  // Cleanup now: after the port change the host is unreachable from the copy.
  expect(remote()).toMatch(/^\/.+\/sshgate-e2e-\d+$/)
  await openTerminal()
  await shell(`chmod 700 '${remote()}/locked'; rm -rf -- '${remote()}' && echo "SG""GONE"`, 'SGGONE')
  cleaned = true

  await win.locator('.tabbar .hometab').click()
  await win.getByRole('button', { name: `Edit ${host}` }).click()
  const port = win.getByLabel('Port')
  await port.fill(String(Number(await port.inputValue()) + 1))
  await expect(win.getByText(/cancel 1 transfer/)).toBeVisible()
  await win.getByRole('button', { name: 'Save' }).click()
  await openFiles()
  await expect(row).toContainText('Cancelled: server changed')
})
```

- [ ] **Step 2: update the checklist**

Replace the checklist body with:

```markdown
# Slice 3a manual checklist

Items 1, 2, 4, 5 and 6 run as an opt-in e2e against a real host, from a copy of your vault (the real one is never written). You type the master password into the app when asked (twice):

    cd desktop && npm run build && SSHGATE_LIVE_HOST=buildpc [SSHGATE_LIVE_DROP=1] [SSHGATE_LIVE_BIG_MB=1024] npx playwright test e2e/live-files.spec.ts

Remote writes stay inside `~/sshgate-e2e-<ms>`, which the test creates and removes (if a run dies midway it prints the folder left behind).

1. Exit gate, on a real host (e.g. buildpc): open Files, browse to a folder, upload a folder, upload it again and choose Skip existing, download it back, rename it, delete it (type `delete`). — `exit gate`
2. Drag a folder from Finder onto a Files tab: it uploads into the folder shown. — `finder drop`, with `SSHGATE_LIVE_DROP=1`: the test reveals the folder in Finder, you drag it.
3. Windows or Linux: Upload files and Upload folder both work. — Linux: CI runs `files.spec.ts` ("Upload folder"). Windows: by hand.
4. Lock the vault during a large download: it keeps running; Cancel still works; a new transfer asks for unlock. — `lock during download` (the part file grows while locked; Cancel after unlock).
5. Save a host's port while a transfer runs: the transfer ends with "server changed"; the editor warned about it first. — `server changed`
6. A folder you cannot read shows "Permission denied". — `permission denied` (skipped when the remote user is root)
```

- [ ] **Step 3: CLAUDE.md** — in the Commands block, after the `npm run e2e` line, add:

```bash
(cd desktop && npm run build && SSHGATE_LIVE_HOST=<server> npx playwright test e2e/live-files.spec.ts)   # opt-in: slice 3a checklist against a real host from a copy of your vault; you type the master password into the app; remote writes only in ~/sshgate-e2e-<ms>
```

- [ ] **Step 4: typecheck, skip check, full `npm run e2e`** — expected: exit 0; live spec skipped; the rest as before (16 passed, 1 skipped + the live spec's skips).

- [ ] **Step 5: commit**

```bash
git add desktop/e2e/live-files.spec.ts docs/superpowers/checklists/slice3a-manual.md CLAUDE.md
git commit -m "test(desktop): live e2e for server changed; checklist points at it"
```

---

### Task 4: run it against the real host

Not a code task: the controller runs it with the human at the app window.

- [ ] **Step 1:** `cd desktop && npm run build && SSHGATE_LIVE_HOST=buildpc SSHGATE_LIVE_DROP=1 npx playwright test e2e/live-files.spec.ts` in the background; tell the human when each unlock or the drag is due (the test prints `>>>` lines).
- [ ] **Step 2:** if a step fails, debug (superpowers:systematic-debugging); a failing assertion on a real host is either a product bug (fix + test) or a wrong test assumption (fix the test, say which).
- [ ] **Step 3:** record the pass output in the PR #2 body's Evidence section and push.

## Open assumptions for the implementer

- `openFiles()` assumes the host card's Files button focuses the existing Files tab rather than opening a second one; if it opens a second, switch tabs through `.tabbar` instead, so the transfer rows asserted later are the ones started.
- The server-changed save may close the Files tab too (it counts open tabs for the host); if so, assert the transfer's end on what the app shows (or on the hub's `files.done` via the audit file in `l.tmp/store/`) and say which in the report.
- `.xterm-rows:visible` must resolve to one element; if hidden terminal tabs match, scope it to the active tab's panel.
