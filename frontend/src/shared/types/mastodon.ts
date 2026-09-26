// Mastodon API type definitions (compatible with backend serializers)

/** Visibility levels for statuses, shared across API types */
export type Visibility = 'public' | 'unlisted' | 'private' | 'direct'

/** Notification types that always include a `status` field */
export type NotificationTypeWithStatus = 'mention' | 'reblog' | 'favourite' | 'poll' | 'update' | 'status'

/** Notification types that never include a `status` field */
export type NotificationTypeWithoutStatus = 'follow' | 'follow_request'

/** All valid Mastodon notification types */
export type NotificationType = NotificationTypeWithStatus | NotificationTypeWithoutStatus

/** API error response shape */
export interface ApiErrorData {
  error?: string
  message?: string
}

export interface MastodonApplication {
  name: string
  website: string | null
}

export interface MastodonEmoji {
  shortcode: string
  url: string
  static_url: string
  visible_in_picker: boolean
}

export interface MastodonField {
  name: string
  value: string
  verified_at: string | null
}

export interface MastodonAccount {
  id: string
  username: string
  acct: string
  display_name: string
  locked: boolean
  bot: boolean
  discoverable: boolean
  indexable: boolean
  group: boolean
  created_at: string
  note: string
  url: string
  uri: string
  avatar: string
  avatar_static: string
  header: string
  header_static: string
  followers_count: number
  following_count: number
  statuses_count: number
  last_status_at: string | null
  hide_collections: boolean
  emojis: MastodonEmoji[]
  fields: MastodonField[]
  /** Only present on verify_credentials / update_credentials (own account). */
  source?: MastodonAccountSource
}

export interface MastodonAccountSource {
  note: string
  privacy?: Visibility
  sensitive?: boolean
  language?: string
  fields?: { name: string; value: string }[]
}

export interface MastodonMention {
  id: string
  username: string
  acct: string
  url: string
}

/** Daily usage history entry for a hashtag */
export interface MastodonTagHistory {
  day: string
  uses: string
  accounts: string
}

export interface MastodonTag {
  name: string
  url: string
  history?: MastodonTagHistory[]
}

/** Allowed media attachment MIME categories */
export type AttachmentType = 'image' | 'video' | 'gifv' | 'audio' | 'unknown'

/** Metadata sub-types for attachment dimensions */
export interface AttachmentDimensions {
  width?: number
  height?: number
  size?: string
  aspect?: number
  frame_rate?: string
  duration?: number
  bitrate?: number
}

/** Structured metadata for a media attachment */
export interface AttachmentMeta {
  original?: AttachmentDimensions
  small?: AttachmentDimensions
  focus?: { x: number; y: number }
}

export interface MastodonAttachment {
  id: string
  type: AttachmentType
  url: string
  preview_url: string | null
  remote_url: string | null
  text_url: string | null
  meta: AttachmentMeta | null
  description: string | null
  blurhash: string | null
}

export interface MastodonPollOption {
  title: string
  votes_count: number | null
}

export interface MastodonPoll {
  id: string
  expires_at: string | null
  expired: boolean
  multiple: boolean
  votes_count: number
  voters_count: number | null
  voted: boolean
  own_votes: number[]
  options: MastodonPollOption[]
  emojis: MastodonEmoji[]
}

/** Preview card type */
export type CardType = 'link' | 'photo' | 'video' | 'rich'

export interface MastodonCard {
  url: string
  title: string
  description: string
  type: CardType
  image: string | null
  provider_name: string
  provider_url: string
}

export interface MastodonStatus {
  id: string
  created_at: string
  in_reply_to_id: string | null
  in_reply_to_account_id: string | null
  sensitive: boolean
  spoiler_text: string
  visibility: Visibility
  language: string | null
  uri: string
  url: string
  replies_count: number
  reblogs_count: number
  favourites_count: number
  edited_at: string | null
  favourited: boolean
  reblogged: boolean
  muted: boolean
  bookmarked: boolean
  pinned: boolean
  content: string
  reblog: MastodonStatus | null
  application: MastodonApplication | null
  account: MastodonAccount
  media_attachments: MastodonAttachment[]
  mentions: MastodonMention[]
  tags: MastodonTag[]
  emojis: MastodonEmoji[]
  card: MastodonCard | null
  poll: MastodonPoll | null
}

/** Notification that always carries a status (mention, reblog, favourite, poll, update, status) */
export interface MastodonNotificationWithStatus {
  id: string
  type: NotificationTypeWithStatus
  created_at: string
  account: MastodonAccount
  status: MastodonStatus
}

/** Notification that never carries a status (follow, follow_request) */
export interface MastodonNotificationWithoutStatus {
  id: string
  type: NotificationTypeWithoutStatus
  created_at: string
  account: MastodonAccount
  status?: never
}

/** Discriminated union — `type` discriminates whether `status` is present */
export type MastodonNotification = MastodonNotificationWithStatus | MastodonNotificationWithoutStatus

/** Type guard: narrows a notification to one that carries a status */
export function notificationHasStatus(
  n: MastodonNotification,
): n is MastodonNotificationWithStatus {
  return n.status !== undefined
}

export interface MastodonStatusSource {
  id: string
  text: string
  spoiler_text: string
}

export interface MastodonList {
  id: string
  title: string
  replies_policy?: 'followed' | 'list' | 'none'
}

export interface MastodonRelationship {
  id: string
  following: boolean
  showing_reblogs: boolean
  notifying: boolean
  followed_by: boolean
  blocking: boolean
  blocked_by: boolean
  muting: boolean
  muting_notifications: boolean
  requested: boolean
  domain_blocking: boolean
  endorsed: boolean
  note: string
}

export interface MastodonConversation {
  id: string
  unread: boolean
  accounts: MastodonAccount[]
  last_status: MastodonStatus | null
}

export interface MastodonSuggestion {
  account: MastodonAccount
}

export interface MastodonContext {
  ancestors: MastodonStatus[]
  descendants: MastodonStatus[]
}

export interface SearchResult {
  accounts: MastodonAccount[]
  statuses: MastodonStatus[]
  hashtags: MastodonTag[]
}
