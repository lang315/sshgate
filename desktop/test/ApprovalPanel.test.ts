import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { ApprovalPanel, Item } from '../src/renderer/ApprovalPanel'
import { seed } from '../src/renderer/approvals'
import type { ApprovalRequest } from '../src/shared/protocol'

const req: ApprovalRequest = {
  id: 'a', client: 'claude-code', server: 'box', target: 'u@h:22', command: 'ls', description: '', sudo: false, timeoutSec: 60, receivedAt: '2024-01-01T00:00:00Z',
}

describe('Item', () => {
  it('makes Allow and Send to tab keyboard-unreachable; only Deny is a submit button', () => {
    const [item] = seed([req], 0)
    const html = renderToStaticMarkup(createElement(Item, {
      item, now: 10_000,
      onDecide: async () => {}, onSendToTab: async () => {}, onEscape: () => {},
    }))
    const buttons = [...html.matchAll(/<button([^>]*)>(.*?)<\/button>/g)]
      .map((m) => ({ attrs: m[1], text: m[2].replace(/<kbd[^>]*>.*?<\/kbd>/g, '').replace(/<[^>]+>/g, '') }))

    const submitButtons = buttons.filter((b) => /type="submit"/.test(b.attrs))
    expect(submitButtons).toHaveLength(1)
    expect(submitButtons[0].text).toBe('Deny')

    const allow = buttons.find((b) => b.text === 'Allow')!
    expect(allow.attrs).toMatch(/type="button"/)
    expect(allow.attrs).toMatch(/tabindex="-1"/)

    const sendToTab = buttons.find((b) => b.text === 'Send to tab')!
    expect(sendToTab.attrs).toMatch(/type="button"/)
    expect(sendToTab.attrs).toMatch(/tabindex="-1"/)
  })

  it('shows the user@host:port the request was submitted for', () => {
    const [item] = seed([req], 0)
    const html = renderToStaticMarkup(createElement(Item, { item, now: 10_000, onDecide: async () => {}, onSendToTab: async () => {}, onEscape: () => {} }))
    expect(html).toContain('u@h:22')
  })

  it('keeps Allow and Send to tab disabled while the item is young, and never autofocuses', () => {
    const [item] = seed([req], 0)
    const html = renderToStaticMarkup(createElement(Item, { item, now: 100, onDecide: async () => {}, onSendToTab: async () => {}, onEscape: () => {} }))
    const buttons = [...html.matchAll(/<button([^>]*)>(.*?)<\/button>/g)].map((m) => ({ attrs: m[1], text: m[2].replace(/<[^>]+>/g, '') }))
    expect(buttons.find((b) => b.text === 'Allow')!.attrs).toMatch(/disabled=""/)
    expect(buttons.find((b) => b.text === 'Send to tab')!.attrs).toMatch(/disabled=""/)
    expect(html).not.toMatch(/autofocus/i)
  })

  it('labels the reason field, hides the Enter hint from the name, and flags sudo in red', () => {
    const [item] = seed([{ ...req, sudo: true, command: 'rm gіt', description: 'cleanup' }], 0)
    const html = renderToStaticMarkup(createElement(Item, { item, now: 10_000, onDecide: async () => {}, onSendToTab: async () => {}, onEscape: () => {} }))
    expect(html).toMatch(/<label[^>]*>Reason \(optional\)/)
    expect(html).toContain('placeholder="Reason (optional)"')
    expect(html).toMatch(/<kbd aria-hidden="true">↵<\/kbd>/)
    expect(html).toContain('class="approval sudo"')
    expect(html).toContain('1 non-ASCII character highlighted (U+0456)')
    expect(html).toContain('AI&#x27;s description · unverified')
    expect(html).toContain('client claude-code (unverified)')
  })
})

// Finds the substring of `html` between a div's own open tag (matched by `openTag`)
// and its matching close tag, tracking div-nesting depth so a nested div inside
// doesn't end the region early.
function divContents(html: string, openTag: RegExp): string {
  const m = openTag.exec(html)
  if (!m) throw new Error(`open tag not found: ${openTag}`)
  let depth = 1
  const tagRe = /<(\/?)div\b[^>]*>/g
  tagRe.lastIndex = m.index + m[0].length
  let t: RegExpExecArray | null
  while ((t = tagRe.exec(html))) {
    depth += t[1] === '/' ? -1 : 1
    if (depth === 0) return html.slice(m.index + m[0].length, t.index)
  }
  throw new Error('unbalanced div nesting')
}

describe('ApprovalPanel', () => {
  it('keeps the Auto-allowed feed outside the scroll region the ResizeObserver watches', () => {
    const html = renderToStaticMarkup(createElement(ApprovalPanel, {
      items: [], seedError: undefined,
      onDecide: async () => {}, onDenyAll: async () => {}, onSendToTab: async () => {},
      onClose: () => {}, onEscape: () => {},
      autoFeed: [], autoN: 0, paused: [], onStopAll: () => {}, onResume: () => {},
    }))
    expect(html).toContain('autofeed')
    expect(divContents(html, /<div class="approvals-scroll"[^>]*>/)).not.toContain('autofeed')
  })
})
