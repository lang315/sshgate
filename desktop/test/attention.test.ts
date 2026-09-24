import { describe, expect, it, vi } from 'vitest'

vi.mock('electron', () => ({}))
const { notificationText, PendingCounter, trayTitle, trayTooltip, ICON_PNG_BASE64 } = await import('../src/main/attention')

const req = { id: 'a', client: 'c', server: 'box', command: 'x'.repeat(200), description: '', sudo: true, timeoutSec: 60, receivedAt: '' }

describe('attention', () => {
  it('formats tray text', () => {
    expect(trayTitle(0)).toBe('')
    expect(trayTitle(3)).toBe('3')
    expect(trayTooltip(0)).toBe('ssh-mcp')
    expect(trayTooltip(1)).toBe('ssh-mcp: 1 request waiting')
    expect(trayTooltip(2)).toBe('ssh-mcp: 2 requests waiting')
  })
  it('formats notifications and truncates', () => {
    const n = notificationText(req)
    expect(n.title).toBe('AI wants to run a command with sudo')
    expect(n.body.startsWith('box: xxx')).toBe(true)
    expect(n.body.length).toBeLessThanOrEqual(121)
    expect(n.body.endsWith('…')).toBe(true)
  })
  it('counts pending by id', () => {
    const c = new PendingCounter()
    c.apply('pending', { request: { id: 'a' } })
    c.apply('pending', { request: { id: 'a' } })
    c.apply('pending', { request: { id: 'b' } })
    expect(c.count).toBe(2)
    c.apply('decided', { request: { id: 'a' } })
    expect(c.count).toBe(1)
    c.apply('term.data', { id: 'x' })
    expect(c.count).toBe(1)
  })
  it('icon constant decodes to a 16x16 PNG', () => {
    const bytes = Buffer.from(ICON_PNG_BASE64, 'base64')
    const sig = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]
    expect(Array.from(bytes.subarray(0, 8))).toEqual(sig)
    // IHDR chunk: length(4) + 'IHDR'(4) + width(4) + height(4), starting at byte 8
    expect(bytes.subarray(12, 16).toString('ascii')).toBe('IHDR')
    const width = bytes.readUInt32BE(16)
    const height = bytes.readUInt32BE(20)
    expect(width).toBe(16)
    expect(height).toBe(16)
  })
})
