import { describe, expect, it } from 'vitest'
import { mentionAtCaret } from '../mentionQuery'

describe('mentionAtCaret', () => {
  it('finds the mention being typed', () => {
    expect(mentionAtCaret('@a', 2)).toEqual({ q: 'a', start: 0, end: 2 })
    expect(mentionAtCaret('hi @ale', 7)).toEqual({ q: 'ale', start: 3, end: 7 })
  })
  it('ignores a finished mention and a bare @', () => {
    expect(mentionAtCaret('@alice ', 7)).toBeNull()
    expect(mentionAtCaret('@', 1)).toBeNull()
    expect(mentionAtCaret('mail me', 7)).toBeNull()
  })
})
