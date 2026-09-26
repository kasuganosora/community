import type { MastodonConversation } from '@/shared/types/mastodon'

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
