import { afterEach, describe, expect, it, vi } from 'vitest'
import { beginLocalSignOut, clearAppealSession, endLocalSignOut, isAppealSession } from '@/shared/api/appealSession'
import { isAppealApiPath, isCredentialRequest, retainSuspendedAppealSession, tokenService } from '@/shared/api/client'

afterEach(() => {
  clearAppealSession()
  endLocalSignOut()
  tokenService.clear()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('suspended appeal session', () => {
  it('keeps the bearer when the appeal route still answers', async () => {
    tokenService.set('tok')
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response('[]', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    expect(await retainSuspendedAppealSession('/api/v1/accounts/verify_credentials', 'tok')).toBe(true)
    expect(isAppealSession()).toBe(true)
    expect(fetchMock).toHaveBeenCalledOnce()
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/appeals')
    expect(new Headers(fetchMock.mock.calls[0]?.[1]?.headers).get('Authorization')).toBe('Bearer tok')
    expect(await retainSuspendedAppealSession('/api/v1/timelines/home', 'tok')).toBe(true)
    expect(fetchMock).toHaveBeenCalledOnce()
  })

  it('drops a dead token and ignores sign-in failures', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"error":"unauthorized"}', { status: 401 })))
    expect(await retainSuspendedAppealSession('/api/v1/accounts/verify_credentials', 'dead')).toBe(false)
    expect(isAppealSession()).toBe(false)
    expect(await retainSuspendedAppealSession('/api/v1/accounts/login', 'dead')).toBe(false)
    expect(await retainSuspendedAppealSession('/api/v1/appeals', 'dead')).toBe(false)
    expect(isAppealApiPath('/api/v2/appeals?limit=1')).toBe(true)
    expect(isCredentialRequest('/api/v1/accounts/login/two_factor')).toBe(true)
    beginLocalSignOut()
    expect(await retainSuspendedAppealSession('/api/v1/accounts/verify_credentials', 'tok')).toBe(false)
  })
})
