import { afterEach, describe, expect, it, vi } from 'vitest'
import client from '@/shared/api/client'
import { appealApi } from '../appealApi'

afterEach(() => vi.restoreAllMocks())

describe('appeal API client', () => {
  it('lists and creates the signed-in account appeal', async () => {
    const row = { id: '3', account_id: '9', status_id: '15', text: 'a mistake', state: 'open', created_at: '2026-01-01T00:00:00Z' }
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: [row] })
    const post = vi.spyOn(client, 'post').mockResolvedValue({ data: row })
    expect(await appealApi.list()).toEqual([row])
    expect(get).toHaveBeenCalledWith('/api/v1/appeals')
    expect(await appealApi.create('a mistake')).toEqual(row)
    expect(await appealApi.create('a mistake', '15')).toEqual(row)
    expect(post.mock.calls).toEqual([
      ['/api/v1/appeals', { text: 'a mistake' }],
      ['/api/v1/appeals', { text: 'a mistake', status_id: '15' }],
    ])
  })
})
