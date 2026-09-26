export type ProfileTabValue = 'posts' | 'replies' | 'media' | 'likes'

export interface ProfileTab {
  value: ProfileTabValue
  label: string
  /** Only shown on your own profile (the API only exposes your own likes). */
  ownOnly?: boolean
}

export const PROFILE_TABS: ProfileTab[] = [
  { value: 'posts', label: '帖子' },
  { value: 'replies', label: '帖子和回复' },
  { value: 'media', label: '媒体' },
  { value: 'likes', label: '喜欢', ownOnly: true },
]
