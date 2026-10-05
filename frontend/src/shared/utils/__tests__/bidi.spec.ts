import { describe, it, expect } from 'vitest'
import { markBidiNumbers, markBidiNumbersHtml, stripBidiMarks } from '../bidi'

const LRM = '\u200e'

describe('markBidiNumbers', () => {
  it('puts a left-to-right mark before a number that follows Arabic', () => {
    expect(markBidiNumbers('مرحبا 3:30')).toBe(`مرحبا ${LRM}3:30`)
  })

  it('does not mark twice', () => {
    expect(markBidiNumbers(`مرحبا ${LRM}3:30`)).toBe(`مرحبا ${LRM}3:30`)
  })

  it('leaves text without digits unchanged', () => {
    expect(markBidiNumbers('مرحبا')).toBe('مرحبا')
  })

  it('strips the marks back out for posting', () => {
    expect(stripBidiMarks(markBidiNumbers('مرحبا 3:30'))).toBe('مرحبا 3:30')
  })
})

describe('markBidiNumbersHtml', () => {
  it('marks body text but not the link or the code', () => {
    const html = '<p>Hello مرحبا <a href="/tags/وسم">#وسم</a> 3:30 <a href="https://example.com/3">https://example.com/3</a> <code>v1</code></p>'
    const out = markBidiNumbersHtml(html)
    expect(out).toContain(`${LRM}3:30`)
    expect(out).toContain('>https://example.com/3<')
    expect(out).toContain('>v1<')
    expect(out).not.toContain(`${LRM}https`)
  })
})
