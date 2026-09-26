import type { MastodonRelationship } from '@/shared/types/mastodon'

export type FollowState = 'unknown' | 'none' | 'following' | 'requested'

export function followStateOf(rel: MastodonRelationship | undefined): FollowState {
  if (!rel) return 'unknown'
  if (rel.following) return 'following'
  if (rel.requested) return 'requested'
  return 'none'
}

export function followButtonLabel(state: FollowState, followedBy = false): string {
  switch (state) {
    case 'following': return '已关注'
    case 'requested': return '已请求'
    default: return followedBy ? '回关' : '关注'
  }
}
