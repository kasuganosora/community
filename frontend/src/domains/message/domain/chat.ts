import type { MastodonAccount, MastodonStatus } from '@/shared/types/mastodon'

import { t } from '@/shared/i18n'
export interface ChatMemberInput { account_id?: string; matrix_id?: string }
export interface ChatMember {
  id: string
  kind: 'ap' | 'local' | 'matrix'
  account_id: string
  matrix_id: string
  acct?: string
  display_name?: string
  active?: boolean
  /** The viewer's own membership. */
  own?: boolean
  /** Serialized account (proxied avatar with a default fallback). */
  account?: MastodonAccount
}
export interface ChatLastMessage {
  event_id: string
  created_at: number
  own: boolean
  encrypted: boolean
  body?: string
  msgtype?: string
}
export interface ChatConversation {
  id: string
  room_id: string
  revision: number
  encrypted: false
  title: string
  creator_id: string
  can_manage: boolean
  can_leave?: boolean
  legacy_conversation_id?: string
  members: ChatMember[]
  last_message?: ChatLastMessage
}
export interface ChatMessage {
  event_id: string
  sender: string
  body: string
  msgtype: string
  url?: string
  status_id: string | null
  status?: MastodonStatus
  created_at: number
  own: boolean
  redacted?: boolean
  reply_event_id?: string
  delivery?: ChatDelivery
}
export interface ChatMessagePage { messages: ChatMessage[]; next_cursor: string | null }
export interface ChatSend { transaction_id: string; body: string; reply_event_id?: string; media_ids?: string[] }

export interface ChatDelivery {
  event_id: string
  room_id: string
  state: 'queued' | 'sending' | 'delivered' | 'failed' | 'skipped'
  remote_received: boolean
  error_code?: string
  can_retry: boolean
  updated_at: string
}
export const deliveryLabel: Record<ChatDelivery['state'], string> = {
  queued: t('message.chat.s1'), sending: t('message.chat.s2'), delivered: t('message.chat.s3'), failed: t('message.chat.s4'), skipped: t('message.chat.s5'),
}

/** Active members other than the viewer, in member order. */
export function chatPeers(chat: ChatConversation, selfId?: string): ChatMember[] {
  return chat.members.filter(m => m.active !== false && !m.own && !(selfId && m.account_id === selfId))
}

export function memberName(m: ChatMember): string {
  return m.account?.display_name || m.display_name || m.account?.username || m.acct?.split('@')[0] || m.matrix_id
}

const TITLE_NAMES = 3

/** Room name when set; otherwise who the conversation is with. */
export function chatTitle(chat: ChatConversation, selfId?: string): string {
  if (chat.title) return chat.title
  const peers = chatPeers(chat, selfId)
  if (peers.length === 0) return ''
  const names = peers.slice(0, TITLE_NAMES).map(memberName).join(t('message.chat.namesSep'))
  return peers.length > TITLE_NAMES ? t('message.chat.titleMore', [names, peers.length - TITLE_NAMES]) : names
}

/** "@acct" of the other member of a 1:1 conversation, else "". */
export function chatPeerAcct(chat: ChatConversation, selfId?: string): string {
  if (chat.title) return ''
  const peers = chatPeers(chat, selfId)
  if (peers.length !== 1) return ''
  const acct = peers[0].account?.acct || peers[0].acct
  return acct ? `@${acct}` : ''
}

/** Accounts for the list avatar: the peer, or up to three for groups. */
export function chatAvatars(chat: ChatConversation, selfId?: string): MastodonAccount[] {
  return chatPeers(chat, selfId).map(m => m.account).filter((a): a is MastodonAccount => !!a).slice(0, 3)
}

/** One-line preview of the last message, or "" when there is none. */
export function chatPreview(chat: ChatConversation): string {
  const last = chat.last_message
  if (!last) return ''
  let text = last.encrypted ? t('message.chat.encrypted') : (last.body || '')
  if (!last.encrypted && last.msgtype && last.msgtype !== 'm.text' && last.msgtype !== 'm.notice' && last.msgtype !== 'm.emote') text = t('message.chat.attachment')
  text = text.replace(/\s+/g, ' ').trim()
  return last.own && text ? t('message.chat.you', [text]) : text
}
