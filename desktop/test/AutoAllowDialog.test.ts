import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { AutoAllowDialog } from '../src/renderer/AutoAllowDialog'
import type { ServerInfo } from '../src/shared/protocol'

const server = (over: Partial<ServerInfo> = {}): ServerInfo => ({
  name: 'box', host: 'h', port: 22, user: 'u', auth: 'password', keyPath: '',
  hostKey: 'k', hostKeyAlgo: 'ssh-ed25519', aiVisible: true, locked: false,
  hasPassword: true, hasSuPassword: false, hasSudoPassword: false, hasKeyPassphrase: false,
  ...over,
})

const buttonsOf = (html: string) =>
  [...html.matchAll(/<button([^>]*)>(.*?)<\/button>/g)]
    .map((m) => ({ attrs: m[1], text: m[2].replace(/<kbd[^>]*>.*?<\/kbd>/g, '').replace(/<[^>]+>/g, '') }))

describe('AutoAllowDialog', () => {
  it('shows why auto-allow is refused and keeps Enable disabled', () => {
    const html = renderToStaticMarkup(createElement(AutoAllowDialog, {
      server: server({ autoAllowRefused: 'root login' }), remoteTunnels: [],
      check: async () => ({ uid: 1000, passwordlessSudo: false }),
      onEnable: async () => {}, onCancel: () => {},
    }))
    expect(html).toContain('Auto-allow is not available here: root login')
    const enable = buttonsOf(html).find((b) => b.text === 'Enable')!
    expect(enable.attrs).toMatch(/disabled=""/)
  })

  it('shows the six duration options, the prompt-injection warning, and sudo-exec note, with a mouse-only disabled Enable', () => {
    const html = renderToStaticMarkup(createElement(AutoAllowDialog, {
      server: server(), remoteTunnels: [],
      check: async () => ({ uid: 1000, passwordlessSudo: false }),
      onEnable: async () => {}, onCancel: () => {},
    }))
    for (const label of ['15 min', '30 min', '60 min', '2 h', '4 h', 'Until turned off']) expect(html).toContain(label)
    expect(html.match(/type="radio"/g) ?? []).toHaveLength(6)
    expect(html).toContain('including commands planted by what it reads')
    expect(html).toContain('sudo-exec still asks')
    const enable = buttonsOf(html).find((b) => b.text === 'Enable')!
    expect(enable.attrs).toMatch(/type="button"/)
    expect(enable.attrs).toMatch(/tabindex="-1"/)
    expect(enable.attrs).toMatch(/disabled=""/)
  })

  it('warns that running remote tunnels reach this machine', () => {
    const html = renderToStaticMarkup(createElement(AutoAllowDialog, {
      server: server(), remoteTunnels: ['R server 127.0.0.1:2222 → localhost:22', 'R server 127.0.0.1:3333 → localhost:80'],
      check: async () => ({ uid: 1000, passwordlessSudo: false }),
      onEnable: async () => {}, onCancel: () => {},
    }))
    expect(html).toContain('Remote tunnels running on this host reach your machine')
    expect(html).toContain('R server 127.0.0.1:2222 → localhost:22')
    expect(html).toContain('R server 127.0.0.1:3333 → localhost:80')
  })
})
