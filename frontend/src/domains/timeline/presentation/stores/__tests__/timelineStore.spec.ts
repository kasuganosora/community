import { beforeEach, describe, expect, it, vi } from 'vitest'
import { invalidateSessionData } from '@/shared/api/sessionScope'
import { timelineApi } from '../../../infrastructure/timelineApi'
import { createPinia, setActivePinia } from 'pinia'
import { useTimelineStore } from '../timelineStore'
import type { MastodonStatus } from '@/shared/types/mastodon'

vi.mock('../../../infrastructure/timelineApi', () => ({ timelineApi: { getHomeTimeline: vi.fn(), getPublicTimeline: vi.fn() } }))

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


describe('timeline account and cursor isolation', () => {
  beforeEach(() => { setActivePinia(createPinia()); vi.clearAllMocks() })
  it('deduplicates overlapping pages by wrapper ID while preserving distinct reblogs', async () => {
    const first = Array.from({ length: 20 }, (_, i) => ({ id: String(100 - i) } as MastodonStatus))
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce(first).mockResolvedValueOnce([first[19]!, { id: '80', reblog: { id: '81' } }, { id: '79', reblog: { id: '81' } }] as MastodonStatus[])
    const store = useTimelineStore()
    await store.fetchHomeTimeline(); await store.loadMoreHome()
    expect(timelineApi.getHomeTimeline).toHaveBeenLastCalledWith({ max_id: '81', limit: 20 })
    expect(store.homeStatuses).toHaveLength(22)
    expect(store.homeStatuses.slice(-2).map(s => s.id)).toEqual(['80', '79'])
  })
  it('stops loading when a full page is only duplicates', async () => {
    const first = Array.from({ length: 20 }, (_, i) => ({ id: String(100 - i) } as MastodonStatus))
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce(first).mockResolvedValueOnce(first)
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    await store.loadMoreHome()
    expect(store.homeStatuses).toHaveLength(20)
    expect(store.homeTimeline.hasMore).toBe(false)
  })
  it('holds newer posts until they are revealed, without duplicating the current head', async () => {
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '20' }] as MastodonStatus[])
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '30' }, { id: '20' }] as MastodonStatus[])
    await store.checkNewer('home')
    expect(store.homeTimeline.newer.map(status => status.id)).toEqual(['30'])
    expect(store.homeStatuses.map(status => status.id)).toEqual(['20'])
    store.revealNewer('home')
    expect(store.homeStatuses.map(status => status.id)).toEqual(['30', '20'])
    expect(store.homeTimeline.newer).toEqual([])
  })
  it('drops cached private content and late timeline responses when the account changes', async () => {
    let resolve!: (rows: MastodonStatus[]) => void
    vi.mocked(timelineApi.getHomeTimeline).mockImplementationOnce(() => new Promise(r => { resolve = r }))
    const store = useTimelineStore()
    store.prependStatus({ id: 'private-A', visibility: 'private' } as MastodonStatus)
    const old = store.fetchHomeTimeline()
    invalidateSessionData()
    expect(store.homeStatuses).toEqual([])
    resolve([{ id: 'late-A', visibility: 'private' } as MastodonStatus]); await old
    expect(store.homeStatuses).toEqual([])
    expect(store.homeTimeline.isLoading).toBe(false)
  })
})
