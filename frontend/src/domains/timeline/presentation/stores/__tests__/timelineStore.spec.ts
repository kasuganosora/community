import { beforeEach, describe, expect, it, vi } from 'vitest'
import { invalidateSessionData } from '@/shared/api/sessionScope'
import { timelineApi } from '../../../infrastructure/timelineApi'
import { createPinia, setActivePinia } from 'pinia'
import { useTimelineStore } from '../timelineStore'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
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
  it('hides your own boost when the original post is already in the feed', () => {
    const auth = useAuthStore()
    auth.currentUser = { id: 'me' } as NonNullable<typeof auth.currentUser>
    const store = useTimelineStore()
    store.prependStatus({ id: 'orig', visibility: 'public', account: { id: 'other' } } as MastodonStatus)
    store.prependStatus({ id: 'mine', visibility: 'public', account: { id: 'me' }, reblog: { id: 'orig' } } as MastodonStatus)
    store.prependStatus({ id: 'theirs', visibility: 'public', account: { id: 'them' }, reblog: { id: 'orig' } } as MastodonStatus)
    expect(store.publicStatuses.map(status => status.id)).toEqual(['theirs', 'orig'])
  })
})


describe('timeline account and cursor isolation', () => {
  beforeEach(() => { setActivePinia(createPinia()); vi.clearAllMocks() })
  it.each(['for-you', 'global'] as const)('retains the %s tab across navigation and resets it synchronously for another session', tab => {
    const store = useTimelineStore()
    store.homeTab = tab
    expect(useTimelineStore().homeTab).toBe(tab)
    invalidateSessionData()
    expect(store.homeTab).toBe('following')
    expect(useTimelineStore().homeTab).toBe('following')
  })
  it('defaults the home tab to following', () => {
    expect(useTimelineStore().homeTab).toBe('following')
  })
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
  it('prefetches the head when newer posts are found and reveals that page without another request', async () => {
    const api = vi.mocked(timelineApi.getHomeTimeline)
    api.mockResolvedValueOnce([{ id: '20' }] as MastodonStatus[])
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    api.mockResolvedValueOnce([{ id: '30' }, { id: '20' }] as MastodonStatus[])
      .mockResolvedValueOnce([{ id: '20' }] as MastodonStatus[])
    await store.checkNewer('home')
    expect(store.homeTimeline.newer.map(status => status.id)).toEqual(['30'])
    expect(store.homeStatuses.map(status => status.id)).toEqual(['20'])
    expect(api).toHaveBeenCalledTimes(3)
    await store.revealNewer('home')
    expect(api).toHaveBeenCalledTimes(3)
    expect(store.homeStatuses.map(status => status.id)).toEqual(['30', '20'])
    expect(store.homeTimeline.newer).toEqual([])
  })
  it('holds newer posts until they are revealed, without duplicating the current head', async () => {
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '20' }] as MastodonStatus[])
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '30' }, { id: '20' }] as MastodonStatus[]).mockResolvedValueOnce([{ id: '20' }] as MastodonStatus[])
    await store.checkNewer('home')
    expect(store.homeTimeline.newer.map(status => status.id)).toEqual(['30'])
    expect(store.homeStatuses.map(status => status.id)).toEqual(['20'])
    await store.revealNewer('home')
    expect(store.homeStatuses.map(status => status.id)).toEqual(['30', '20'])
    expect(store.homeTimeline.newer).toEqual([])
  })
  it('does not apply a home prefetch that finishes after the account changes', async () => {
    let resolvePrefetch!: (rows: MastodonStatus[]) => void
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '20' }] as MastodonStatus[])
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    vi.mocked(timelineApi.getHomeTimeline).mockImplementationOnce(() => new Promise(resolve => { resolvePrefetch = resolve }))
    store.homeTimeline.newer.push({ id: '30' })
    invalidateSessionData()
    resolvePrefetch([{ id: '30' }, { id: '20' }] as MastodonStatus[])
    await Promise.resolve()
    await Promise.resolve()
    expect(store.homeStatuses).toEqual([])
    expect(store.homeTimeline.newer).toEqual([])
    await store.revealNewer('home')
    expect(timelineApi.getHomeTimeline).toHaveBeenCalledTimes(2)
    expect(store.homeStatuses).toEqual([])
  })
  it('puts streamed home posts on the pill and deletes without another timeline read', async () => {
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '20', content: 'old' }, { id: '19' }] as MastodonStatus[])
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    store.applyStreamed('home', { event: 'update', id: '21' })
    store.applyStreamed('home', { event: 'update', id: '22' })
    store.applyStreamed('home', { event: 'update', id: '21' })
    expect(store.homeTimeline.newer.map(status => status.id)).toEqual(['21', '22'])
    expect(store.homeStatuses.map(status => status.id)).toEqual(['20', '19'])
    expect(timelineApi.getHomeTimeline).toHaveBeenCalledTimes(1)
    store.applyStreamed('home', { event: 'delete', id: '19' })
    expect(store.homeStatuses.map(status => status.id)).toEqual(['20'])
    expect(timelineApi.getHomeTimeline).toHaveBeenCalledTimes(1)
  })
  it('shares one home window read while another is in flight', async () => {
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '20' }, { id: '19' }] as MastodonStatus[])
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    let release!: (rows: MastodonStatus[]) => void
    vi.mocked(timelineApi.getHomeTimeline).mockImplementationOnce(() => new Promise(resolve => { release = resolve }))
    const first = store.checkNewer('home')
    const second = store.checkNewer('home')
    await Promise.resolve()
    expect(timelineApi.getHomeTimeline).toHaveBeenCalledTimes(2)
    release([{ id: '20' }] as MastodonStatus[])
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '20' }] as MastodonStatus[])
    await first
    await second
    expect(timelineApi.getHomeTimeline).toHaveBeenCalledTimes(3)
  })
  it('does not start load-more while a home window read is in flight', async () => {
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '20' }, { id: '19' }] as MastodonStatus[])
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    store.homeTimeline.hasMore = true
    let release!: (rows: MastodonStatus[]) => void
    vi.mocked(timelineApi.getHomeTimeline).mockImplementationOnce(() => new Promise(resolve => { release = resolve }))
    const pending = store.checkNewer('home')
    await Promise.resolve()
    const calls = vi.mocked(timelineApi.getHomeTimeline).mock.calls.length
    await store.loadMoreHome()
    expect(vi.mocked(timelineApi.getHomeTimeline)).toHaveBeenCalledTimes(calls)
    release([{ id: '20' }] as MastodonStatus[])
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '20' }] as MastodonStatus[])
    await pending
  })
  it('checks only the head when asked, and does not walk max_id', async () => {
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '20' }, { id: '19' }] as MastodonStatus[])
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '22', content: 'new' }, { id: '20', content: 'edited' }] as MastodonStatus[])
    await store.checkNewer('home', 'head')
    expect(timelineApi.getHomeTimeline).toHaveBeenCalledTimes(2)
    expect(timelineApi.getHomeTimeline).toHaveBeenLastCalledWith({ limit: 20 })
    expect(store.homeTimeline.newer.map(status => status.id)).toEqual(['22'])
    expect(store.homeStatuses.map(status => status.id)).toEqual(['20', '19'])
    expect(store.homeStatuses[0]).toMatchObject({ content: 'edited' })
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
