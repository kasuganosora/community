import { describe, it, expect, vi, beforeEach } from 'vitest'
import { effectScope } from 'vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { applyStatusUpdate, useStatusActions, pendingActions } from '../useStatusActions'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { MessagePlugin } from '@/shared/ui/feedback'

vi.mock('@/domains/post/infrastructure/postApi', () => ({
  postApi: { favourite: vi.fn(), unfavourite: vi.fn(), reblog: vi.fn(), unreblog: vi.fn(), bookmark: vi.fn(), unbookmark: vi.fn() },
}))
vi.mock('@/shared/ui/feedback', () => ({ MessagePlugin: { error: vi.fn(), success: vi.fn() }, preloadFeedback: vi.fn() }))

const mk = (o: Partial<MastodonStatus> = {}) => ({ id: '1', favourited: false, favourites_count: 5, reblogged: false, reblogs_count: 2, bookmarked: false, ...o } as MastodonStatus)
function deferred<T>() { let resolve!: (v: T) => void, reject!: (e: unknown) => void; const promise = new Promise<T>((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
const flush = () => new Promise(r => setTimeout(r, 0))

describe('optimistic status actions', () => {
  beforeEach(() => { vi.clearAllMocks(); pendingActions.clear() })

  it('flips state and count immediately on every copy, including reblog wrappers and other pages', async () => {
    const d = deferred<MastodonStatus>()
    vi.mocked(postApi.favourite).mockReturnValueOnce(d.promise)
    const timeline = [mk(), { id: '9', reblog: mk() } as MastodonStatus]
    const notifications = [mk()]
    const scope = effectScope()
    const actions = scope.run(() => {
      useStatusActions((id, u) => applyStatusUpdate(notifications, id, u))
      return useStatusActions((id, u) => applyStatusUpdate(timeline, id, u))
    })!
    const p = actions.toggleFavourite(timeline[1].reblog!)
    for (const s of [timeline[0], timeline[1].reblog!, notifications[0]]) {
      expect(s.favourited).toBe(true)
      expect(s.favourites_count).toBe(6)
    }
    expect(pendingActions.has('fav:1')).toBe(true)
    d.resolve(mk({ favourited: true, favourites_count: 7 }))
    await p
    expect(timeline[0].favourites_count).toBe(7)
    expect(notifications[0].favourites_count).toBe(7)
    expect(pendingActions.size).toBe(0)
    scope.stop()
  })

  it('rolls back and shows a toast when the request fails', async () => {
    vi.mocked(postApi.reblog).mockRejectedValueOnce(Object.assign(new Error('Too many requests'), { status: 429 }))
    const list = [mk()]
    const scope = effectScope()
    const { toggleReblog } = scope.run(() => useStatusActions((id, u) => applyStatusUpdate(list, id, u)))!
    const p = toggleReblog(list[0])
    expect(list[0].reblogged).toBe(true)
    expect(list[0].reblogs_count).toBe(3)
    await p
    expect(list[0].reblogged).toBe(false)
    expect(list[0].reblogs_count).toBe(2)
    expect(MessagePlugin.error).toHaveBeenCalledTimes(1)
    scope.stop()
  })

  it('serializes rapid toggles and ends in the last requested state', async () => {
    const first = deferred<MastodonStatus>()
    vi.mocked(postApi.favourite).mockReturnValueOnce(first.promise)
    vi.mocked(postApi.unfavourite).mockResolvedValue(mk({ favourited: false, favourites_count: 5 }))
    const list = [mk()]
    const scope = effectScope()
    const { toggleFavourite } = scope.run(() => useStatusActions((id, u) => applyStatusUpdate(list, id, u)))!
    const p = toggleFavourite(list[0])          // like
    void toggleFavourite(list[0])               // unlike
    void toggleFavourite(list[0])               // like
    void toggleFavourite(list[0])               // unlike (final)
    expect(list[0].favourited).toBe(false)
    expect(list[0].favourites_count).toBe(5)
    expect(postApi.favourite).toHaveBeenCalledTimes(1)
    first.resolve(mk({ favourited: true, favourites_count: 6 }))
    await p
    await flush()
    expect(postApi.unfavourite).toHaveBeenCalledTimes(1)
    expect(list[0].favourited).toBe(false)
    expect(list[0].favourites_count).toBe(5)
    scope.stop()
  })

  it('sends nothing extra when rapid toggles return to the confirmed state', async () => {
    const first = deferred<MastodonStatus>()
    vi.mocked(postApi.bookmark).mockReturnValueOnce(first.promise)
    vi.mocked(postApi.unbookmark).mockResolvedValue(mk({ bookmarked: false }))
    const list = [mk()]
    const scope = effectScope()
    const { toggleBookmark } = scope.run(() => useStatusActions((id, u) => applyStatusUpdate(list, id, u)))!
    const p = toggleBookmark(list[0]); void toggleBookmark(list[0])
    expect(list[0].bookmarked).toBe(false)
    first.resolve(mk({ bookmarked: true }))
    await p
    expect(postApi.unbookmark).toHaveBeenCalledTimes(1)
    expect(list[0].bookmarked).toBe(false)
    scope.stop()
  })
})
