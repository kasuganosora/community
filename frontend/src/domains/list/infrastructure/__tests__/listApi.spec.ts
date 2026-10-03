import { describe, expect, it, vi } from 'vitest'
import client from '@/shared/api/client'
import { listApi } from '../listApi'

describe('list membership reads', () => {
  it('does not truncate the member list at eighty accounts', async () => {
    const accounts = Array.from({ length: 81 }, (_, i) => ({ id: String(i) }))
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: accounts })
    expect(await listApi.getAccounts('7')).toHaveLength(81)
    expect(get).toHaveBeenCalledWith('/api/v1/lists/7/accounts')
  })
})
