import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { closesTabs, draftFrom, endpointChanged, filterHosts, passwordChecks, secretPlaceholder, toInput } from '../src/renderer/hostForm'
import { EditorWarnings, HostEditor } from '../src/renderer/HostEditor'
import type { ServerInfo } from '../src/shared/protocol'

const box: ServerInfo = {
  name: 'box', host: 'h', port: 22, user: 'u', auth: 'password', keyPath: '', hostKey: 'SHA256:x', hostKeyAlgo: 'ssh-ed25519',
  aiVisible: false, locked: false, hasPassword: true, hasSuPassword: false, hasSudoPassword: false, hasKeyPassphrase: false,
}
const noop = async () => {}

describe('host editor secrets', () => {
  it('omits untouched secrets, sends "" for cleared ones and the value for typed ones', () => {
    const d = draftFrom(box)
    d.secrets.password = { value: '', cleared: true }
    d.secrets.suPassword = { value: 'su!', cleared: false }
    const input = toInput(d)
    expect(input).toEqual({ name: 'box', host: 'h', port: 22, user: 'u', auth: 'password', keyPath: '', aiVisible: false, password: '', suPassword: 'su!' })
    expect('sudoPassword' in input).toBe(false)
    expect('keyPassphrase' in input).toBe(false)
  })
  it('shows "saved" for a stored secret and offers Clear only for it', () => {
    expect(secretPlaceholder(true, { value: '', cleared: false })).toBe('saved')
    expect(secretPlaceholder(false, { value: '', cleared: false })).toBe('')
    expect(secretPlaceholder(true, { value: '', cleared: true })).toBe('will be cleared')
    const html = renderToStaticMarkup(createElement(HostEditor, { server: box, openTabs: 0, onSave: noop, onForget: noop, onClose: () => {} }))
    expect(html.match(/placeholder="saved"/g)).toHaveLength(1)
    expect(html.match(/>Clear</g)).toHaveLength(1)
    expect(html).toContain('SHA256:x')
    expect(html).toContain('Forget host key')
  })
  it('says a saved secret will be cleared while the endpoint is changed', () => {
    expect(secretPlaceholder(true, { value: '', cleared: false }, true)).toBe('will be cleared')
    expect(secretPlaceholder(false, { value: '', cleared: false }, true)).toBe('')
    expect(secretPlaceholder(true, { value: 'new', cleared: false }, true)).toBe('saved')
  })
  it('renders the sheet with the full fingerprint, a unique Close, and a labelled auth group', () => {
    const html = renderToStaticMarkup(createElement(HostEditor, { server: box, openTabs: 0, onSave: noop, onForget: noop, onClose: () => {} }))
    expect(html).toContain('ssh-ed25519 SHA256:x')
    expect(html).toContain('aria-label="Close host editor"')
    expect(html.match(/>Close</g)).toHaveLength(1)
    expect(html).toContain('role="radiogroup" aria-label="Auth"')
    expect(html).toContain('Never shown. Leave empty to keep what is saved.')
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
    const html = renderToStaticMarkup(createElement(EditorWarnings, { server: box, draft: { ...d, port: '2222' }, openTabs: 2 }))
    expect(html).toContain('Changing host or port forgets the host key and saved passwords unless you re-enter them.')
    expect(html).toContain('Saving will close 2 open tabs.')
  })
  it('closes tabs only for connection changes, not for Visible to AI', () => {
    const d = draftFrom(box)
    expect(closesTabs(box, { ...d, aiVisible: true })).toBe(false)
    expect(closesTabs(box, { ...d, user: 'root' })).toBe(true)
    expect(closesTabs(box, { ...d, secrets: { ...d.secrets, sudoPassword: { value: 'x', cleared: false } } })).toBe(true)
    expect(renderToStaticMarkup(createElement(EditorWarnings, { server: box, draft: { ...d, aiVisible: true }, openTabs: 2 }))).toBe('')
  })
  it('wraps warnings in one status box', () => {
    const d = draftFrom(box)
    const html = renderToStaticMarkup(createElement(EditorWarnings, { server: box, draft: { ...d, port: '2222' }, openTabs: 1 }))
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
