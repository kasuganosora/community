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

describe('localHosts', () => {
  it('collects page, API and dev proxy hosts', async () => {
    const { localHosts } = await import('../env')
    expect(localHosts({ VITE_API_URL: 'https://api.example', VITE_DEV_PROXY_TARGET: 'http://localhost:8081' }, 'localhost:5174'))
      .toEqual(['localhost:5174', 'api.example', 'localhost:8081'])
    expect(localHosts({ VITE_API_URL: 'not a url' }, 'x.test')).toEqual(['x.test'])
  })
})
