import { describe, it, expect } from 'vitest'
import { formatTimeLeft, wasEdited } from '../format'

describe('formatTimeLeft', () => {
  const now = Date.parse('2026-09-26T10:00:00Z')
  it('formats the remaining time compactly', () => {
    expect(formatTimeLeft('2026-09-26T10:00:30Z', now)).toBe('30s')
    expect(formatTimeLeft('2026-09-26T10:05:00Z', now)).toBe('5m')
    expect(formatTimeLeft('2026-09-26T13:00:00Z', now)).toBe('3h')
    expect(formatTimeLeft('2026-09-29T10:00:00Z', now)).toBe('3d')
  })
  it('clamps past dates and rejects garbage', () => {
    expect(formatTimeLeft('2026-09-26T09:00:00Z', now)).toBe('0s')
    expect(formatTimeLeft('nope', now)).toBe('')
  })
})

describe('wasEdited', () => {
  it('only reports edits made after the post was created', () => {
    expect(wasEdited({ created_at: '2026-10-01T00:00:00Z', edited_at: null })).toBe(false)
    expect(wasEdited({ created_at: '2026-10-01T00:00:00Z' })).toBe(false)
    expect(wasEdited({ created_at: '2026-10-01T00:00:00Z', edited_at: '2026-10-01T00:00:00.400Z' })).toBe(false)
    expect(wasEdited({ created_at: '2026-10-01T00:00:00Z', edited_at: '2026-09-30T00:00:00Z' })).toBe(false)
    expect(wasEdited({ created_at: '2026-10-01T00:00:00Z', edited_at: '2026-10-01T00:05:00Z' })).toBe(true)
  })
})
