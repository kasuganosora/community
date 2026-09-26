import { describe, it, expect } from 'vitest'
import { isMockEnabled } from '../env'

describe('isMockEnabled', () => {
  it('is off unless explicitly requested in dev', () => {
    expect(isMockEnabled({ DEV: true })).toBe(false)
    expect(isMockEnabled({ DEV: true, VITE_USE_MOCK: 'false' })).toBe(false)
    expect(isMockEnabled({ DEV: true, VITE_USE_MOCK: 'true' })).toBe(true)
  })

  it('is never on in production builds', () => {
    expect(isMockEnabled({ DEV: false, VITE_USE_MOCK: 'true' })).toBe(false)
  })
})
