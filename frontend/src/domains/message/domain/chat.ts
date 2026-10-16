import type { MastodonStatus } from '@/shared/types/mastodon'

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
