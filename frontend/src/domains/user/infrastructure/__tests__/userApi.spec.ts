import { afterEach, describe, expect, it, vi } from 'vitest'
import client, { tokenService } from '@/shared/api/client'
import { userApi } from '../userApi'

afterEach(() => {
  vi.restoreAllMocks()
  tokenService.clear()
})

function notFound() {
  return Object.assign(new Error('请求的资源不存在'), { response: { status: 404 } })
}

describe('profile lookup', () => {
  it('uses the exact accounts/lookup endpoint for remote accts', async () => {
    const account = { id: '2', username: 'luna', acct: 'luna@maid.lat' }
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: account })
    expect(await userApi.lookupAccount('@luna@maid.lat')).toEqual(account)
    expect(get).toHaveBeenCalledExactlyOnceWith('/api/v1/accounts/lookup', { params: { acct: 'luna@maid.lat' } })
  })

  it('looks up local profiles by bare username', async () => {
    const account = { id: '1', username: 'luna', acct: 'luna' }
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: account })
    expect(await userApi.lookupAccount('luna')).toEqual(account)
    expect(get).toHaveBeenCalledExactlyOnceWith('/api/v1/accounts/lookup', { params: { acct: 'luna' } })
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
