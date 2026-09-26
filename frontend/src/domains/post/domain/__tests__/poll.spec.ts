import { describe, it, expect } from 'vitest'
import { pollOptionPercent, pollShowsResults } from '../poll'

describe('poll helpers', () => {
  it('computes rounded percentages without dividing by zero', () => {
    expect(pollOptionPercent(1, 3)).toBe(33)
    expect(pollOptionPercent(2, 3)).toBe(67)
    expect(pollOptionPercent(0, 0)).toBe(0)
    expect(pollOptionPercent(null, 10)).toBe(0)
  })
  it('shows results after voting, after expiry, or for the author', () => {
    expect(pollShowsResults({ voted: false, expired: false })).toBe(false)
    expect(pollShowsResults({ voted: true, expired: false })).toBe(true)
    expect(pollShowsResults({ voted: false, expired: true })).toBe(true)
    expect(pollShowsResults({ voted: false, expired: false }, true)).toBe(true)
  })
})
