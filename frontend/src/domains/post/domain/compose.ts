import type { MastodonStatus, Visibility } from '@/shared/types/mastodon'
import type { CreateStatusParams } from './entities'

export const MAX_POLL_OPTIONS = 4
export const MIN_POLL_OPTIONS = 2
export const MAX_POLL_OPTION_LENGTH = 50
export const MAX_MEDIA_ATTACHMENTS = 4

/** Shown wherever a post is replaced: the server deletes the original and publishes a new one. */
export const REPLACE_NOTE =
  '编辑会删除原帖后再重新发布，帖子链接会改变。点赞、转发、书签和投票记录不会保留，已有回复不会迁移。可见范围、回复对象和附件保持不变。'
export const REPLACE_ACTION = '删除原帖并重发'
export const REPLACE_MENU = '编辑（删除后重发）'
export const REPLACE_DIALOG = '编辑帖子 · 删除后重发'
export const REPLACE_SUCCESS = '已删除原帖并重新发布。旧链接已失效，点赞和回复不会带到新帖。'
export const REPLACE_FAILED = '删除重发未完成，草稿已保留'
export const DIRECT_SCOPE =
  '仅你和被提及的人可以看到这条私信。它不会出现在公开或关注时间线，也不能被转发。'

export const POLL_DURATIONS: { label: string; value: number }[] = [
  { label: '5 分钟', value: 300 },
  { label: '30 分钟', value: 1800 },
  { label: '1 小时', value: 3600 },
  { label: '6 小时', value: 21600 },
  { label: '1 天', value: 86400 },
  { label: '3 天', value: 259200 },
  { label: '7 天', value: 604800 },
]

export const VISIBILITY_OPTIONS: { label: string; short: string; value: Visibility; icon: string }[] = [
  { label: '公开 · 所有人可见', short: '公开', value: 'public', icon: 'earth' },
  { label: '不公开 · 不出现在公共时间线', short: '不公开', value: 'unlisted', icon: 'lock-off' },
  { label: '仅关注者可见', short: '仅关注者', value: 'private', icon: 'lock-on' },
  { label: '私信 · 仅提及的人可见', short: '仅提及的人', value: 'direct', icon: 'mail' },
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

export function validateDraft(draft: ComposeDraft, limit: number): string | null {
  const text = draft.text.trim()
  if (draft.spoilerEnabled && !draft.spoilerText.trim()) return '请填写内容警告'
  if (composeLength(draft) > limit) return `不能超过 ${limit} 个字符`
  if (draft.poll) {
    const options = draft.poll.options.map(option => option.trim())
    if (options.length < MIN_POLL_OPTIONS) return `投票至少需要 ${MIN_POLL_OPTIONS} 个选项`
    if (options.some(option => !option)) return '投票选项不能为空'
    if (new Set(options).size !== options.length) return '投票选项不能重复'
    if (options.some(option => countChars(option) > MAX_POLL_OPTION_LENGTH)) {
      return `每个投票选项不能超过 ${MAX_POLL_OPTION_LENGTH} 个字符`
    }
    if (draft.mediaIds.length) return '投票不能和媒体同时发布'
  }
  if (!text && draft.mediaIds.length === 0) return '写点什么再发布'
  if (draft.visibility === 'direct' && !MENTION.test(draft.text)) return '私信需要 @提及 至少一位用户'
  return null
}

export function buildCreateParams(draft: ComposeDraft): CreateStatusParams {
  const params: CreateStatusParams = { status: draft.text, visibility: draft.visibility }
  if (draft.spoilerEnabled && draft.spoilerText.trim()) {
    params.spoiler_text = draft.spoilerText.trim()
    params.sensitive = true
  }
  if (draft.inReplyToId) params.in_reply_to_id = draft.inReplyToId
  if (draft.mediaIds.length) params.media_ids = draft.mediaIds
  if (draft.poll) {
    params.poll = {
      options: draft.poll.options.map(option => option.trim()),
      expires_in: draft.poll.expiresIn,
      multiple: draft.poll.multiple,
    }
  }
  return params
}
