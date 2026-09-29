import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { arrowStep, closesTabs, closeWarning, draftFrom, endpointChanged, labelHint, filterHosts, passwordChecks, secretPlaceholder, toInput } from '../src/renderer/hostForm'
import { EditorWarnings, HostEditor } from '../src/renderer/HostEditor'
import type { ServerInfo } from '../src/shared/protocol'

const box: ServerInfo = {
  name: 'box', host: 'h', port: 22, user: 'u', auth: 'password', keyPath: '', hostKey: 'SHA256:x', hostKeyAlgo: 'ssh-ed25519',
  aiVisible: false, locked: false, hasPassword: true, hasSuPassword: false, hasSudoPassword: false, hasKeyPassphrase: false,
  autoAllowRoot: false, autoAllowSudo: false,
}
const noop = async () => {}
const check = async () => ({ uid: 1000, passwordlessSudo: false })

describe('host editor secrets', () => {
  it('omits untouched secrets, sends "" for cleared ones and the value for typed ones', () => {
    const d = draftFrom(box)
    d.secrets.password = { value: '', cleared: true }
    d.secrets.suPassword = { value: 'su!', cleared: false }
    const input = toInput(d)
    expect(input).toEqual({ name: 'box', host: 'h', port: 22, user: 'u', auth: 'password', keyPath: '', aiVisible: false, autoAllowRoot: false, autoAllowSudo: false, password: '', suPassword: 'su!' })
    expect('sudoPassword' in input).toBe(false)
    expect('keyPassphrase' in input).toBe(false)
  })
  it('shows "saved" for a stored secret and offers Clear only for it', () => {
    expect(secretPlaceholder(true, { value: '', cleared: false })).toBe('saved')
    expect(secretPlaceholder(false, { value: '', cleared: false })).toBe('')
    expect(secretPlaceholder(true, { value: '', cleared: true })).toBe('will be cleared')
    const html = renderToStaticMarkup(createElement(HostEditor, { server: box, openTabs: 0, transfers: 0, remoteTunnels: [], autoAllowCheck: check, onSave: noop, onForget: noop, onClose: () => {} }))
    expect(html.match(/placeholder="saved"/g)).toHaveLength(1)
    expect(html.match(/>Clear</g)).toHaveLength(1)
    expect(html).toContain('SHA256:x')
    expect(html).toContain('Forget host key')
  })
  it('moves the Auth choice with arrow keys, wrapping, and keeps one radio in the tab order', () => {
    const auths = ['password', 'key', 'agent'] as const
    expect(arrowStep(auths, 'password', 'ArrowRight')).toBe('key')
    expect(arrowStep(auths, 'agent', 'ArrowDown')).toBe('password')
    expect(arrowStep(auths, 'password', 'ArrowLeft')).toBe('agent')
    expect(arrowStep(auths, 'key', 'ArrowUp')).toBe('password')
    expect(arrowStep(auths, 'key', 'Enter')).toBeUndefined()
    const html = renderToStaticMarkup(createElement(HostEditor, { server: { ...box, auth: 'key' }, openTabs: 0, transfers: 0, remoteTunnels: [], autoAllowCheck: check, onSave: noop, onForget: noop, onClose: () => {} }))
    expect(html.match(/role="radio"[^>]*tabindex="0"/g)).toHaveLength(1)
    expect(html).toMatch(/aria-checked="true" data-auth="key" tabindex="0"/)
  })
  it('says a saved secret will be cleared while the endpoint is changed', () => {
    expect(secretPlaceholder(true, { value: '', cleared: false }, true)).toBe('will be cleared')
    expect(secretPlaceholder(false, { value: '', cleared: false }, true)).toBe('')
    expect(secretPlaceholder(true, { value: 'new', cleared: false }, true)).toBe('saved')
  })
  it('renders an existing host with the full fingerprint, a unique Close, and the saved-secrets note', () => {
    const html = renderToStaticMarkup(createElement(HostEditor, { server: box, openTabs: 0, transfers: 0, remoteTunnels: [], autoAllowCheck: check, onSave: noop, onForget: noop, onClose: () => {} }))
    expect(html).toContain('ssh-ed25519 SHA256:x')
    expect(html).toContain('aria-label="Close host editor"')
    expect(html.match(/>Close</g)).toHaveLength(1)
    expect(html).toContain('Saved secrets are never shown. Leave a field empty to keep it.')
  })
  it('opens a new host at Address, with only user and password, AI off, and extras folded', () => {
    const html = renderToStaticMarkup(createElement(HostEditor, { openTabs: 0, transfers: 0, remoteTunnels: [], autoAllowCheck: check, onSave: noop, onForget: noop, onClose: () => {} }))
    expect(html.indexOf('>Address<')).toBeGreaterThan(-1)
    expect(html.indexOf('>Address<')).toBeLessThan(html.indexOf('>Label<'))
    expect(html).toContain('+ Key or agent')
    expect(html).not.toContain('role="radiogroup"')
    expect(html).not.toContain('Saved secrets are never shown')
    expect(html).toContain('AI access · Off')
    expect(html).not.toMatch(/role="switch"[^>]*checked/)
    expect(html).toMatch(/<details[^>]*class="fold"[^>]*>\s*<summary[^>]*>Privilege escalation/)
  })
})

