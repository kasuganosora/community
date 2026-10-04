import { describe, expect, it } from 'vitest'
import { isServerAuthReturn, safeInternalPath } from '../authReturn'

describe('auth return paths', () => {
  it('keeps same-origin paths and rejects open redirects', () => {
    expect(safeInternalPath('/oauth/authorize?client_id=1')).toBe('/oauth/authorize?client_id=1')
    expect(safeInternalPath('https://evil.example')).toBeNull()
    expect(safeInternalPath('//evil.example')).toBeNull()
  })

  it('recognizes Mastodon and Misskey callback handoffs', () => {
    expect(isServerAuthReturn('/oauth/authorize?response_type=code')).toBe(true)
    expect(isServerAuthReturn('/miauth/abc')).toBe(true)
    expect(isServerAuthReturn('/auth/session-token')).toBe(true)
    expect(isServerAuthReturn('/settings')).toBe(false)
  })
})
