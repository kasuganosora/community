import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useTimelineStore } from '../timelineStore'
import type { MastodonStatus } from '@/shared/types/mastodon'

describe('new posts in timelines', () => {
  beforeEach(() => setActivePinia(createPinia()))
  it('shows the first public post immediately and does not duplicate it', () => {
    const store = useTimelineStore()
    const post = { id: 'first', visibility: 'public' } as MastodonStatus
    store.prependStatus(post)
    store.prependStatus(post)
    expect(store.publicStatuses.map(s => s.id)).toEqual(['first'])
    expect(store.homeStatuses.map(s => s.id)).toEqual(['first'])
  })
  it.each(['private', 'direct', 'unlisted'] as const)('keeps %s posts out of the public feed', (visibility) => {
    const store = useTimelineStore()
    store.prependStatus({ id: 'private', visibility } as MastodonStatus)
    expect(store.publicStatuses).toEqual([])
    expect(store.homeStatuses).toHaveLength(1)
  })
})
