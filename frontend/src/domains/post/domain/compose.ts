import type { MastodonStatus, Visibility } from '@/shared/types/mastodon'
import type { CreateStatusParams } from './entities'

import { t } from '@/shared/i18n'
export const MAX_POLL_OPTIONS = 4
export const MIN_POLL_OPTIONS = 2
export const MAX_POLL_OPTION_LENGTH = 50
export const MAX_MEDIA_ATTACHMENTS = 4

/** Shown wherever a post is replaced: the server deletes the original and publishes a new one. */
export const REPLACE_NOTE =
  t('post.compose.s1')
export const REPLACE_ACTION = t('post.compose.s2')
export const REPLACE_MENU = t('post.compose.s3')
export const REPLACE_DIALOG = t('post.compose.s4')
export const REPLACE_SUCCESS = t('post.compose.s5')
export const REPLACE_FAILED = t('post.compose.s6')
export const DIRECT_SCOPE =
  t('post.compose.s7')

export const POLL_DURATIONS: { label: string; value: number }[] = [
  { label: t('post.compose.s8'), value: 300 },
  { label: t('post.compose.s9'), value: 1800 },
  { label: t('post.compose.s10'), value: 3600 },
  { label: t('post.compose.s11'), value: 21600 },
  { label: t('post.compose.s12'), value: 86400 },
  { label: t('post.compose.s13'), value: 259200 },
  { label: t('post.compose.s14'), value: 604800 },
]

export const VISIBILITY_OPTIONS: { label: string; short: string; value: Visibility; icon: string }[] = [
  { label: t('post.compose.s15'), short: t('post.compose.s16'), value: 'public', icon: 'earth' },
  { label: t('post.compose.s17'), short: t('post.compose.s18'), value: 'unlisted', icon: 'lock-off' },
  { label: t('post.compose.s19'), short: t('post.compose.s20'), value: 'private', icon: 'lock-on' },
  { label: t('post.compose.s21'), short: t('post.compose.s22'), value: 'direct', icon: 'mail' },
]

/** expires_in sent for a poll without deadline (~10 years, the server cap). */
export const POLL_NO_DEADLINE = 10 * 365 * 24 * 3600

/** Misskey-style deadline modes. The Mastodon API always needs expires_in. */
export type PollDeadlineMode = 'none' | 'duration' | 'at'

export interface PollDraft {
  options: string[]
  expiresIn: number
  multiple: boolean
  /** Defaults to 'duration'. */
  mode?: PollDeadlineMode
  /** Local datetime ("YYYY-MM-DDTHH:mm") for mode 'at'. */
  endsAt?: string
}

/** Instance poll limits (configuration.polls of /api/v2/instance). */
export interface PollLimits {
  maxOptions: number
  maxCharactersPerOption: number
  minExpiration: number
  maxExpiration: number
}

export const DEFAULT_POLL_LIMITS: PollLimits = {
  maxOptions: MAX_POLL_OPTIONS,
  maxCharactersPerOption: MAX_POLL_OPTION_LENGTH,
  minExpiration: 300,
  maxExpiration: 2629746,
}

/** Seconds until the poll closes, as sent in expires_in. */
export function pollExpiresIn(poll: PollDraft, now: Date = new Date()): number {
  if (poll.mode === 'none') return POLL_NO_DEADLINE
  if (poll.mode === 'at') {
    const end = poll.endsAt ? new Date(poll.endsAt).getTime() : NaN
    if (Number.isNaN(end)) return 0
    return Math.floor((end - now.getTime()) / 1000)
  }
  return poll.expiresIn
}

/** A poll whose end is years away was created with "no deadline". */
export function isNoDeadlinePoll(expiresAt: string | null | undefined, now: Date = new Date()): boolean {
  if (!expiresAt) return false
  const end = new Date(expiresAt).getTime()
  return !Number.isNaN(end) && end - now.getTime() > 5 * 365 * 24 * 3600 * 1000
}

export interface ComposeDraft {
  text: string
  spoilerEnabled: boolean
  spoilerText: string
  visibility: Visibility
  poll: PollDraft | null
  mediaIds: string[]
  inReplyToId?: string
  quoteId?: string
}

const VISIBILITY_RANK: Record<Visibility, number> = { public: 0, unlisted: 1, private: 2, direct: 3 }

/** A reply defaults to the more restrictive of the parent's and the default visibility. */
export function replyVisibility(parent: Visibility | undefined, fallback: Visibility = 'public'): Visibility {
  if (!parent) return fallback
  return VISIBILITY_RANK[parent] > VISIBILITY_RANK[fallback] ? parent : fallback
}

