import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { StoreErrorBanner } from '../src/renderer/StoreError'

describe('StoreErrorBanner', () => {
  it('renders the message as a non-dismissable alert only while one is set', () => {
    const html = renderToStaticMarkup(createElement(StoreErrorBanner, { message: 'the vault file failed its integrity check' }))
    expect(html).toContain('role="alert"')
    expect(html).toContain('the vault file failed its integrity check')
    expect(html).not.toContain('<button')
    expect(renderToStaticMarkup(createElement(StoreErrorBanner, { message: undefined }))).toBe('')
    expect(renderToStaticMarkup(createElement(StoreErrorBanner, { message: '' }))).toBe('')
  })
})
