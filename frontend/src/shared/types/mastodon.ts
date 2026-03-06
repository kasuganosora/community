// Mastodon API type definitions (compatible with backend serializers)

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
}

export interface MastodonMention {
  id: string
  username: string
  acct: string
  url: string
}

export interface MastodonTag {
  name: string
  url: string
  history?: { day: string; uses: string; accounts: string }[]
}

export interface MastodonAttachment {
  id: string
  type: 'image' | 'video' | 'gifv' | 'audio' | 'unknown'
  url: string
  preview_url: string | null
  remote_url: string | null
  text_url: string | null
  meta: Record<string, unknown> | null
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

export interface MastodonCard {
  url: string
  title: string
  description: string
  type: 'link' | 'photo' | 'video' | 'rich'
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
  visibility: 'public' | 'unlisted' | 'private' | 'direct'
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

export interface MastodonNotification {
  id: string
  type: 'mention' | 'reblog' | 'favourite' | 'follow' | 'poll' | 'follow_request' | 'update' | 'status'
  created_at: string
  account: MastodonAccount
  status?: MastodonStatus
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

export interface MastodonContext {
  ancestors: MastodonStatus[]
  descendants: MastodonStatus[]
}

export interface SearchResult {
  accounts: MastodonAccount[]
  statuses: MastodonStatus[]
  hashtags: MastodonTag[]
}
