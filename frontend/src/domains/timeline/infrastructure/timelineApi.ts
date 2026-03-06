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
    const { data } = await client.get<MastodonStatus[]>(`/api/v1/timelines/tag/${tag}`, { params: cursor })
    return data
  },
}
