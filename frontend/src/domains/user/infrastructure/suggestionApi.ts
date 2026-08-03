import client from '@/shared/api/client'
import type { MastodonSuggestion } from '@/shared/types/mastodon'
import type { PaginationParams } from '@/shared/types/pagination'

export const suggestionApi = {
  async list(params?: Pick<PaginationParams, 'limit'>): Promise<MastodonSuggestion[]> {
    const { data } = await client.get<MastodonSuggestion[]>('/api/v1/suggestions', { params })
    return data
  },

  async remove(accountId: string): Promise<void> {
    await client.delete(`/api/v1/suggestions/${encodeURIComponent(accountId)}`)
  },
}
