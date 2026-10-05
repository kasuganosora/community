import axios from 'axios'
import client from '@/shared/api/client'
import type { MastodonStatus } from '@/shared/types/mastodon'
import type { TimelineCursor } from '../domain/entities'

export const timelineApi = {
  async getHomeTimeline(cursor?: TimelineCursor): Promise<MastodonStatus[]> {
    const { data } = await client.get<MastodonStatus[]>('/api/v1/timelines/home', { params: cursor })
    return data
  },

  async getPublicTimeline(cursor?: TimelineCursor & { local?: boolean }): Promise<MastodonStatus[]> {
    const { data } = await client.get<MastodonStatus[]>('/api/v1/timelines/public', { params: cursor })
    return data
  },

  async getTagTimeline(tag: string, cursor?: TimelineCursor): Promise<MastodonStatus[]> {
    try {
      const { data } = await client.get<MastodonStatus[]>(`/api/v1/timelines/tag/${encodeURIComponent(tag)}`, { params: cursor })
      return data
    } catch (error) {
      // A hashtag nobody has used is not a broken page.
      if (axios.isAxiosError(error) && error.response?.status === 404) return []
      throw error
    }
  },
}
