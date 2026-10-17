import { t } from '@/shared/i18n'
export interface GroupCapabilities {
  send_message: boolean; invite: boolean; kick: boolean; ban: boolean; redact: boolean
  set_name: boolean; set_topic?: boolean; set_avatar: boolean; manage_permissions: boolean; manage_address?: boolean
}
export interface GroupPerson { id: string; display_name: string; avatar_url?: string }
export interface Group {
  id: string; name: string; topic: string; avatar_url?: string; target?: string; via?: string[]; directory_source?: string; preview_ref?: string
  membership: 'join' | 'invite' | 'knock' | 'leave' | 'ban' | 'none'
  encryption: 'unknown' | 'unencrypted' | 'encrypted'
  room_type?: string; join_rule: string; listed: boolean; member_count: number | null; unread_count?: number
  client_url: string; canonical_alias?: string; address_server?: string; capabilities: GroupCapabilities; replacement_id?: string
  limits?: { max_upload_bytes: number; max_attachments: number }; invite_failures?: { user_id: string; error: string; retryable: boolean }[]; muted?: boolean; typing_users?: GroupPerson[]; current_user_id?: string
}
export interface GroupMember extends GroupPerson { membership: string; power_level: number }
export interface GroupInvitee { user_id: string; username: string; display_name: string; avatar_url: string }
export interface GroupAttachment { id: string; name: string; type: string; size: number; url: string; thumbnail_url?: string }
export interface GroupEmoji { shortcode: string; url: string; static_url: string }
export interface GroupReaction { key: string; count: number; own: boolean; url?: string; label?: string; shortcode?: string }
export interface GroupMessage {
  id: string; sender: GroupPerson; body: string; formatted_body?: string
  kind: 'text' | 'image' | 'file' | 'notice' | 'unsupported'; created_at: number
  own: boolean; redacted: boolean; edited: boolean; reply_unavailable?: boolean; reply_to?: string
  custom_emojis?: GroupEmoji[]; attachments?: GroupAttachment[]; reactions: GroupReaction[]; transaction_id?: string
}
export interface GroupPage { groups: Group[]; next_cursor: string | null }
export interface GroupSearchPage extends GroupPage { source: string }
export interface GroupMessagePage { messages: GroupMessage[]; next_cursor: string | null }
export interface GroupSend { transaction_id: string; body: string; reply_to?: string; upload_ids?: string[]; mentions?: string[]; custom_emojis?: string[] }
export interface GroupSendResult { messages: GroupMessage[]; status: 'accepted' | 'partial'; event_ids: string[]; failed_upload_ids?: string[]; deleted?: boolean }
export interface GroupJoinResult { group?: Group; status: 'joined' | 'joining' | 'knock'; operation_id?: string; client_required?: boolean; client_url?: string; reason?: string }
export interface GroupUpload { id: string; name: string; type: string; size: number; url?: string }
export interface GroupSync {
  has_more?: boolean; next_cursor: string; groups: Group[]; changed_group_ids: string[]; left_group_ids: string[]; reset: boolean
  messages_by_group?: Record<string, GroupMessage[]>; typing_by_group?: Record<string, GroupPerson[]>
  degraded?: boolean; retry_after_ms?: number
}
export const groupPath = (id: string) => `/groups/${encodeURIComponent(id)}`
export const groupMembershipLabel: Record<string, string> = { join: t('group.group.s1'), invite: t('group.group.s2'), knock: t('group.group.s3'), leave: t('group.group.s4'), ban: t('group.group.s5'), none: t('group.group.s6') }
export function groupCanSend(group: Group | null | undefined): boolean {
  return !!group && group.membership === 'join' && group.encryption === 'unencrypted' && group.room_type !== 'm.space' && !group.replacement_id && group.capabilities.send_message
}
export function mergeGroupMessages(current: GroupMessage[], incoming: GroupMessage[], prepend = false): GroupMessage[] {
  const updates = new Map(incoming.map(message => [message.id, message]))
  const existing = new Set(current.map(message => message.id))
  const replaced = current.map(message => updates.get(message.id) ?? message)
  const added = incoming.filter(message => !existing.has(message.id))
  // Server order is authoritative; remote timestamps must not reorder history.
  return prepend ? [...added, ...replaced] : [...replaced, ...added]
}
export function safeGroupURL(raw?: string): string {
  if (!raw) return ''
  try { const url = new URL(raw, window.location.origin); return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : '' } catch { return '' }
}
export function fileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return t('group.group.s7')
  return bytes < 1024 ? `${bytes} B` : bytes < 1048576 ? `${(bytes / 1024).toFixed(1)} KB` : `${(bytes / 1048576).toFixed(1)} MB`
}

/** Match the complete tag, not #NSFWart, identifiers or URL fragments. */
export function hasNSFWTag(text: string): boolean {
  return /(?:^|[^\p{L}\p{N}\p{M}_/#])#nsfw(?![\p{L}\p{N}\p{M}_])/iu.test(text)
}
