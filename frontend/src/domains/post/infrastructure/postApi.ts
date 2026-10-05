import client from '@/shared/api/client'
import type { MastodonStatus, MastodonContext, MastodonPoll, MastodonAttachment, MastodonStatusSource, MastodonEmojiReaction } from '@/shared/types/mastodon'
import type { CreateStatusParams, ReplaceStatusParams } from '../domain/entities'

/** Factory for status action endpoints that return a single MastodonStatus */
function createStatusAction(action: string) {
  return async (id: string): Promise<MastodonStatus> => {
    const { data } = await client.post<MastodonStatus>(`/api/v1/statuses/${encodeURIComponent(id)}/${action}`)
    return data
  }
}

export const postApi = {
  async react(id: string, name: string, remove = false): Promise<MastodonStatus> {
    const path = `/api/v1/statuses/${encodeURIComponent(id)}/reactions/${encodeURIComponent(name)}`
    const { data } = remove ? await client.delete<MastodonStatus>(path) : await client.put<MastodonStatus>(path)
    return data
  },
  async reactions(id: string): Promise<MastodonEmojiReaction[]> {
    const { data } = await client.get<MastodonEmojiReaction[]>(`/api/v1/statuses/${encodeURIComponent(id)}/reactions`)
    return data
  },
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
  pin: createStatusAction('pin'),
  unpin: createStatusAction('unpin'),

  async deleteStatus(id: string): Promise<void> {
    await client.delete(`/api/v1/statuses/${encodeURIComponent(id)}`)
  },

  async replaceStatus(id: string, params: ReplaceStatusParams): Promise<MastodonStatus> {
    const { data } = await client.post<MastodonStatus>(`/api/v1/statuses/${encodeURIComponent(id)}/replace`, params)
    return data
  },

  async getSource(id: string): Promise<MastodonStatusSource> {
    const { data } = await client.get<MastodonStatusSource>(`/api/v1/statuses/${encodeURIComponent(id)}/source`)
    return data
  },

  async votePoll(pollId: string, choices: number[]): Promise<MastodonPoll> {
    const { data } = await client.post<MastodonPoll>(`/api/v1/polls/${encodeURIComponent(pollId)}/votes`, { choices })
    return data
  },

  /**
   * Upload a media file. Uses the synchronous v1 endpoint, which returns the
   * processed attachment (url/preview_url) right away; /api/v2/media returns
   * an empty url that would have to be polled.
   */
  async uploadMedia(file: File, description?: string): Promise<MastodonAttachment> {
    const form = new FormData()
    form.append('file', file)
    if (description) form.append('description', description)
    const { data } = await client.post<MastodonAttachment>('/api/v1/media', form, {
      headers: { 'Content-Type': 'multipart/form-data' },
      timeout: 120000,
    })
    return data
  },
}
