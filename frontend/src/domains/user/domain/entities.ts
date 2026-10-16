import { t } from '@/shared/i18n'
export type ProfileTabValue = 'posts' | 'replies' | 'media' | 'likes'

export interface ProfileTab {
  value: ProfileTabValue
  label: string
  /** Only shown on your own profile (the API only exposes your own likes). */
  ownOnly?: boolean
}

export const PROFILE_TABS: ProfileTab[] = [
  { value: 'posts', label: t('user.entities.s1') },
  { value: 'replies', label: t('user.entities.s2') },
  { value: 'media', label: t('user.entities.s3') },
  { value: 'likes', label: t('user.entities.s4'), ownOnly: true },
]
