import { afterEach, describe, expect, it, vi } from 'vitest'
import client from '@/shared/api/client'
import { appealAdminApi } from '../appealAdminApi'

afterEach(() => vi.restoreAllMocks())

describe('admin appeal API client', () => {
  it('lists by state and resolves without unsuspending', async () => {
    const row = { id: '3', account_id: '9', status_id: '0', text: 'please', state: 'accepted', created_at: '2026-01-01T00:00:00Z', admin_note: 'ok' }
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: [row] })
    const post = vi.spyOn(client, 'post').mockResolvedValue({ data: row })
    expect(await appealAdminApi.list({ state: 'open', limit: 40, offset: 0 })).toEqual([row])
    expect(get).toHaveBeenCalledWith('/api/v1/admin/appeals', { params: { state: 'open', limit: 40, offset: 0 } })
    await appealAdminApi.list({ state: '', limit: 40, offset: 40 })
    expect(get).toHaveBeenLastCalledWith('/api/v1/admin/appeals', { params: { limit: 40, offset: 40 } })
    expect(await appealAdminApi.resolve('3', false, 'no')).toEqual(row)
    expect(post).toHaveBeenCalledWith('/api/v1/admin/appeals/3/resolve', { accept: false, note: 'no' })
  })
})
