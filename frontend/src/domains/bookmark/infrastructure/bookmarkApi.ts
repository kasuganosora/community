import { cursorPage, type CursorPage } from '@/shared/utils/pagination'
import client from '@/shared/api/client'
import type { MastodonStatus } from '@/shared/types/mastodon'
import type { PaginationParams } from '@/shared/types/pagination'

export const bookmarkApi = {
  async list(params?: Pick<PaginationParams, 'max_id' | 'limit'>): Promise<CursorPage<MastodonStatus>> {
    const { data, headers } = await client.get<MastodonStatus[]>('/api/v1/bookmarks', { params })
    return cursorPage(data, headers.link)
  },
}
