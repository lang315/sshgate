import { test, expect, type Page } from '@playwright/test'
import * as net from 'node:net'
import { launch, unlock, type Launched } from './launch'

// Spec 3b §Testing: local and dynamic tunnels through the app, surviving a lock.
test.skip(process.platform === 'win32', 'launch uses /tmp and a Unix socket')

let l: Launched
let echoPort = 0
let echo: net.Server
test.beforeAll(async () => {
  echo = net.createServer((c) => c.pipe(c))
  await new Promise<void>((r) => echo.listen(0, '127.0.0.1', r))
  echoPort = (echo.address() as net.AddressInfo).port
  l = await launch()
})
test.afterAll(async () => { await l?.close(); echo?.close() })

const freePort = () => new Promise<number>((resolve) => {
  const s = net.createServer().listen(0, '127.0.0.1', () => { const p = (s.address() as net.AddressInfo).port; s.close(() => resolve(p)) })
})

// Sends "ping" on c and waits for it back.
const ping = (c: net.Socket) => new Promise<void>((resolve, reject) => {
  c.once('data', (d) => (d.toString() === 'ping' ? resolve() : reject(new Error(`got ${d}`))))
  c.once('error', reject)
  c.write('ping')
})
const connect = (port: number) => new Promise<net.Socket>((resolve, reject) => {
  const c = net.connect(port, '127.0.0.1', () => resolve(c)); c.once('error', reject)
})
// A SOCKS5 CONNECT to 127.0.0.1:target through the proxy on port.
async function socks(port: number, target: number): Promise<net.Socket> {
  const c = await connect(port)
  const read = (n: number) => new Promise<Buffer>((resolve) => {
    let buf = Buffer.alloc(0)
    const on = (d: Buffer) => { buf = Buffer.concat([buf, d]); if (buf.length >= n) { c.off('data', on); resolve(buf) } }
    c.on('data', on)
  })
  c.write(Buffer.from([5, 1, 0]))
  expect([...(await read(2))]).toEqual([5, 0])
  c.write(Buffer.from([5, 1, 0, 1, 127, 0, 0, 1, target >> 8, target & 0xff]))
  expect((await read(10))[1]).toBe(0)
  return c
}

async function addTunnel(win: Page, kind: 'Local' | 'Dynamic', listen: number, target?: number) {
  await win.getByRole('button', { name: 'Add tunnel' }).click()
  const d = win.getByRole('dialog', { name: 'Add tunnel' })
  await d.getByRole('radio', { name: kind }).check()
  await d.getByLabel('Listen port').fill(String(listen))
  if (target) {
    await d.getByLabel('Target host').fill('127.0.0.1')
    await d.getByLabel('Target port').fill(String(target))
  }
  await d.getByLabel('Label').fill(kind.toLowerCase())
  await d.getByRole('button', { name: 'Save' }).click()
  await expect(d).toBeHidden()
}

test('local and dynamic tunnels run, survive a lock, and stop', async () => {
  const win = await l.app.firstWindow()
  await unlock(win)
  await win.locator('.tabbar .hometab').click()
  await win.getByRole('button', { name: 'Tunnels box' }).click()
  const view = win.getByRole('region', { name: 'Tunnels on box' })
  await expect(win.locator('.tabbar .tab.active')).toHaveAttribute('data-kind', 'tunnels')

  const lp = await freePort()
  const dp = await freePort()
  await addTunnel(win, 'Local', lp, echoPort)
  await addTunnel(win, 'Dynamic', dp)
  const rows = view.locator('tbody tr')
  await expect(rows).toHaveCount(2)

  for (const i of [0, 1]) {
    await rows.nth(i).getByRole('button', { name: 'Start' }).click()
    await expect(rows.nth(i).locator('td.status')).toHaveText('running')
  }
  const lc = await connect(lp)
  await ping(lc)
  await expect(rows.nth(0).locator('td.conns')).toHaveText('1')
  const sc = await socks(dp, echoPort)
  await ping(sc)

  // The host card shows the running count.
  await win.locator('.tabbar .hometab').click()
  await expect(win.locator('.hostcard .chip.tunnels')).toHaveText('2 ⇄')

  // Lock: tunnels keep working.
  await win.getByRole('button', { name: 'Lock' }).click()
  await expect(win.getByLabel('Master password')).toBeVisible()
  await ping(lc)
  await ping(sc)
  lc.destroy(); sc.destroy()
  await unlock(win)

  // Stop both; the ports close.
  await win.locator('.tabbar .tab[data-kind="tunnels"] .tabname').click()
  for (const i of [0, 1]) {
    await rows.nth(i).getByRole('button', { name: 'Stop' }).click()
    await expect(rows.nth(i).locator('td.status')).toHaveText('stopped')
  }
  await expect(connect(lp)).rejects.toThrow()
  await win.locator('.tabbar .hometab').click()
  await expect(win.locator('.hostcard .chip.tunnels')).toHaveCount(0)
})
