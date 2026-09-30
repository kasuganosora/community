import type { MastodonConversation } from '@/shared/types/mastodon'
import { stripHtml } from '@/shared/utils/sanitize'

/** "Alice、Bob" — participants other than you; falls back to the last status author. */
export function conversationTitle(c: MastodonConversation, myId?: string): string {
  const others = c.accounts.filter(a => a.id !== myId)
  const people = others.length ? others : (c.last_status ? [c.last_status.account] : [])
  if (!people.length) return '未知会话'
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
  if (!s) return '（没有可显示的消息）'
  const spoiler = s.spoiler_text?.trim()
  if (spoiler) return `内容警告：${spoiler}`
  const text = stripHtml(s.content || '').replace(/\s+/g, ' ').trim()
  return text || '（没有可显示的消息）'
}
