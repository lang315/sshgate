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

const dialog = (over: Partial<Parameters<typeof AutoAllowDialog>[0]> = {}) => renderToStaticMarkup(createElement(AutoAllowDialog, {
  server: server(), mode: '15m', typeName: false, rootNew: false, sudoNew: false, sudo: false, remoteTunnels: [],
  check: async () => ({ uid: 1000, passwordlessSudo: false }),
  onEnable: async () => {}, onCancel: () => {},
  ...over,
}))

const buttonsOf = (html: string) =>
  [...html.matchAll(/<button([^>]*)>(.*?)<\/button>/g)]
    .map((m) => ({ attrs: m[1], text: m[2].replace(/<kbd[^>]*>.*?<\/kbd>/g, '').replace(/<[^>]+>/g, '') }))

describe('AutoAllowDialog', () => {
  it('shows why auto-allow is refused and keeps Enable disabled', () => {
    const html = dialog({ refused: 'root login' })
    expect(html).toContain('Auto-allow is not available here: root login')
    const enable = buttonsOf(html).find((b) => b.text === 'Enable')!
    expect(enable.attrs).toMatch(/disabled=""/)
  })

  it('shows the chosen duration with no radios, the prompt-injection warning, and sudo-exec note, with a mouse-only disabled Enable', () => {
    const html = dialog()
    expect(html).not.toContain('type="radio"')
    expect(html).toMatch(/For 15 min \(ends at \d{1,2}:\d{2}/)
    expect(html).toContain('including commands planted by what it reads')
    expect(html).toContain('sudo-exec still asks')
    const enable = buttonsOf(html).find((b) => b.text === 'Enable')!
    expect(enable.attrs).toMatch(/type="button"/)
    expect(enable.attrs).toMatch(/tabindex="-1"/)
    expect(enable.attrs).toMatch(/disabled=""/)
  })

  it('shows "Until turned off" for the forever mode', () => {
    const html = dialog({ mode: 'forever' })
    expect(html).toContain('Until turned off')
    expect(html).toContain('keeps running on this host')
  })

  it('says the AI keeps running on a timed grant while locked from inactivity', () => {
    expect(dialog({ mode: '15m' })).toContain('keeps running on this host')
  })

  it('shows the typed-name input only when typeName is true', () => {
    expect(dialog({ mode: 'forever', typeName: true })).toContain('Type box to confirm')
    expect(dialog({ mode: 'forever', typeName: false })).not.toContain('Type box to confirm')
    expect(dialog({ typeName: false })).not.toContain('Type box to confirm')
  })

  it('shows the root warning only when rootNew', () => {
    const warning = 'Allowing root: the AI runs as root, and a command it plants can capture sudo or su passwords you type or store'
    expect(dialog({ rootNew: true })).toContain(warning)
    expect(dialog({ rootNew: false })).not.toContain(warning)
  })

  it('shows the sudo warning only when sudoNew', () => {
    const warning = 'sudo-exec will run without asking: the AI has full root on this host'
    expect(dialog({ sudoNew: true })).toContain(warning)
    expect(dialog({ sudoNew: false })).not.toContain(warning)
  })

  it('states the sudo-exec status without contradicting the sudo opt-in', () => {
    const stillAsks = 'sudo-exec still asks.'
    const runsToo = 'sudo-exec also runs without asking on this host, the same as plain exec.'
    // Off: still asks, no other sudo statement.
    const off = dialog({ sudo: false, sudoNew: false })
    expect(off).toContain(stillAsks)
    expect(off).not.toContain(runsToo)
    // Newly ticked: the red warning carries the message; no "still asks".
    const newlyOn = dialog({ sudo: true, sudoNew: true })
    expect(newlyOn).not.toContain(stillAsks)
    expect(newlyOn).not.toContain(runsToo)
    expect(newlyOn).toContain('sudo-exec will run without asking')
    // Already on (re-arming an existing grant): the plain statement, no "still asks".
    const alreadyOn = dialog({ sudo: true, sudoNew: false })
    expect(alreadyOn).not.toContain(stillAsks)
    expect(alreadyOn).toContain(runsToo)
  })

  it('warns that running remote tunnels reach this machine', () => {
    const html = dialog({ remoteTunnels: ['R server 127.0.0.1:2222 → localhost:22', 'R server 127.0.0.1:3333 → localhost:80'] })
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
    const changes = new ListChanges(dialogChangeKey('15m', false, false, false, undefined, undefined, []), 0)
    // The sudo check resolves at T=1000, revealing the "root access" warning
    // and shifting the buttons down.
    changes.setKey(dialogChangeKey('15m', false, false, false, { uid: 0, passwordlessSudo: false }, undefined, []), 1000)
    expect(changes.at).toBe(1000)
    const allowedAt = (now: number) => enableAllowed({ typeName: false, typed: '', host: 'h', openedAt: 0, changedAt: changes.at, now })
    expect(allowedAt(1000)).toBe(false)
    expect(allowedAt(1499)).toBe(false)
    expect(allowedAt(1500)).toBe(true)
  })

  it('does not restart the delay when the key is unchanged', () => {
    const changes = new ListChanges(dialogChangeKey('15m', false, false, false, undefined, undefined, []), 0)
    changes.setKey(dialogChangeKey('15m', false, false, false, undefined, undefined, []), 1000) // re-render, no real change
    expect(changes.at).toBe(0)
  })

  it('changes key when the check result changes, when rootNew/sudoNew flip, but not when only typed changes', () => {
    const pending = dialogChangeKey('forever', false, false, false, undefined, undefined, [])
    const root = dialogChangeKey('forever', false, false, false, { uid: 0, passwordlessSudo: false }, undefined, [])
    const error = dialogChangeKey('forever', false, false, false, 'error', undefined, [])
    expect(root).not.toBe(pending)
    expect(error).not.toBe(pending)
    expect(root).not.toBe(error)
    expect(dialogChangeKey('forever', true, false, false, undefined, undefined, [])).not.toBe(pending)
    expect(dialogChangeKey('forever', false, true, false, undefined, undefined, [])).not.toBe(pending)
    expect(dialogChangeKey('forever', false, false, true, undefined, undefined, [])).not.toBe(pending)
    // Typing the confirmation text isn't part of the key at all.
    expect(dialogChangeKey('forever', false, false, false, undefined, undefined, [])).toBe(pending)
  })

  it('changes key when an enable error appears or the remote-tunnels list changes', () => {
    const base = dialogChangeKey('15m', false, false, false, undefined, undefined, [])
    expect(dialogChangeKey('15m', false, false, false, undefined, 'boom', [])).not.toBe(base)
    expect(dialogChangeKey('15m', false, false, false, undefined, undefined, ['t1'])).not.toBe(base)
    expect(dialogChangeKey('15m', false, false, false, undefined, undefined, [])).toBe(base)
  })
})
