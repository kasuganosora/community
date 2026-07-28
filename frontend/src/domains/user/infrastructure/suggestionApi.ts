import client from '@/shared/api/client'
import type { MastodonAccount } from '@/shared/types/mastodon'

export const suggestionApi = {
  async list(params?: { limit?: number }): Promise<{ account: MastodonAccount }[]> {
    const { data } = await client.get<{ account: MastodonAccount }[]>('/api/v1/suggestions', { params })
    return data
  },

  async remove(accountId: string): Promise<void> {
    await client.delete(`/api/v1/suggestions/${accountId}`)
  },
}
