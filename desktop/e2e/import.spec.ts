import { test, expect } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import * as path from 'node:path'
import { launch, unlock, waitOpen, type Launched } from './launch'

// Spec 2b-1 §Testing: import pins from known_hosts, so the first connect has no Trust prompt.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')

let l: Launched
test.beforeAll(async () => {
  l = await launch({}, {
    sshConfig: (port, tmp) => {
      const key = path.join(tmp, 'id_ed25519')
      execFileSync('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', key])
      // Host * sets an IdentityFile and a known_hosts file, so nothing reads the real ~/.ssh.
      return [
        'Host work', '  HostName 127.0.0.1', `  Port ${port}`, '  User test', `  IdentityFile ${key}`,
        'Host viaproxy', '  HostName 127.0.0.1', '  ProxyCommand nc %h %p',
        'Host *', `  IdentityFile ${path.join(tmp, 'missing')}`, `  UserKnownHostsFile ${path.join(tmp, 'known_hosts')}`, '',
      ].join('\n')
    },
  })
})
test.afterAll(async () => { await l?.close() })

test('import a host from the SSH config, pinned from known_hosts', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  await win.locator('.tabbar .hometab').click()
  const hosts = win.locator('nav.hosts')
  await hosts.getByRole('button', { name: 'Import from SSH config' }).click()

  const sheet = win.getByRole('dialog', { name: 'Import from SSH config' })
  await expect(sheet).toContainText('StrictHostKeyChecking accept-new')
  await expect(sheet.getByRole('checkbox', { name: 'Import work' })).toBeChecked()
  await expect(sheet).toContainText('SHA256:')
  await expect(sheet).toContainText('needs ProxyCommand')
  await sheet.getByRole('button', { name: 'Import 1 host' }).click()
  await expect(sheet).toBeHidden()

  // Pinned at import: no "New key" chip, and connecting asks nothing.
  const card = hosts.locator('.hostcard', { hasText: 'work' })
  await expect(card).toBeVisible()
  await expect(card).not.toContainText('New key')
  // Click the tile: only the card's top row opens it; the footer holds the
  // chips and action icons.
  await hosts.getByRole('button', { name: 'work', exact: true }).locator('.tile').click()
  await waitOpen(win)
  await expect(win.getByRole('dialog', { name: 'Unknown host key' })).toHaveCount(0)
  await win.locator('.xterm').click()
  await win.keyboard.type('echo import-ok')
  await expect(win.locator('.xterm-rows')).toContainText('echo import-ok')
})
