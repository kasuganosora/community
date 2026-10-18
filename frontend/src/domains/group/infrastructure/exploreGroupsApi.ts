import client from '@/shared/api/client'

export interface ExploreGroup {
  id: string; name: string; topic: string; alias: string; domain: string; local: boolean
  member_count: number; target: string; via: string[]; membership: string; join_rule: string
  avatar_url?: string; last_post_at: string | null; created_at: string
}
export interface ExploreGroupPage { groups: ExploreGroup[]; next: string | null }

/** Extracts the cursor of the Link rel="next" header. */
export function nextCursor(link: string | undefined): string | null {
  const match = link?.match(/<([^>]+)>;\s*rel="next"/)
  if (!match) return null
  try { return new URL(match[1]!, window.location.origin).searchParams.get('cursor') } catch { return null }
}

export const exploreGroupsApi = {
  async list(cursor?: string | null, signal?: AbortSignal): Promise<ExploreGroupPage> {
    const res = await client.get<ExploreGroup[]>('/api/v1/explore/groups', { params: { limit: 20, ...(cursor ? { cursor } : {}) }, signal })
    return { groups: res.data, next: nextCursor(res.headers?.link as string | undefined) }
  },
}
