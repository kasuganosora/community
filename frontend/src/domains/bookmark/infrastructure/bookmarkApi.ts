import client from '@/shared/api/client'
import type { MastodonStatus } from '@/shared/types/mastodon'

export const bookmarkApi = {
  async list(params?: { max_id?: string; limit?: number }): Promise<MastodonStatus[]> {
    const { data } = await client.get<MastodonStatus[]>('/api/v1/bookmarks', { params })
    return data
  },
}