describe('draftFrom auto-allow', () => {
  it('is "forever" for a forever host, paused or not, and "off" for a timed or off host', () => {
    expect(draftFrom({ ...box, autoAllow: { forever: true } }).autoAllow).toBe('forever')
    expect(draftFrom({ ...box, autoAllow: { forever: true, paused: true } }).autoAllow).toBe('forever')
    expect(draftFrom({ ...box, autoAllow: { until: '2026-01-01T00:00:00Z' } }).autoAllow).toBe('off')
    expect(draftFrom(box).autoAllow).toBe('off')
    expect(draftFrom().autoAllow).toBe('off')
  })
  it('takes autoAllowRoot/autoAllowSudo from the server, false for a new host', () => {
    expect(draftFrom({ ...box, autoAllowRoot: true, autoAllowSudo: true })).toMatchObject({ autoAllowRoot: true, autoAllowSudo: true })
    expect(draftFrom()).toMatchObject({ autoAllowRoot: false, autoAllowSudo: false })
  })
  it('toInput carries autoAllowRoot/autoAllowSudo', () => {
    const d = { ...draftFrom(box), autoAllowRoot: true, autoAllowSudo: true }
    expect(toInput(d)).toMatchObject({ autoAllowRoot: true, autoAllowSudo: true })
  })
})

describe('host label', () => {
  it('defaults the name to the address', () => {
    const d = { ...draftFrom(), host: ' 10.0.4.21 ', user: 'u' }
    expect(toInput(d).name).toBe('10.0.4.21')
    expect(toInput({ ...d, name: ' web ' }).name).toBe('web')
  })
  it('asks for a label only when the address cannot be a name', () => {
    const d = draftFrom()
    expect(labelHint({ ...d, host: 'db.example.com' })).toBeUndefined()
    expect(labelHint({ ...d, host: '2001:db8::1' })).toBe('An IPv6 address cannot be a name: add a label.')
    expect(labelHint({ ...d, host: '2001:db8::1', name: 'v6box' })).toBeUndefined()
  })
})

describe('host editor warnings', () => {
  it('warns that a new host or port forgets the key and saved passwords', () => {
    const d = draftFrom(box)
    expect(endpointChanged(box, d)).toBe(false)
    expect(endpointChanged(box, { ...d, user: 'root' })).toBe(false)
    expect(endpointChanged(box, { ...d, port: '2222' })).toBe(true)
    expect(endpointChanged(box, { ...d, host: 'h2' })).toBe(true)
    expect(endpointChanged(undefined, d)).toBe(false)
    const html = renderToStaticMarkup(createElement(EditorWarnings, { server: box, draft: { ...d, port: '2222' }, openTabs: 2, transfers: 0 }))
    expect(html).toContain('Changing host or port forgets the host key and saved passwords unless you re-enter them.')
    expect(html).toContain('Saving will close 2 open tabs.')
  })
  it('closes tabs only for connection changes, not for Visible to AI', () => {
    const d = draftFrom(box)
    expect(closesTabs(box, { ...d, aiVisible: true })).toBe(false)
    expect(closesTabs(box, { ...d, user: 'root' })).toBe(true)
    expect(closesTabs(box, { ...d, secrets: { ...d.secrets, sudoPassword: { value: 'x', cleared: false } } })).toBe(true)
    expect(renderToStaticMarkup(createElement(EditorWarnings, { server: box, draft: { ...d, aiVisible: true }, openTabs: 2, transfers: 0 }))).toBe('')
  })
  it('wraps warnings in one status box', () => {
    const d = draftFrom(box)
    const html = renderToStaticMarkup(createElement(EditorWarnings, { server: box, draft: { ...d, port: '2222' }, openTabs: 1, transfers: 0 }))
    expect(html.match(/role="status"/g)).toHaveLength(1)
    expect(html).toContain('Saving will close 1 open tab.')
  })
})

describe('filterHosts', () => {
  const other: ServerInfo = { ...box, name: 'db-primary', host: '10.0.4.30', user: 'postgres' }
  it('matches name, host or user, case-insensitively; empty query keeps all', () => {
    expect(filterHosts([box, other], '')).toEqual([box, other])
    expect(filterHosts([box, other], '  ')).toEqual([box, other])
    expect(filterHosts([box, other], 'DB')).toEqual([other])
    expect(filterHosts([box, other], '10.0.4')).toEqual([other])
    expect(filterHosts([box, other], 'postgres')).toEqual([other])
    expect(filterHosts([box, other], 'nothing')).toEqual([])
  })
})

describe('passwordChecks', () => {
  it('needs 8 characters and a non-empty matching confirmation', () => {
    expect(passwordChecks('short', 'short')).toEqual({ length: false, match: true })
    expect(passwordChecks('password1', 'password2')).toEqual({ length: true, match: false })
    expect(passwordChecks('password1', '')).toEqual({ length: true, match: false })
    expect(passwordChecks('password1', 'password1')).toEqual({ length: true, match: true })
  })
})

describe('closeWarning', () => {
  it('counts tabs and transfers', () => {
    expect(closeWarning(2, 0)).toBe('Saving will close 2 open tabs.')
    expect(closeWarning(1, 1)).toBe('Saving will close 1 open tab and cancel 1 transfer.')
    expect(closeWarning(0, 3)).toBe('Saving will cancel 3 transfers.')
  })
})
