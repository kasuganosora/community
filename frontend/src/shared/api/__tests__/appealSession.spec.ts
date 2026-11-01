import { afterEach, describe, expect, it, vi } from 'vitest'
import { AxiosError, type AxiosAdapter } from 'axios'
import client, { setAuthExpiredCallback } from '@/shared/api/client'
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

describe('appeal session request races', () => {
  it('ignores a late probe after switching accounts', async () => {
    let finish!: (response: Response) => void
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(resolve => { finish = resolve })))
    tokenService.set('old')
    const pending = retainSuspendedAppealSession('/api/v1/accounts/verify_credentials', 'old')
    tokenService.set('new')
    finish(new Response('[]', { status: 200 }))
    expect(await pending).toBe(false)
    expect(isAppealSession()).toBe(false)
    expect(tokenService.get()).toBe('new')
  })

  it('does not reuse the previous account’s suspended state', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200 })))
    tokenService.set('suspended')
    await retainSuspendedAppealSession('/api/v1/accounts/verify_credentials', 'suspended')
    tokenService.set('normal')
    expect(isAppealSession()).toBe(false)
    expect(await retainSuspendedAppealSession('/api/v1/accounts/verify_credentials', 'suspended')).toBe(false)
  })

  it('propagates a temporary probe failure instead of declaring the token dead', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 503 })))
    tokenService.set('valid')
    await expect(retainSuspendedAppealSession('/api/v1/accounts/verify_credentials', 'valid')).rejects.toThrow()
    expect(tokenService.get()).toBe('valid')
    expect(isAppealSession()).toBe(false)
  })
})

const unauthorizedAdapter: AxiosAdapter = async config => {
  throw new AxiosError('unauthorized', 'ERR_BAD_REQUEST', config, undefined, { status: 401, statusText: 'Unauthorized', headers: {}, config, data: { error: 'unauthorized' } })
}

describe('appeal detection in the response interceptor', () => {
  it('does not log out the active account for another account’s failed request', async () => {
    const expired = vi.fn()
    setAuthExpiredCallback(expired)
    tokenService.set('active')
    await expect(client.get('/api/v1/accounts/verify_credentials', { adapter: unauthorizedAdapter, headers: { Authorization: 'Bearer previous' } })).rejects.toThrow()
    expect(expired).not.toHaveBeenCalled()
    expect(tokenService.get()).toBe('active')
  })
  it('keeps the active session when the appeal probe cannot reach the server', async () => {
    const expired = vi.fn()
    setAuthExpiredCallback(expired)
    tokenService.set('active')
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 503 })))
    await expect(client.get('/api/v1/accounts/verify_credentials', { adapter: unauthorizedAdapter })).rejects.toMatchObject({ code: 'ERR_NETWORK' })
    expect(expired).not.toHaveBeenCalled()
    expect(tokenService.get()).toBe('active')
  })
})
