import { describe, it, expect } from 'vitest'
import { formatTimeLeft } from '../format'

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
