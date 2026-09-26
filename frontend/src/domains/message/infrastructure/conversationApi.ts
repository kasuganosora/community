import client from '@/shared/api/client'
import type { MastodonConversation } from '@/shared/types/mastodon'
import type { PaginationParams } from '@/shared/types/pagination'

export const conversationApi = {
  async list(params?: Pick<PaginationParams, 'max_id' | 'limit'>): Promise<MastodonConversation[]> {
    const { data } = await client.get<MastodonConversation[]>('/api/v1/conversations', { params })
    return data
  },

  async markRead(id: string): Promise<void> {
    await client.post(`/api/v1/conversations/${encodeURIComponent(id)}/read`)
  },

  async remove(id: string): Promise<void> {
    await client.delete(`/api/v1/conversations/${encodeURIComponent(id)}`)
  },
}
