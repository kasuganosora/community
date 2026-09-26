import type { MastodonPoll } from '@/shared/types/mastodon'

/** Rounded share of `votes` in `total`, 0 when there are no votes yet. */
export function pollOptionPercent(votes: number | null | undefined, total: number | null | undefined): number {
  if (!votes || !total || total <= 0) return 0
  return Math.round((votes / total) * 100)
}

/** Results are shown once the viewer voted, the poll ended, or it's their own poll. */
export function pollShowsResults(poll: Pick<MastodonPoll, 'voted' | 'expired'>, own = false): boolean {
  return !!poll.voted || !!poll.expired || own
}
