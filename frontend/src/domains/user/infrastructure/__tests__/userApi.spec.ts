import { afterEach, describe, expect, it, vi } from 'vitest'
import client, { tokenService } from '@/shared/api/client'
import { classifyProfileError, clearCachedAccounts, isTransientAccountError, peekCachedAccount, rememberCachedAccount, userApi } from '../userApi'
import type { MastodonAccount } from '@/shared/types/mastodon'

afterEach(() => {
  vi.restoreAllMocks()
  tokenService.clear()
  clearCachedAccounts()
})

function notFound() {
  return Object.assign(new Error('请求的资源不存在'), { response: { status: 404 } })
}

describe('profile lookup', () => {
  it('uses the exact accounts/lookup endpoint for remote accts', async () => {
    const account = { id: '2', username: 'luna', acct: 'luna@maid.lat' }
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: account })
    expect(await userApi.lookupAccount('@luna@maid.lat')).toEqual(account)
    expect(get).toHaveBeenCalledExactlyOnceWith('/api/v1/accounts/lookup', expect.objectContaining({ params: { acct: 'luna@maid.lat' } }))
  })

  it('looks up local profiles by bare username', async () => {
    const account = { id: '1', username: 'luna', acct: 'luna' }
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: account })
    expect(await userApi.lookupAccount('luna')).toEqual(account)
    expect(get).toHaveBeenCalledExactlyOnceWith('/api/v1/accounts/lookup', expect.objectContaining({ params: { acct: 'luna' } }))
  })

  it('resolves an uncached remote account for a signed-in user, exact acct only', async () => {
    tokenService.set('test-token')
    const remote = { id: '9', username: 'alice', acct: 'alice@remote.example' }
    const local = { id: '1', username: 'alice', acct: 'alice' }
    const get = vi.spyOn(client, 'get')
      .mockRejectedValueOnce(notFound())
      .mockResolvedValueOnce({ data: [local, remote] })
    expect(await userApi.lookupAccount('@alice@remote.example')).toEqual(remote)
    expect(get).toHaveBeenLastCalledWith('/api/v1/accounts/search', {
      params: { q: 'alice@remote.example', limit: 5, resolve: true },
    })
  })

  it('never falls back to a same-named local account', async () => {
    tokenService.set('test-token')
    const local = { id: '1', username: 'luna', acct: 'luna' }
    vi.spyOn(client, 'get')
      .mockRejectedValueOnce(notFound())
      .mockResolvedValueOnce({ data: [local] })
    await expect(userApi.lookupAccount('luna@maid.lat')).rejects.toThrow('用户不存在')
  })

  it('keeps guest profile lookup local (no remote resolve)', async () => {
    const get = vi.spyOn(client, 'get').mockRejectedValueOnce(notFound())
    await expect(userApi.lookupAccount('alice@remote.example')).rejects.toThrow('用户不存在')
    expect(get).toHaveBeenCalledOnce()
  })

  it('propagates non-404 errors', async () => {
    vi.spyOn(client, 'get').mockRejectedValueOnce(Object.assign(new Error('boom'), { response: { status: 500 } }))
    await expect(userApi.lookupAccount('luna')).rejects.toThrow('boom')
  })

  it('gives local account lookup a longer budget than the global 15s axios timeout', async () => {
    const account = { id: '1538', username: 'kanna', acct: 'kanna' }
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: account })
    expect(await userApi.lookupAccount('kanna')).toEqual(account)
    expect(get).toHaveBeenCalledExactlyOnceWith('/api/v1/accounts/lookup', expect.objectContaining({
      params: { acct: 'kanna' },
      timeout: expect.any(Number),
    }))
    expect(get.mock.calls[0]![1]!.timeout).toBeGreaterThan(15_000)
    expect(peekCachedAccount('kanna')).toEqual(account)
    expect(peekCachedAccount('@Kanna')).toEqual(account)
  })

  it('uses that same longer budget for profile statuses', async () => {
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: [], headers: {} })
    await userApi.getStatuses('1538', { limit: 20 })
    expect(get.mock.calls[0]![1]!.timeout).toBeGreaterThan(15_000)
  })

  it('does not hide 410 or drop a timeout into the missing-user path', async () => {
    const get = vi.spyOn(client, 'get')
    get.mockRejectedValueOnce(Object.assign(new Error('gone'), { response: { status: 410 } }))
    await expect(userApi.lookupAccount('kanna')).rejects.toMatchObject({ response: { status: 410 } })
    expect(get).toHaveBeenCalledOnce()

    get.mockRejectedValueOnce(Object.assign(new Error('timeout of 15000ms exceeded'), { code: 'ECONNABORTED' }))
    await expect(userApi.lookupAccount('kanna')).rejects.toMatchObject({ code: 'ECONNABORTED' })
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('does not resolve a suspended node, and keeps authorization failures distinct', async () => {
    tokenService.set('test-token')
    const get = vi.spyOn(client, 'get')
    get.mockRejectedValueOnce(Object.assign(new Error('missing'), { response: { status: 404, data: { error: 'Record not found', availability: { reason: 'domain_suspended' } } } }))
    await expect(userApi.lookupAccount('ada@blocked.example')).rejects.toThrow('该节点已被本站封禁')
    expect(get).toHaveBeenCalledOnce()

    get.mockResolvedValueOnce({ data: { id: '3', username: 'banned', acct: 'banned', display_name: 'Old name', note: 'old bio', availability: { reason: 'account_suspended' }, suspended: true } })
    const banned = await userApi.lookupAccount('banned')
    expect(banned.availability?.reason).toBe('account_suspended')
    expect(peekCachedAccount('banned')?.availability?.reason).toBe('account_suspended')

    get.mockRejectedValueOnce(Object.assign(new Error('Request failed with status code 401'), { response: { status: 401 } }))
    await expect(userApi.lookupAccount('ada')).rejects.toThrow('登录已过期，请重新登录')
    get.mockRejectedValueOnce(Object.assign(new Error('Request failed with status code 403'), { response: { status: 403 } }))
    await expect(userApi.lookupAccount('ada')).rejects.toThrow('没有权限执行此操作')
    expect(classifyProfileError(Object.assign(new Error('avatar'), { response: { status: 403 } }))).toBe('forbidden')
    expect(classifyProfileError(Object.assign(new Error('unsigned'), { response: { status: 401 } }))).toBe('unauthorized')
    expect(classifyProfileError(Object.assign(new Error('timeout of 15000ms exceeded'), { code: 'ECONNABORTED' }))).toBe('timeout')
    expect(classifyProfileError(Object.assign(new Error('network'), { code: 'ERR_NETWORK' }))).toBe('fetch_failed')
    expect(classifyProfileError(Object.assign(new Error('missing'), { response: { status: 404 } }))).toBe('not_found')
    for (const reason of ['unauthorized', 'forbidden', 'timeout', 'fetch_failed', 'not_found']) {
      expect(reason).not.toBe('account_suspended')
      expect(reason).not.toBe('domain_suspended')
    }
  })

  it('forgets a cached local account on 404 and 410', async () => {
    const kanna = { id: '1538', username: 'kanna', acct: 'kanna' } as MastodonAccount
    rememberCachedAccount(kanna, 'kanna')
    vi.spyOn(client, 'get').mockRejectedValueOnce(notFound())
    await expect(userApi.lookupAccount('kanna')).rejects.toThrow('用户不存在')
    expect(peekCachedAccount('kanna')).toBeUndefined()

    rememberCachedAccount(kanna, 'kanna')
    vi.spyOn(client, 'get').mockRejectedValueOnce(Object.assign(new Error('gone'), { response: { status: 410 } }))
    await expect(userApi.lookupAccount('kanna')).rejects.toMatchObject({ response: { status: 410 } })
    expect(peekCachedAccount('kanna')).toBeUndefined()
  })
})

