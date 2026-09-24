import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { Item } from '../src/renderer/ApprovalPanel'
import { seed } from '../src/renderer/approvals'
import type { ApprovalRequest } from '../src/shared/protocol'

const req: ApprovalRequest = {
  id: 'a', client: 'claude-code', server: 'box', command: 'ls', description: '', sudo: false, timeoutSec: 60, receivedAt: '2024-01-01T00:00:00Z',
}

describe('Item', () => {
  it('makes Allow and Send to tab keyboard-unreachable; only Deny is a submit button', () => {
    const [item] = seed([req], 0)
    const html = renderToStaticMarkup(createElement(Item, {
      item, now: 10_000, server: undefined,
      onDecide: async () => {}, onSendToTab: async () => {},
    }))
    const buttons = [...html.matchAll(/<button([^>]*)>([^<]*)<\/button>/g)].map((m) => ({ attrs: m[1], text: m[2] }))

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
})
