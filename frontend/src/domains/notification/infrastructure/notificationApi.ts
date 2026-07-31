import client from '@/shared/api/client'
import type { MastodonNotification } from '@/shared/types/mastodon'
import type { PaginationParams } from '@/shared/types/pagination'

export const notificationApi = {
  async list(params?: Pick<PaginationParams, 'max_id' | 'limit'>): Promise<MastodonNotification[]> {
    const { data } = await client.get<MastodonNotification[]>('/api/v1/notifications', { params })
    return data
  },

  async dismiss(id: string): Promise<void> {
    await client.post(`/api/v1/notifications/${id}/dismiss`)
  },

  async clear(): Promise<void> {
    await client.post('/api/v1/notifications/clear')
  },
}
