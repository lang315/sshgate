import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { AutoAllowDialog } from '../src/renderer/AutoAllowDialog'
import { ListChanges } from '../src/renderer/approvals'
import { dialogChangeKey, enableAllowed } from '../src/renderer/autoallow'
import type { ServerInfo } from '../src/shared/protocol'

const server = (over: Partial<ServerInfo> = {}): ServerInfo => ({
  name: 'box', host: 'h', port: 22, user: 'u', auth: 'password', keyPath: '',
  hostKey: 'k', hostKeyAlgo: 'ssh-ed25519', aiVisible: true, locked: false,
  hasPassword: true, hasSuPassword: false, hasSudoPassword: false, hasKeyPassphrase: false,
  autoAllowRoot: false, autoAllowSudo: false,
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

  it('shows the timed grant\'s end time computed from the selected duration', () => {
    const html = renderToStaticMarkup(createElement(AutoAllowDialog, {
      server: server(), remoteTunnels: [],
      check: async () => ({ uid: 1000, passwordlessSudo: false }),
      onEnable: async () => {}, onCancel: () => {},
    }))
    expect(html).toMatch(/Ends at \d{1,2}:\d{2}.*, when you stop it, or when you lock the vault\. The vault will not auto-lock before then\./)
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

// The dialog computes changedAt from ListChanges.setKey called during render
// (not a post-paint useEffect), so a change is never missed for one painted
// frame. This pins the key -> time bookkeeping the dialog relies on.
describe('dialogChangeKey + ListChanges bookkeeping (the dialog\'s Enable delay)', () => {
  it('restarts the delay synchronously when the root-access check resolves mid-dialog', () => {
    const changes = new ListChanges(dialogChangeKey('15m', undefined, undefined, []), 0)
    // The sudo check resolves at T=1000, revealing the "root access" warning
    // and shifting the buttons down.
    changes.setKey(dialogChangeKey('15m', { uid: 0, passwordlessSudo: false }, undefined, []), 1000)
    expect(changes.at).toBe(1000)
    const allowedAt = (now: number) => enableAllowed({ mode: '15m', typed: '', host: 'h', openedAt: 0, changedAt: changes.at, now })
    expect(allowedAt(1000)).toBe(false)
    expect(allowedAt(1499)).toBe(false)
    expect(allowedAt(1500)).toBe(true)
  })

  it('does not restart the delay when the key is unchanged', () => {
    const changes = new ListChanges(dialogChangeKey('15m', undefined, undefined, []), 0)
    changes.setKey(dialogChangeKey('15m', undefined, undefined, []), 1000) // re-render, no real change
    expect(changes.at).toBe(0)
  })

  it('changes key when the check result changes but not when only typed changes', () => {
    const pending = dialogChangeKey('forever', undefined, undefined, [])
    const root = dialogChangeKey('forever', { uid: 0, passwordlessSudo: false }, undefined, [])
    const error = dialogChangeKey('forever', 'error', undefined, [])
    expect(root).not.toBe(pending)
    expect(error).not.toBe(pending)
    expect(root).not.toBe(error)
    // Typing the confirmation text isn't part of the key at all.
    expect(dialogChangeKey('forever', undefined, undefined, [])).toBe(pending)
  })

  it('changes key when an enable error appears or the remote-tunnels list changes', () => {
    const base = dialogChangeKey('15m', undefined, undefined, [])
    expect(dialogChangeKey('15m', undefined, 'boom', [])).not.toBe(base)
    expect(dialogChangeKey('15m', undefined, undefined, ['t1'])).not.toBe(base)
    expect(dialogChangeKey('15m', undefined, undefined, [])).toBe(base)
  })
})
