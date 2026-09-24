import { test, expect } from '@playwright/test'
import { doorCall } from './doorClient'
import { launch, openBox, unlock, type Launched } from './launch'

// Checklist item 5 (idle auto-lock), with the hub's idle period cut to 3 s.
test.skip(process.platform === 'win32', 'door client uses a Unix socket')

let l: Launched
test.beforeAll(async () => { l = await launch({ SSH_MCP_IDLE_LOCK: '3s' }) })
test.afterAll(async () => { await l?.close() })

test('idle auto-lock keeps terminals and waits for pending AI requests', async () => {
  const win = await l.app.firstWindow()
  const rows = win.locator('.xterm-rows')
  const password = win.getByLabel('Master password')
  await unlock(win)
  await openBox(win)
  await win.keyboard.type('echo before')
  await expect(rows).toContainText('echo before')

  // No input: the vault locks on its own and says why.
  await expect(password).toBeVisible({ timeout: 15000 })
  await expect(win.getByText('Locked after inactivity.')).toBeVisible()

  // The tab and its SSH session survive: same shell, no reconnect.
  await unlock(win)
  await expect(win.locator('.tabbar .tab').first().getByRole('button').first()).toHaveText('box')
  await win.locator('.xterm').click()
  await win.keyboard.type(' after')
  await expect(rows).toContainText('echo before after')

  // A pending AI request holds off the idle lock.
  const pending = doorCall(l.socket, 'exec', { requestId: 'i1', client: 'e2e', server: 'box', command: 'echo idle', description: '' })
  const approvals = win.locator('.approval')
  await expect(approvals).toHaveCount(1)
  await win.waitForTimeout(6000)
  await expect(password).toBeHidden()
  await expect(approvals).toHaveCount(1)

  // Once it is settled, the idle lock comes back.
  const denied = expect(pending).rejects.toThrow('Denied by user')
  await approvals.getByRole('button', { name: 'Deny', exact: true }).click()
  await denied
  await expect(password).toBeVisible({ timeout: 10000 })
})
