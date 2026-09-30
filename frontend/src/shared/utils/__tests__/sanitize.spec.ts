import { describe, expect, it } from 'vitest'
import { stripHtml } from '../sanitize'

describe('stripHtml', () => {
  it('keeps line breaks from br and paragraphs', () => {
    expect(stripHtml('line1<br>line2')).toBe('line1\nline2')
    expect(stripHtml('<p>hello</p><p>world</p>')).toBe('hello\nworld')
  })

  it('drops tags without gluing inline text', () => {
    expect(stripHtml('<b>bold</b> and <i>italic</i>')).toBe('bold and italic')
  })
})
