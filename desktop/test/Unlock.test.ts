import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { Unlock } from '../src/renderer/Unlock'

const render = (props: Partial<Parameters<typeof Unlock>[0]>) =>
  renderToStaticMarkup(createElement(Unlock, { onUnlock: async () => {}, ...props }))

describe('Unlock', () => {
  it('names the hosts still on auto-allow and offers a stop', () => {
    const html = render({ lockReason: 'idle', autoHosts: ['box', 'db'], onStop: () => {} })
    expect(html).toContain('AI auto-allow is still running on: box, db')
    expect(html).toContain('Stop auto-allow and lock')
    expect(html).not.toContain('AI requests are refused until you unlock.')
    expect(html).toContain('Locked after inactivity.')
  })
  it('says AI requests are refused when no host is on auto-allow', () => {
    const html = render({ lockReason: 'idle', autoHosts: [] })
    expect(html).toContain('AI requests are refused until you unlock.')
    expect(html).not.toContain('Stop auto-allow and lock')
    expect(render({ lockReason: 'manual' })).not.toContain('Stop auto-allow and lock')
  })
})
