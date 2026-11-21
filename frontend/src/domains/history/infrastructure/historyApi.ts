import client from '@/shared/api/client'
import type { HistoryItem } from '@/shared/types/mastodon'

export const historyApi = {
  async list(params?: { max_cursor?: string; limit?: number }): Promise<HistoryItem[]> {
    const { data } = await client.get<HistoryItem[]>('/api/v1/history', { params })
    return data
  },

  async record(statusId: string): Promise<void> {
    await client.post('/api/v1/history', { status_id: statusId })
  },

  async remove(statusId: string): Promise<void> {
    await client.delete(`/api/v1/history/${encodeURIComponent(statusId)}`)
  },

  async clear(): Promise<void> {
    await client.delete('/api/v1/history')
  },

  async getSettings(): Promise<{ paused: boolean }> {
    const { data } = await client.get<{ paused: boolean }>('/api/v1/history/settings')
    return data
  },

  async updateSettings(paused: boolean): Promise<{ paused: boolean }> {
    const { data } = await client.put<{ paused: boolean }>('/api/v1/history/settings', { paused })
    return data
  },
}
