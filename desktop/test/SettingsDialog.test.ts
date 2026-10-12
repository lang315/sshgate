import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { SettingsDialog } from '../src/renderer/SettingsDialog'

const render = (over: Partial<Parameters<typeof SettingsDialog>[0]> = {}) => renderToStaticMarkup(createElement(SettingsDialog, {
  limit: '24h', hosts: [], onSave: async () => {}, onCancel: () => {}, ...over,
}))

describe('SettingsDialog', () => {
  it('opens on the value in force, with no notice and a disabled, mouse-only Save', () => {
    const html = render({ limit: '7d' })
    expect(html).toContain('role="dialog"')
    expect(html).toContain('>Settings</h3>')
    expect(html).toContain('>Auto-allow</h4>')
    expect(html).toContain('Keep running while locked')
    expect(html).toMatch(/<option value="7d" selected="">7 days<\/option>/)
    expect(html).not.toContain('banner-wait')
    expect(html).not.toContain('banner-danger')
    const save = html.match(/<button[^>]*>Save<\/button>/)![0]
    expect(save).toMatch(/disabled=""/)
    expect(save).toMatch(/tabindex="-1"/)
    expect(html).toMatch(/<button type="submit" class="btn primary"[^>]*>Cancel/)
  })
  it('marks the select red when the value in force is No limit', () => {
    expect(render({ limit: 'none' })).toContain('class="select danger"')
    expect(render({ limit: 'none' })).toContain('Auto-allow stops only with Stop auto-allow and lock, a restart or a crash.')
  })
})
