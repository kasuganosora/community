import { t as i18nT } from '@/shared/i18n'
export const MAX_LIST_TITLE_LENGTH = 64

export function validateListTitle(title: string): string | null {
  const t = title.trim()
  if (!t) return i18nT('list.list.s1')
  if (Array.from(t).length > MAX_LIST_TITLE_LENGTH) return i18nT('list.list.s2', [MAX_LIST_TITLE_LENGTH])
  return null
}

export const REPLIES_POLICY_OPTIONS = [
  { value: 'list', label: i18nT('list.list.s3') },
  { value: 'followed', label: i18nT('list.list.s4') },
  { value: 'none', label: i18nT('list.list.s5') },
] as const

/** Matches the server's models.MaxListHashtags. */
export const MAX_LIST_TAGS = 20

/** "#Godot " -> "Godot"; returns '' for input that cannot be a hashtag. */
export function normalizeTagInput(raw: string): string {
  const name = raw.trim().replace(/^#+/, '').trim()
  return /^[\p{L}\p{N}_\p{M}·・]+$/u.test(name) ? name : ''
}
