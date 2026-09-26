import type { MastodonStatus, Visibility } from '@/shared/types/mastodon'
import type { CreateStatusParams } from './entities'

export const MAX_POLL_OPTIONS = 4
export const MIN_POLL_OPTIONS = 2
export const MAX_POLL_OPTION_LENGTH = 50
export const MAX_MEDIA_ATTACHMENTS = 4

export const POLL_DURATIONS: { label: string; value: number }[] = [
  { label: '5 分钟', value: 300 },
  { label: '30 分钟', value: 1800 },
  { label: '1 小时', value: 3600 },
  { label: '6 小时', value: 21600 },
  { label: '1 天', value: 86400 },
  { label: '3 天', value: 259200 },
  { label: '7 天', value: 604800 },
]

export const VISIBILITY_OPTIONS: { label: string; value: Visibility; icon: string }[] = [
  { label: '公开', value: 'public', icon: 'earth' },
  { label: '不公开（不出现在公共时间线）', value: 'unlisted', icon: 'lock-off' },
  { label: '仅关注者', value: 'private', icon: 'lock-on' },
  { label: '私信（仅提及的用户）', value: 'direct', icon: 'mail' },
]

export interface PollDraft {
  options: string[]
  expiresIn: number
  multiple: boolean
}

export interface ComposeDraft {
  text: string
  spoilerEnabled: boolean
  spoilerText: string
  visibility: Visibility
  poll: PollDraft | null
  mediaIds: string[]
  inReplyToId?: string
}

const VISIBILITY_RANK: Record<Visibility, number> = { public: 0, unlisted: 1, private: 2, direct: 3 }

/** A reply defaults to the more restrictive of the parent's and the default visibility. */
export function replyVisibility(parent: Visibility | undefined, fallback: Visibility = 'public'): Visibility {
  if (!parent) return fallback
  return VISIBILITY_RANK[parent] > VISIBILITY_RANK[fallback] ? parent : fallback
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
    if (!acct || key === self || seen.has(key)) continue
    seen.add(key)
    out.push(`@${acct}`)
  }
  return out.length ? out.join(' ') + ' ' : ''
}

/** Characters counted against the limit: the text plus the content warning. */
export function composeLength(draft: Pick<ComposeDraft, 'text' | 'spoilerEnabled' | 'spoilerText'>): number {
  // Array.from counts astral characters (emoji) once, like the server does.
  const text = Array.from(draft.text).length
  return draft.spoilerEnabled ? text + Array.from(draft.spoilerText).length : text
}

/** Returns a user-facing error message, or null when the draft can be posted. */
export function validateDraft(draft: ComposeDraft, maxChars: number): string | null {
  const hasText = draft.text.trim().length > 0
  if (!hasText && draft.mediaIds.length === 0) return '请输入内容'
  if (composeLength(draft) > maxChars) return `内容不能超过 ${maxChars} 个字`
  if (draft.spoilerEnabled && !draft.spoilerText.trim()) return '请填写内容警告的说明文字'
  if (draft.poll) {
    if (draft.mediaIds.length) return '投票和媒体不能同时使用'
    const options = draft.poll.options.map(o => o.trim())
    if (options.some(o => !o)) return '投票选项不能为空'
    if (options.length < MIN_POLL_OPTIONS) return `投票至少需要 ${MIN_POLL_OPTIONS} 个选项`
    if (new Set(options).size !== options.length) return '投票选项不能重复'
    if (options.some(o => Array.from(o).length > MAX_POLL_OPTION_LENGTH)) return `每个选项最多 ${MAX_POLL_OPTION_LENGTH} 个字`
  }
  if (draft.mediaIds.length > MAX_MEDIA_ATTACHMENTS) return `最多只能添加 ${MAX_MEDIA_ATTACHMENTS} 个媒体文件`
  // A direct post is only delivered to the accounts it mentions.
  if (draft.visibility === 'direct' && !MENTION_RE.test(draft.text)) return '私信需要 @提及 至少一位用户'
  return null
}

const MENTION_RE = /(^|[^\w@])@[\w.-]+/

/** Build the POST /api/v1/statuses body from a validated draft. */
export function buildCreateParams(draft: ComposeDraft): CreateStatusParams {
  const params: CreateStatusParams = {
    status: draft.text,
    visibility: draft.visibility,
  }
  if (draft.inReplyToId) params.in_reply_to_id = draft.inReplyToId
  if (draft.spoilerEnabled && draft.spoilerText.trim()) {
    params.spoiler_text = draft.spoilerText.trim()
    params.sensitive = true
  }
  if (draft.mediaIds.length) params.media_ids = [...draft.mediaIds]
  if (draft.poll) {
    params.poll = {
      options: draft.poll.options.map(o => o.trim()),
      expires_in: draft.poll.expiresIn,
      multiple: draft.poll.multiple,
    }
  }
  return params
}
