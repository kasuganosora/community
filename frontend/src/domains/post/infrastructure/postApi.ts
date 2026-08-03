import client from '@/shared/api/client'
import type { MastodonStatus, MastodonContext } from '@/shared/types/mastodon'
import type { CreateStatusParams } from '../domain/entities'

/** Factory for status action endpoints that return a single MastodonStatus */
function createStatusAction(action: string) {
  return async (id: string): Promise<MastodonStatus> => {
    const { data } = await client.post<MastodonStatus>(`/api/v1/statuses/${encodeURIComponent(id)}/${action}`)
    return data
  }
}

export const postApi = {
  async create(params: CreateStatusParams): Promise<MastodonStatus> {
    const { data } = await client.post<MastodonStatus>('/api/v1/statuses', params)
    return data
  },

  async getStatus(id: string): Promise<MastodonStatus> {
    const { data } = await client.get<MastodonStatus>(`/api/v1/statuses/${encodeURIComponent(id)}`)
    return data
  },

  async getContext(id: string): Promise<MastodonContext> {
    const { data } = await client.get<MastodonContext>(`/api/v1/statuses/${encodeURIComponent(id)}/context`)
    return data
  },

  reblog: createStatusAction('reblog'),
  unreblog: createStatusAction('unreblog'),
  favourite: createStatusAction('favourite'),
  unfavourite: createStatusAction('unfavourite'),
  bookmark: createStatusAction('bookmark'),
  unbookmark: createStatusAction('unbookmark'),

  async deleteStatus(id: string): Promise<void> {
    await client.delete(`/api/v1/statuses/${encodeURIComponent(id)}`)
  },
}
