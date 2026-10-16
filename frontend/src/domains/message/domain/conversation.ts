import type { MastodonConversation } from '@/shared/types/mastodon'
import { stripHtml } from '@/shared/utils/sanitize'

import { t } from '@/shared/i18n'
/** "Alice、Bob" — participants other than you; falls back to the last status author. */
export function conversationTitle(c: MastodonConversation, myId?: string): string {
  const others = c.accounts.filter(a => a.id !== myId)
  const people = others.length ? others : (c.last_status ? [c.last_status.account] : [])
  if (!people.length) return t('message.conversation.s1')
  return people.map(a => a.display_name || a.username).join('、')
}

/** Route to open a conversation: its last status thread, if known. */
export function conversationTarget(c: MastodonConversation): string | null {
  const s = c.last_status
  if (!s) return null
  return `/@${s.account.acct || s.account.username}/status/${s.id}`
}

/** One-line preview. A content warning replaces the body. */
export function conversationPreview(c: MastodonConversation): string {
  const s = c.last_status
  if (!s) return t('message.conversation.s2')
  const spoiler = s.spoiler_text?.trim()
  if (spoiler) return t('message.conversation.s3', [spoiler])
  const text = stripHtml(s.content || '').replace(/\s+/g, ' ').trim()
  return text || (s.media_attachments?.length ? '' : t('message.conversation.s2'))
}

/** Short label for the last message's attachments, kept separate from the text preview. */
export function conversationMediaLabel(c: MastodonConversation): string | null {
  const media = c.last_status?.media_attachments ?? []
  if (!media.length) return null
  const images = media.filter(item => item.type === 'image').length
  const rest = media.length - images
  if (images && !rest) return images === 1 ? t('message.conversation.s4') : t('message.conversation.s5', [images])
  if (!images) return t('message.conversation.s6', [media.length])
  return t('message.conversation.s7', [images, rest])
}

export function conversationImage(c: MastodonConversation): string | null {
  const image = c.last_status?.media_attachments?.find(item => item.type === 'image')
  const raw = image?.preview_url || image?.url
  if (!raw) return null
  try {
    const url = new URL(raw, 'http://local.invalid')
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) return null
    return raw
  } catch {
    return null
  }
}
