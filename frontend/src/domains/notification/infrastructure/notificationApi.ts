import client from '@/shared/api/client'
import type { MastodonNotification } from '@/shared/types/mastodon'
import type { PaginationParams } from '@/shared/types/pagination'

export const notificationApi = {
  async list(params?: Pick<PaginationParams, 'max_id' | 'limit'> & { 'types[]'?: string[] }): Promise<MastodonNotification[]> {
    const { data } = await client.get<MastodonNotification[]>('/api/v1/notifications', { params })
    return data
  },

  async dismiss(id: string): Promise<void> {
    await client.post(`/api/v1/notifications/${encodeURIComponent(id)}/dismiss`)
  },

  async clear(): Promise<void> {
    await client.post('/api/v1/notifications/clear')
  },
}
