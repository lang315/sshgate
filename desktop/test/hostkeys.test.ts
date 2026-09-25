import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { HostKeyPrompts, promptKey, trustEnabled } from '../src/renderer/hostkeys'
import { HostKeyPromptView } from '../src/renderer/HostKeyDialog'
import { ListChanges } from '../src/renderer/approvals'
import type { HostKeyUnknown } from '../src/shared/protocol'

const info = (fingerprint: string, knownHosts: HostKeyUnknown['knownHosts'] = 'absent'): HostKeyUnknown => ({
  status: 'hostKeyUnknown', server: 'box', host: 'h', port: 22, user: 'u', fingerprint, keyType: 'ssh-ed25519', knownHosts,
})

describe('HostKeyPrompts', () => {
  it('shows prompts one at a time in arrival order; a closed tab drops its own', async () => {
    const q = new HostKeyPrompts()
    const seen = vi.fn()
    q.subscribe(seen)
    const a = q.ask('t1', info('SHA256:a'))
    const b = q.ask('t2', info('SHA256:b'))
    expect(q.current?.id).toBe('t1')
    q.answer(true)
    await expect(a).resolves.toBe(true)
    expect(q.current?.id).toBe('t2')
    q.drop('t2')
    await expect(b).resolves.toBe(false)
    expect(q.current).toBeUndefined()
    expect(seen).toHaveBeenCalledTimes(4)
  })
})

describe('Trust delay', () => {
  it('lasts 500 ms and restarts whenever the prompt shown changes', () => {
    const q = new HostKeyPrompts()
    q.ask('t1', info('SHA256:a'))
    q.ask('t2', info('SHA256:b'))
    const c = new ListChanges(promptKey(q.current), 0)
    expect(trustEnabled(c.at, 499)).toBe(false)
    expect(trustEnabled(c.at, 500)).toBe(true)
    q.answer(false)
    c.setKey(promptKey(q.current), 600)
    expect(trustEnabled(c.at, 1000)).toBe(false)
    expect(trustEnabled(c.at, 1100)).toBe(true)
  })
})

describe('HostKeyPromptView', () => {
  it('makes Cancel the only Enter target and Trust mouse-only', () => {
    const html = renderToStaticMarkup(createElement(HostKeyPromptView,
      { info: info('SHA256:abc'), trustEnabled: false, onTrust: () => {}, onCancel: () => {} }))
    expect(html).toContain('SHA256:abc')
    expect(html).toContain('u@h:22')
    expect(html).toContain('ssh-ed25519')
    const buttons = [...html.matchAll(/<button([^>]*)>([^<]*)<\/button>/g)].map((m) => ({ attrs: m[1], text: m[2] }))
    expect(buttons.filter((b) => /type="submit"/.test(b.attrs)).map((b) => b.text)).toEqual(['Cancel'])
    const trust = buttons.find((b) => b.text === 'Trust')!
    expect(trust.attrs).toMatch(/type="button"/)
    expect(trust.attrs).toMatch(/tabindex="-1"/)
    expect(trust.attrs).toMatch(/disabled=""/)
  })
  it('warns in the mismatch style when known_hosts lists a different key', () => {
    const html = renderToStaticMarkup(createElement(HostKeyPromptView,
      { info: info('SHA256:abc', 'different'), trustEnabled: true, onTrust: () => {}, onCancel: () => {} }))
    expect(html).toContain('class="mismatch-text"')
    expect(html).toContain('lists a different key')
  })
})