/** Options the author may pick: equal to `ceiling` or stricter, never wider. */
export function visibilityChoices(ceiling: Visibility = 'public') {
  const min = VISIBILITY_RANK[ceiling]
  return VISIBILITY_OPTIONS.filter(o => VISIBILITY_RANK[o.value] >= min)
}

export function visibilityOption(visibility: Visibility) {
  return VISIBILITY_OPTIONS.find(o => o.value === visibility) ?? VISIBILITY_OPTIONS[0]!
}

/** A boost publishes into the booster's timeline, so followers-only and direct posts stay put. */
export function canBoost(visibility: Visibility): boolean {
  return visibility === 'public' || visibility === 'unlisted'
}

/** A quote embeds a public or unlisted post. Followers-only and direct stay put. */
export function canQuote(visibility: Visibility): boolean {
  return visibility === 'public' || visibility === 'unlisted'
}
/** Direct messages are not a public link. */
export function canShare(visibility: Visibility): boolean {
  return visibility !== 'direct'
}

/**
 * "@alice @bob " prefix for a reply: the author of the parent plus everyone it
 * mentions, minus the current user, without duplicates.
 */
export function replyMentions(parent: MastodonStatus, selfAcct?: string): string {
  const self = selfAcct?.toLowerCase()
  const accts = [parent.account.acct, ...(parent.mentions || []).map(m => m.acct)]
  const seen = new Set<string>()
  const out: string[] = []
  for (const acct of accts) {
    const key = acct.toLowerCase()
    if (!key || key === self || seen.has(key)) continue
    seen.add(key)
    out.push(`@${acct}`)
  }
  return out.length ? `${out.join(' ')} ` : ''
}

function countChars(value: string): number {
  return Array.from(value).length
}

export function composeLength(draft: ComposeDraft): number {
  return countChars(draft.text) + (draft.spoilerEnabled ? countChars(draft.spoilerText) : 0)
}

const MENTION = /(^|[^\w@])@[\p{L}\p{N}_]+(?:@[\w.-]+)?/u

export function validateDraft(draft: ComposeDraft, limit: number, pollLimits: PollLimits = DEFAULT_POLL_LIMITS, now: Date = new Date()): string | null {
  const text = draft.text.trim()
  if (draft.spoilerEnabled && !draft.spoilerText.trim()) return t('post.compose.s23')
  if (composeLength(draft) > limit) return t('post.compose.s24', [limit])
  if (draft.poll) {
    const options = draft.poll.options.map(option => option.trim())
    if (options.length < MIN_POLL_OPTIONS) return t('post.compose.s25', [MIN_POLL_OPTIONS])
    if (options.some(option => !option)) return t('post.compose.s26')
    if (new Set(options).size !== options.length) return t('post.compose.s27')
    if (options.length > pollLimits.maxOptions) return t('post.compose.pollTooManyOptions', [pollLimits.maxOptions])
    if (options.some(option => countChars(option) > pollLimits.maxCharactersPerOption)) {
      return t('post.compose.s28', [pollLimits.maxCharactersPerOption])
    }
    if (draft.poll.mode === 'at') {
      const seconds = pollExpiresIn(draft.poll, now)
      if (seconds < pollLimits.minExpiration) return t('post.compose.pollEndsTooSoon')
      if (pollLimits.maxExpiration > 0 && seconds > pollLimits.maxExpiration) return t('post.compose.pollEndsTooLate')
    }
    if (draft.mediaIds.length) return t('post.compose.s29')
  }
  if (!text && draft.mediaIds.length === 0 && !draft.quoteId) return t('post.compose.s30')
  if (draft.visibility === 'direct' && !MENTION.test(draft.text)) return t('post.compose.s31')
  return null
}

export function buildCreateParams(draft: ComposeDraft, now: Date = new Date()): CreateStatusParams {
  const params: CreateStatusParams = { status: draft.text, visibility: draft.visibility }
  if (draft.spoilerEnabled && draft.spoilerText.trim()) {
    params.spoiler_text = draft.spoilerText.trim()
    params.sensitive = true
  }
  if (draft.inReplyToId) params.in_reply_to_id = draft.inReplyToId
  if (draft.quoteId) params.quote_id = draft.quoteId
  if (draft.mediaIds.length) params.media_ids = draft.mediaIds
  if (draft.poll) {
    params.poll = {
      options: draft.poll.options.map(option => option.trim()),
      expires_in: pollExpiresIn(draft.poll, now),
      multiple: draft.poll.multiple,
    }
  }
  return params
}
