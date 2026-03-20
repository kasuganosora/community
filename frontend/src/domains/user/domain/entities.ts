export type ProfileTabValue = 'posts' | 'replies' | 'media' | 'likes'

export interface ProfileTab {
  value: ProfileTabValue
  label: string
}

export const PROFILE_TABS: ProfileTab[] = [
  { value: 'posts', label: 'Posts' },
  { value: 'replies', label: 'Replies' },
  { value: 'media', label: 'Media' },
  { value: 'likes', label: 'Likes' },
]
