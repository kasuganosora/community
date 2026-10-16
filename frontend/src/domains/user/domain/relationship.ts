import type { MastodonRelationship } from '@/shared/types/mastodon'

import { t } from '@/shared/i18n'
export type FollowState = 'unknown' | 'none' | 'following' | 'requested'

export function followStateOf(rel: MastodonRelationship | undefined): FollowState {
  if (!rel) return 'unknown'
  if (rel.following) return 'following'
  if (rel.requested) return 'requested'
  return 'none'
}

export function followButtonLabel(state: FollowState, followedBy = false): string {
  switch (state) {
    case 'following': return t('user.relationship.s1')
    case 'requested': return t('user.relationship.s2')
    default: return followedBy ? t('user.relationship.s3') : t('user.relationship.s4')
  }
}
