import { test, expect, type Page } from '@playwright/test'
import { launch, unlock, type Launched } from './launch'

// Redesign behaviour (spec 2026-09-25-desktop-ui-redesign-design.md). The tests
// share one app and run in order; the first one unlocks it.
test.skip(process.platform === 'win32', 'the launcher and door client are Unix-only')

let l: Launched
let win: Page
test.beforeAll(async () => {
  l = await launch()
  win = await l.app.firstWindow()
  await l.app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].setSize(1280, 720))
})
test.afterAll(async () => { await l?.close() })

test('the theme control switches data-theme and remembers the choice', async () => {
  const html = win.locator('html')
  await expect(html).toHaveAttribute('data-theme', /^(dark|light)$/) // set by theme-boot.js before React
  await unlock(win)
  await win.getByRole('radio', { name: 'Light' }).click()
  await expect(html).toHaveAttribute('data-theme', 'light')
  expect(await win.evaluate(() => localStorage.getItem('ssh-mcp.theme'))).toBe('light')
  await win.getByRole('radio', { name: 'Dark' }).click()
  await expect(html).toHaveAttribute('data-theme', 'dark')
  await win.getByRole('radio', { name: 'Auto' }).click()
  expect(await win.evaluate(() => localStorage.getItem('ssh-mcp.theme'))).toBe('auto')
})

test('Hosts is the home tab, search filters the cards, closing the last tab returns home', async () => {
  const hosts = win.locator('nav.hosts')
  await expect(hosts).toBeVisible()
  const search = hosts.getByRole('searchbox', { name: 'Search hosts' })
  await search.fill('nomatch')
  await expect(hosts).toContainText('No hosts match "nomatch"')
  await search.fill('BO')
  await expect(hosts.getByRole('button', { name: 'box', exact: true })).toBeVisible()
  await search.fill('')
  await hosts.getByRole('button', { name: 'box', exact: true }).click()
  await expect(win.locator('.xterm')).toBeVisible()
  await expect(hosts).toBeHidden()
  await win.locator('.tabbar .tab').first().getByRole('button', { name: 'Close' }).click()
  await expect(hosts).toBeVisible()
})
