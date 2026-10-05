import type { MastodonPoll } from '@/shared/types/mastodon'

/** Rounded share of `votes` in `total`, 0 when there are no votes yet. */
export function pollOptionPercent(votes: number | null | undefined, total: number | null | undefined): number {
  if (!votes || !total || total <= 0) return 0
  return Math.round((votes / total) * 100)
}

/** Results replace the ballot once the viewer has voted or the poll has ended.
 *  The author can still vote; "查看结果" reveals the tally without voting. */
export function pollShowsResults(poll: Pick<MastodonPoll, 'voted' | 'expired'>): boolean {
  return !!poll.voted || !!poll.expired
}
