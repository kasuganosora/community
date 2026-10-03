import { afterEach, describe, expect, it, vi } from 'vitest'
import client, { tokenService } from '@/shared/api/client'
import { userApi } from '../userApi'

afterEach(() => {
  vi.restoreAllMocks()
  tokenService.clear()
})

describe('remote profile lookup', () => {
  it('resolves an uncached remote account for a signed-in user', async () => {
    tokenService.set('test-token')
    const account = { id: '9', username: 'alice', acct: 'alice@remote.example' }
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: [account] })
    expect(await userApi.lookupAccount('@alice@remote.example')).toEqual(account)
    expect(get).toHaveBeenCalledExactlyOnceWith('/api/v1/accounts/search', {
      params: { q: 'alice@remote.example', limit: 40, resolve: true },
    })
  })

  it('keeps guest profile lookup local', async () => {
    const account = { id: '9', username: 'alice', acct: 'alice@remote.example' }
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: [account] })
    expect(await userApi.lookupAccount('alice@remote.example')).toEqual(account)
    expect(get).toHaveBeenCalledWith('/api/v1/accounts/search', {
      params: { q: 'alice@remote.example', limit: 40, resolve: false },
    })
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
