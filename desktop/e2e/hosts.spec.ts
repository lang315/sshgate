import { test, expect } from '@playwright/test'
import { launch, type Launched } from './launch'

// Spec 2a §Testing: vault, host CRUD, and the host-key prompt, in order.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')

let l: Launched
test.beforeAll(async () => { l = await launch({}, { emptyStore: true }) })
test.afterAll(async () => { await l?.close() })

test('create a vault, add a host, trust its key, edit the port, forget the key', async () => {
  const win = await l.app.firstWindow()
  const hosts = win.locator('nav.hosts')
  const editor = win.getByRole('dialog', { name: 'Host editor' })
  const prompt = win.getByRole('dialog', { name: 'Unknown host key' })
  const home = () => win.locator('.tabbar .hometab').click()
  const openBox = async () => { await home(); await hosts.getByRole('button', { name: 'box', exact: true }).click() }
  const editBox = async () => { await home(); await hosts.getByRole('button', { name: 'Edit box' }).click() }
  const trust = async () => {
    await expect(prompt).toContainText('SHA256:')
    await expect(prompt).toContainText(`test@127.0.0.1:${l.port}`)
    const button = prompt.getByRole('button', { name: 'Trust' })
    await expect(button).toBeEnabled({ timeout: 2000 })
    await button.click()
    await expect(prompt).toBeHidden()
  }

  // 1. Create a vault on an empty store.
  await win.getByLabel('New master password').fill('password1')
  await win.getByLabel('Confirm master password').fill('password1')
  await win.getByRole('button', { name: 'Create vault' }).click()
  await expect(hosts).toContainText('Vault file:')

  // 2. Add a host pointing at sshtestd.
  await hosts.getByRole('button', { name: 'New host' }).click()
  await editor.getByLabel('Name', { exact: true }).fill('box')
  await editor.getByLabel('Host', { exact: true }).fill('127.0.0.1')
  await editor.getByLabel('Port', { exact: true }).fill(String(l.port))
  await editor.getByLabel('User', { exact: true }).fill('test')
  await editor.getByLabel('Password', { exact: true }).fill('testpass')
  await editor.getByRole('button', { name: 'Save' }).click()
  await expect(editor).toBeHidden()

  // 3. Connect: the prompt shows the fingerprint; Trust gives a shell.
  await openBox()
  await trust()
  await win.locator('.xterm').click()
  await win.keyboard.type('echo hosts-ok')
  await expect(win.locator('.xterm-rows')).toContainText('echo hosts-ok')

  // 4. Edit the port: the editor warns, and saving closes the tab.
  await editBox()
  await editor.getByLabel('Port', { exact: true }).fill(String(l.port + 1))
  await expect(editor).toContainText('Changing host or port forgets the host key and saved passwords unless you re-enter them.')
  await expect(editor).toContainText('Saving will close 1 open tab.')
  await editor.getByRole('button', { name: 'Save' }).click()
  await expect(win.locator('.tabbar .tab').first()).toContainText('exited')
  // Back to the real port: the pin went with the old endpoint, so connecting asks again.
  await editBox()
  await editor.getByLabel('Port', { exact: true }).fill(String(l.port))
  await editor.getByRole('button', { name: 'Save' }).click()
  await expect(editor).toBeHidden()
  await openBox()
  await trust()

  // 5. Forget the key; reconnecting shows the prompt again.
  await editBox()
  await expect(editor).toContainText('SHA256:')
  await editor.getByRole('button', { name: 'Forget host key' }).click()
  await expect(editor).toContainText('Not pinned')
  await editor.getByRole('button', { name: 'Close' }).click()
  await openBox()
  await expect(prompt).toBeVisible()
  await prompt.getByRole('button', { name: 'Cancel' }).click()
  await expect(prompt).toBeHidden()
  await expect(win.locator('.tabbar .tab').last()).toContainText('exited')
})
