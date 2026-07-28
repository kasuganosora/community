import client from '@/shared/api/client'
import type { MastodonNotification } from '@/shared/types/mastodon'

export const notificationApi = {
  async list(params?: { max_id?: string; limit?: number }): Promise<MastodonNotification[]> {
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
