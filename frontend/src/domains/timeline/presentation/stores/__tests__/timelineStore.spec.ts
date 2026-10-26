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
  beforeEach(() => { setActivePinia(createPinia()); vi.resetAllMocks() })
  it('honors a new-post click queued behind load-more rather than treating it as navigation', async () => {
    const first = Array.from({ length: 20 }, (_, i) => ({ id: String(100 - i) })) as MastodonStatus[]
    const fresh = [{ id: '101' }, ...first.slice(0, 19)] as MastodonStatus[]
    const api = vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce(first)
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    let finish!: (rows: MastodonStatus[]) => void
    api.mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValue(fresh)
    const more = store.loadMoreHome()
    store.applyStreamed('home', { event: 'update', id: '101' })
    const reveal = store.revealNewer('home')
    finish([{ id: '80' }] as MastodonStatus[])
    await more; await reveal
    expect(store.homeStatuses).toEqual(fresh)
    expect(store.homeTimeline.newer).toEqual([])
    expect(api).toHaveBeenCalledTimes(3)
  })
  it('reveals new posts on the first click while a window read is in flight', async () => {
    const old = { id: '20', created_at: '2026-10-26T12:00:00.000Z' } as MastodonStatus
    const fresh = { id: '21', created_at: '2026-10-26T13:00:00.000Z' } as MastodonStatus
    const api = vi.mocked(timelineApi.getHomeTimeline).mockResolvedValue([old])
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    let finish!: (rows: MastodonStatus[]) => void
    api.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const read = store.checkNewer('home')
    store.applyStreamed('home', { event: 'update', id: '21' })
    const reveal = store.revealNewer('home')
    finish([fresh, old])
    await read; await reveal
    expect(store.homeStatuses.map(row => row.id)).toEqual(['21', '20'])
    expect(store.homeTimeline.newer).toEqual([])
    expect(api).toHaveBeenCalledTimes(2)
  })
  it('keeps streamed deletions and new-post hints when an older HTTP window finishes', async () => {
    const rows = [
      { id: '20', created_at: '2026-10-26T12:00:00.000Z' },
      { id: '19', created_at: '2026-10-26T11:00:00.000Z' },
    ] as MastodonStatus[]
    const api = vi.mocked(timelineApi.getHomeTimeline).mockResolvedValue(rows)
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    let finish!: (rows: MastodonStatus[]) => void
    api.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const pending = store.checkNewer('home')
    store.applyStreamed('home', { event: 'delete', id: '19' })
    store.applyStreamed('home', { event: 'update', id: '21' })
    finish(rows)
    await pending
    expect(store.homeStatuses.map(row => row.id)).toEqual(['20'])
    expect(store.homeTimeline.newer).toEqual([{ id: '21' }])
    // A stream delete removes this timeline's membership; the object can
    // legitimately return in a later authoritative read (e.g. after following).
    await store.fetchHomeTimeline()
    expect(store.homeStatuses.map(row => row.id)).toEqual(['20', '19'])
  })
  it.each(['home', 'public'] as const)('restores evicted %s pages using real publication boundaries', async type => {
    const at = Date.parse('2026-10-26T12:00:00.000Z')
    const rows = Array.from({ length: 220 }, (_, i) => ({ id: String(1000 - i * 2), created_at: new Date(at - i * 60000).toISOString() })) as MastodonStatus[]
    rows.push({ id: '1001', created_at: '2026-10-25T12:00:00.000Z' } as MastodonStatus)
    const api = vi.mocked(type === 'home' ? timelineApi.getHomeTimeline : timelineApi.getPublicTimeline)
    api.mockImplementation(async params => {
      const start = params?.max_id ? rows.findIndex(row => row.id === params.max_id) + 1 : 0
      return rows.slice(start, start + (params?.limit ?? 20))
    })
    const store = useTimelineStore()
    if (type === 'home') await store.fetchHomeTimeline()
    else await store.fetchPublicTimeline()
    for (let i = 0; i < 9; i++) {
      if (type === 'home') await store.loadMoreHome()
      else await store.loadMorePublic()
    }
    const state = type === 'home' ? store.homeTimeline : store.publicTimeline
    expect(state.statuses).toEqual(rows.slice(40, 200))
    await store.loadPrevious(type)
    expect(state.statuses).toEqual(rows.slice(20, 180))
    await store.loadPrevious(type)
    expect(state.statuses).toEqual(rows.slice(0, 160))
    expect(state.hasPrevious).toBe(false)
    expect(api.mock.calls.some(([params]) => params?.max_id === '1001')).toBe(false)
  })
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
  it('does not pill a status already visible as a reply parent, boost, or quote', async () => {
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([
      { id: '20', reply: { id: '13' } },
      { id: '19', reblog: { id: '18' } },
      { id: '17', quote: { id: '16' } },
    ] as MastodonStatus[])
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    store.applyStreamed('home', { event: 'update', id: '13' })
    store.applyStreamed('home', { event: 'update', id: '18' })
    store.applyStreamed('home', { event: 'update', id: '16' })
    store.applyStreamed('home', { event: 'update', id: '20' })
    store.applyStreamed('home', { event: 'status.update', id: '13' })
    expect(store.homeTimeline.newer).toEqual([])
    expect(store.homeStatuses.map(status => status.id)).toEqual(['20', '19', '17'])
    store.applyStreamed('home', { event: 'update', id: '21' })
    store.applyStreamed('home', { event: 'update', id: '21' })
    expect(store.homeTimeline.newer.map(status => status.id)).toEqual(['21'])
    expect(store.homeStatuses.map(status => status.id)).toEqual(['20', '19', '17'])
    store.applyStreamed('home', { event: 'delete', id: '13' })
    expect(store.homeStatuses.map(status => status.id)).toEqual(['20', '19', '17'])
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
  it('applies the home reread that replaces one cancelled by a tab switch', async () => {
    const visible = [
      { id: '240737', created_at: '2026-10-24T12:00:00.000Z', content: 'visible' },
      { id: '240700', created_at: '2026-10-24T11:00:00.000Z', content: 'older' },
    ] as MastodonStatus[]
    const api = vi.mocked(timelineApi.getHomeTimeline)
    api.mockResolvedValueOnce(visible)
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    let release!: (rows: MastodonStatus[]) => void
    api.mockImplementationOnce(() => new Promise(resolve => { release = resolve }))
    const stale = store.checkNewer('home')
    await Promise.resolve()
    store.cancelRevalidation()
    api.mockResolvedValue([{ id: '240800', created_at: '2026-10-24T13:00:00.000Z', content: 'new' }, ...visible] as MastodonStatus[])
    const fresh = store.checkNewer('home')
    release(visible)
    await stale
    await fresh
    expect(api.mock.calls.length).toBeGreaterThan(2)
    expect(store.homeStatuses.map(status => status.id)).toEqual(['240737', '240700'])
    expect(store.homeTimeline.newer.map(status => status.id)).toEqual(['240800'])
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


describe('explicit head refresh isolation', () => {
  beforeEach(() => { setActivePinia(createPinia()); vi.resetAllMocks() })
  it('keeps updates received during reveal after the snapshot is installed', async () => {
    let finish!: (rows: MastodonStatus[]) => void
    const api = vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '1' }] as MastodonStatus[])
      .mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    store.applyStreamed('home', { event: 'update', id: '2' })
    const reveal = store.revealNewer('home')
    await Promise.resolve()
    for (let i = 3; i < 25; i++) store.applyStreamed('home', { event: 'update', id: String(i) })
    finish([{ id: '2' }, { id: '1' }] as MastodonStatus[])
    expect(await reveal).toBe(true)
    expect(store.homeTimeline.newer).toHaveLength(20)
    expect(store.homeTimeline.newer.at(-1)?.id).toBe('24')
    expect(api).toHaveBeenCalledTimes(2)
  })
  it('does not replace retained rows after leaving the page while reveal is loading', async () => {
    let finish!: (rows: MastodonStatus[]) => void
    vi.mocked(timelineApi.getHomeTimeline).mockResolvedValueOnce([{ id: '1' }] as MastodonStatus[])
      .mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const store = useTimelineStore()
    await store.fetchHomeTimeline()
    store.applyStreamed('home', { event: 'update', id: '2' })
    const reveal = store.revealNewer('home')
    await Promise.resolve()
    store.cancelRevalidation()
    finish([{ id: '2' }, { id: '1' }] as MastodonStatus[])
    expect(await reveal).toBe(false)
    expect(store.homeStatuses.map(row => row.id)).toEqual(['1'])
    expect(store.homeTimeline.newer).toEqual([{ id: '2' }])
  })
})
