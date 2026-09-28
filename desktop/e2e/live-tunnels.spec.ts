import { test, expect, type Page } from '@playwright/test'
import * as net from 'node:net'
import { launchLive, waitHumanUnlock, type LiveLaunched } from './launch'

// Opt-in: the slice 3b exit gate against a real host from a copy of the real
// vault. SSHGATE_LIVE_HOST names the server; a person unlocks the app when
// asked (twice: at start and after the lock). A local tunnel and a dynamic
// (SOCKS5) tunnel both reach SSHGATE_LIVE_TUNNEL_TARGET as seen from the
// server (default 127.0.0.1:22, the host's own sshd), whose "SSH-" banner
// proves bytes came through. Only the vault copy is written.
const host = process.env.SSHGATE_LIVE_HOST ?? ''
const [targetHost, targetPortText] = (process.env.SSHGATE_LIVE_TUNNEL_TARGET ?? '127.0.0.1:22').split(/:(?=\d+$)/)
const targetPort = Number(targetPortText)
test.skip(!host, 'set SSHGATE_LIVE_HOST to a server in your vault')
test.setTimeout(20 * 60_000)

let l: LiveLaunched
test.beforeAll(async () => { test.setTimeout(10 * 60_000); l = await launchLive() })
test.afterAll(async () => { await l?.close() })

const freePort = () => new Promise<number>((resolve) => {
  const s = net.createServer().listen(0, '127.0.0.1', () => { const p = (s.address() as net.AddressInfo).port; s.close(() => resolve(p)) })
})
const connect = (port: number) => new Promise<net.Socket>((resolve, reject) => {
  const c = net.connect(port, '127.0.0.1', () => resolve(c)); c.once('error', reject)
})
// read collects bytes on c until it has n of them.
const read = (c: net.Socket, n: number) => new Promise<Buffer>((resolve, reject) => {
  let buf = Buffer.alloc(0)
  const timer = setTimeout(() => reject(new Error(`timed out with ${buf.length}/${n} bytes`)), 15_000)
  const on = (d: Buffer) => { buf = Buffer.concat([buf, d]); if (buf.length >= n) { clearTimeout(timer); c.off('data', on); resolve(buf) } }
  c.on('data', on)
  // The hub closes a tunnelled connection whose target refused it.
  c.once('close', () => { clearTimeout(timer); if (buf.length < n) reject(new Error(`closed after ${buf.length}/${n} bytes (is ${targetHost}:${targetPort} listening on the server?)`)) })
})
async function bannerLocal(port: number): Promise<string> {
  const c = await connect(port)
  try { return (await read(c, 4)).toString('latin1') } finally { c.destroy() }
}
// A SOCKS5 CONNECT by domain name, resolved on the server, then the banner.
async function bannerSocks(port: number): Promise<string> {
  const c = await connect(port)
  try {
    c.write(Buffer.from([5, 1, 0]))
    expect([...(await read(c, 2))]).toEqual([5, 0])
    const name = Buffer.from(targetHost)
    c.write(Buffer.concat([Buffer.from([5, 1, 0, 3, name.length]), name, Buffer.from([targetPort >> 8, targetPort & 0xff])]))
    const rep = await read(c, 14) // 10-byte reply, then at least "SSH-"
    expect(rep[1]).toBe(0)
    return rep.subarray(10).toString('latin1')
  } finally { c.destroy() }
}

async function addTunnel(win: Page, kind: 'Local' | 'Dynamic', listen: number) {
  await win.getByRole('button', { name: 'Add tunnel' }).click()
  const d = win.getByRole('dialog', { name: 'Add tunnel' })
  await d.getByRole('radio', { name: kind }).check()
  await d.getByLabel('Listen port').fill(String(listen))
  if (kind === 'Local') {
    await d.getByLabel('Target host').fill(targetHost)
    await d.getByLabel('Target port').fill(String(targetPort))
  }
  await d.getByLabel('Label').fill(`e2e ${kind.toLowerCase()}`)
  await d.getByRole('button', { name: 'Save' }).click()
  await expect(d).toBeHidden()
}

test('exit gate: local and dynamic tunnels on a real host, across a lock', async () => {
  const win = await l.app.firstWindow()
  await waitHumanUnlock(win, 'Unlock 1 of 2 (start)')
  await win.locator('.tabbar .hometab').click()
  await win.getByRole('button', { name: `Tunnels ${host}`, exact: true }).click()
  const view = win.getByRole('region', { name: `Tunnels on ${host}` })
  await expect(view).toBeVisible()

  const lp = await freePort()
  const dp = await freePort()
  await addTunnel(win, 'Local', lp)
  await addTunnel(win, 'Dynamic', dp)
  const row = (label: string) => view.locator('tbody tr', { hasText: label })
  for (const label of ['e2e local', 'e2e dynamic']) {
    await row(label).getByRole('button', { name: 'Start' }).click()
    await expect(row(label).locator('td.status')).toHaveText('running', { timeout: 60_000 })
  }
  console.log(`>>> local 127.0.0.1:${lp} → ${targetHost}:${targetPort}, SOCKS5 127.0.0.1:${dp}`)
  expect(await bannerLocal(lp)).toMatch(/^SSH-/)
  expect(await bannerSocks(dp)).toMatch(/^SSH-/)
  await win.locator('.tabbar .hometab').click()
  await expect(win.locator('.hostcard .chip.tunnels')).toHaveText('2 ⇄')

  // Lock: both tunnels keep carrying new connections.
  await win.getByRole('button', { name: 'Lock' }).click()
  await expect(win.getByLabel('Master password')).toBeVisible()
  expect(await bannerLocal(lp)).toMatch(/^SSH-/)
  expect(await bannerSocks(dp)).toMatch(/^SSH-/)
  console.log('>>> both tunnels still work while the vault is locked')
  await waitHumanUnlock(win, 'Unlock 2 of 2 (after the lock check)')

  // Stop both; the ports close.
  await win.locator('.tabbar .tab[data-kind="tunnels"] .tabname').click()
  for (const label of ['e2e local', 'e2e dynamic']) {
    await row(label).getByRole('button', { name: 'Stop' }).click()
    await expect(row(label).locator('td.status')).toHaveText('stopped')
  }
  await expect(connect(lp)).rejects.toThrow()
  await expect(connect(dp)).rejects.toThrow()
})
