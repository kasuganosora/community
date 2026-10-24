import axios from 'axios'
import client from '@/shared/api/client'
import { INTERACTIVE_READ_TIMEOUT_MS, settleWithin } from '@/shared/api/requestDeadline'
import type { MastodonStatus } from '@/shared/types/mastodon'
import type { TimelineCursor } from '../domain/entities'

function readTimeline<T>(path: string, params: object | undefined): Promise<T> {
  return settleWithin(signal => client.get<T>(path, { params, signal, timeout: INTERACTIVE_READ_TIMEOUT_MS }).then(response => response.data))
}

export const timelineApi = {
  getHomeTimeline(cursor?: TimelineCursor): Promise<MastodonStatus[]> {
    return readTimeline('/api/v1/timelines/home', cursor)
  },

  getPublicTimeline(cursor?: TimelineCursor & { local?: boolean }): Promise<MastodonStatus[]> {
    return readTimeline('/api/v1/timelines/public', cursor)
  },

  async getTagTimeline(tag: string, cursor?: TimelineCursor): Promise<MastodonStatus[]> {
    try {
      return await readTimeline(`/api/v1/timelines/tag/${encodeURIComponent(tag)}`, cursor)
    } catch (error) {
      // A hashtag nobody has used is not a broken page.
      if (axios.isAxiosError(error) && error.response?.status === 404) return []
      throw error
    }
  },
}
