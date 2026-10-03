import { describe, expect, it } from 'vitest'
import { cursorPage } from '../pagination'

describe('pagination Link headers', () => {
  it('extracts next max_id from relative links alongside a prev link', () => {
    expect(cursorPage([{ id: 'status-id' }], '</api/v1/bookmarks?min_id=10>; rel="prev", </api/v1/bookmarks?limit=40&max_id=42>; rel="next"')).toEqual({ items: [{ id: 'status-id' }], nextCursor: '42' })
  })
  it('treats absent next links as the last page', () => {
    expect(cursorPage([1])).toEqual({ items: [1] })
    expect(cursorPage([1], '</api/v1/bookmarks?min_id=10>; rel="prev"')).toEqual({ items: [1] })
  })
})