describe('transient account errors', () => {
  it('retries timeouts and server failures without hiding 404 or 410', () => {
    expect(isTransientAccountError(Object.assign(new Error('timeout of 15000ms exceeded'), { code: 'ECONNABORTED' }))).toBe(true)
    expect(isTransientAccountError(Object.assign(new Error('加载超时，点重试'), { code: 'ETIMEDOUT' }))).toBe(true)
    expect(isTransientAccountError(Object.assign(new Error('offline'), { code: 'ERR_NETWORK' }))).toBe(true)
    expect(isTransientAccountError(Object.assign(new Error('busy'), { response: { status: 503 } }))).toBe(true)
    expect(isTransientAccountError(Object.assign(new Error('missing'), { response: { status: 404 } }))).toBe(false)
    expect(isTransientAccountError(Object.assign(new Error('gone'), { response: { status: 410 } }))).toBe(false)
    expect(isTransientAccountError(Object.assign(new Error('nope'), { response: { status: 403 } }))).toBe(false)
  })
})

describe('all followed accounts for list candidates', () => {
  it('reads beyond eighty accounts using relationship ids from Link', async () => {
    const accounts = Array.from({ length: 80 }, (_, i) => ({ id: String(1000 + i) }))
    const get = vi.spyOn(client, 'get')
      .mockResolvedValueOnce({ data: accounts, headers: { link: '</api/v1/accounts/me/following?max_id=9&limit=80>; rel="next"' } })
      .mockResolvedValueOnce({ data: [{ id: '2000' }], headers: {} })
    expect(await userApi.getAllFollowing('me')).toHaveLength(81)
    expect(get).toHaveBeenLastCalledWith('/api/v1/accounts/me/following', { params: { limit: 80, max_id: '9' } })
  })
})
