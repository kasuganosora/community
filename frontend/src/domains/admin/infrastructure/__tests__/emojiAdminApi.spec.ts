import { afterEach, describe, expect, it, vi } from 'vitest'
import client from '@/shared/api/client'
import { emojiAdminApi } from '../emojiAdminApi'
afterEach(() => vi.restoreAllMocks())
describe('admin emoji search filters', () => {
  it('does not accidentally restrict an unfiltered list to empty category or domain', async () => {
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: [], headers: { 'x-total-count': '80' } })
    const result = await emojiAdminApi.list({ origin: 'remote', q: '', category: '', domain: '', page: 1, limit: 40 })
    expect(get).toHaveBeenCalledWith('/api/v1/admin/custom_emojis', { params: expect.objectContaining({ category: undefined, domain: undefined }) })
    expect(result.total).toBe(80)
  })
  it('keeps category filtering but discards a stale remote domain when switching to local', async () => {
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: [], headers: {} })
    await emojiAdminApi.list({ origin: 'local', q: 'cat', category: 'Animals', domain: 'remote.test', page: 2, limit: 40 })
    expect(get).toHaveBeenCalledWith('/api/v1/admin/custom_emojis', { params: expect.objectContaining({ category: 'Animals', domain: undefined, page: 2 }) })
  })
})
