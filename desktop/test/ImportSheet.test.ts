import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { ImportRows, ImportSheet } from '../src/renderer/ImportSheet'
import type { ImportCandidate, ImportScan } from '../src/shared/protocol'

const ready: ImportCandidate = {
  alias: 'web', host: '10.0.0.5', port: 2200, user: 'deploy', auth: 'key', keyPath: '~/.ssh/id_web',
  hostKey: 'SHA256:abc', hostKeyAlgo: 'ssh-ed25519', status: 'ready',
}
const rows = (c: ImportCandidate, checked: string[] = []) =>
  renderToStaticMarkup(createElement(ImportRows, { candidates: [c], checked: new Set(checked), onToggle: () => {} }))

describe('ImportRows', () => {
  it('checks a ready row and shows its address, key path and pin', () => {
    const html = rows(ready, ['web'])
    expect(html).toContain('aria-label="Import web"')
    expect(html).toMatch(/<input[^>]*checked=""/)
    expect(html).toContain('deploy@10.0.0.5:2200 · ~/.ssh/id_web')
    expect(html).toContain('ssh-ed25519 SHA256:abc')
  })
  it('shows a skipped row with its reason and no checkbox', () => {
    const html = rows({ alias: 'jump', host: 'jump', port: 22, user: 'u', auth: 'agent', status: 'skipped', reason: 'needs ProxyJump' })
    expect(html).toContain('needs ProxyJump')
    expect(html).toContain('Skipped')
    expect(html).not.toContain('<input')
  })
  it('marks an alias already in the vault, with no checkbox', () => {
    const html = rows({ ...ready, hostKey: undefined, hostKeyAlgo: undefined, status: 'exists', reason: 'Already in vault' })
    expect(html).toContain('Already in vault')
    expect(html).not.toContain('<input')
    expect(html).not.toContain('not in known_hosts')
  })
  it('says when a key is not in known_hosts or needs a passphrase', () => {
    const html = rows({ ...ready, hostKey: undefined, hostKeyAlgo: undefined, needsPassphrase: true })
    expect(html).toContain('not in known_hosts')
    expect(html).toContain('Key has a passphrase: add it in the editor after import.')
  })
})

describe('ImportSheet', () => {
  it('shows the known_hosts note and a disabled Import button before the scan returns', () => {
    const html = renderToStaticMarkup(createElement(ImportSheet, {
      scan: () => new Promise<ImportScan>(() => {}), apply: async () => ({ imported: [], skipped: [] }),
      onImported: async () => {}, onClose: () => {},
    }))
    expect(html).toContain('aria-label="Import from SSH config"')
    expect(html).toContain('StrictHostKeyChecking accept-new')
    expect(html).toMatch(/<button type="submit"[^>]*disabled=""/)
  })
})
