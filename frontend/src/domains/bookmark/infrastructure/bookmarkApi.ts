import client from '@/shared/api/client'
import type { MastodonStatus } from '@/shared/types/mastodon'
import type { PaginationParams } from '@/shared/types/pagination'

export const bookmarkApi = {
  async list(params?: Pick<PaginationParams, 'max_id' | 'limit'>): Promise<MastodonStatus[]> {
    const { data } = await client.get<MastodonStatus[]>('/api/v1/bookmarks', { params })
    return data
  },
}
