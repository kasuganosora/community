import client from '@/shared/api/client'
import type { MastodonStatus, MastodonContext } from '@/shared/types/mastodon'
import type { CreateStatusParams } from '../domain/entities'

export const postApi = {
  async create(params: CreateStatusParams): Promise<MastodonStatus> {
    const { data } = await client.post<MastodonStatus>('/api/v1/statuses', params)
    return data
  },

  async getStatus(id: string): Promise<MastodonStatus> {
    const { data } = await client.get<MastodonStatus>(`/api/v1/statuses/${id}`)
    return data
  },

  async getContext(id: string): Promise<MastodonContext> {
    const { data } = await client.get<MastodonContext>(`/api/v1/statuses/${id}/context`)
    return data
  },

  async reblog(id: string): Promise<MastodonStatus> {
    const { data } = await client.post<MastodonStatus>(`/api/v1/statuses/${id}/reblog`)
    return data
  },

  async unreblog(id: string): Promise<MastodonStatus> {
    const { data } = await client.post<MastodonStatus>(`/api/v1/statuses/${id}/unreblog`)
    return data
  },

  async favourite(id: string): Promise<MastodonStatus> {
    const { data } = await client.post<MastodonStatus>(`/api/v1/statuses/${id}/favourite`)
    return data
  },

  async unfavourite(id: string): Promise<MastodonStatus> {
    const { data } = await client.post<MastodonStatus>(`/api/v1/statuses/${id}/unfavourite`)
    return data
  },

  async bookmark(id: string): Promise<MastodonStatus> {
    const { data } = await client.post<MastodonStatus>(`/api/v1/statuses/${id}/bookmark`)
    return data
  },

  async unbookmark(id: string): Promise<MastodonStatus> {
    const { data } = await client.post<MastodonStatus>(`/api/v1/statuses/${id}/unbookmark`)
    return data
  },

  async deleteStatus(id: string): Promise<void> {
    await client.delete(`/api/v1/statuses/${id}`)
  },
}
