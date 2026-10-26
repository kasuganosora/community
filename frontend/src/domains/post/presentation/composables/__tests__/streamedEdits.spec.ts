import { beforeEach, describe, expect, it, vi } from 'vitest'
import { invalidateSessionData } from '@/shared/api/sessionScope'
import type { MastodonStatus } from '@/shared/types/mastodon'

const api = vi.hoisted(() => ({ getStatus: vi.fn(), favourite: vi.fn(), unfavourite: vi.fn() }))
vi.mock('@/domains/post/infrastructure/postApi', () => ({ postApi: api }))
vi.mock('@/shared/ui/feedback', () => ({ preloadFeedback: vi.fn(), MessagePlugin: { error: vi.fn() } }))
import { editedStatuses, markStatusDeleted, refreshStreamedStatus, useStatusActions } from '../useStatusActions'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'

const row = (id: string, content: string) => ({ id, content, favourited: false, favourites_count: 0 }) as MastodonStatus
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}

describe('streamed edits', () => {
  beforeEach(() => { invalidateSessionData(); vi.resetAllMocks() })

  it('refreshes different old posts independently without losing the last same-post edit', async () => {
    const first = deferred<MastodonStatus>()
    api.getStatus.mockImplementation((id: string) => id === '1' ? first.promise : Promise.resolve(row('2', 'second')))
    const update = vi.fn()
    const pending = refreshStreamedStatus('1', update)
    await refreshStreamedStatus('2', update)
    expect(update).toHaveBeenCalledWith('2', expect.objectContaining({ content: 'second' }))
    refreshStreamedStatus('1', update)
    api.getStatus.mockResolvedValue(row('1', 'latest'))
    first.resolve(row('1', 'stale'))
    await pending
    expect(api.getStatus).toHaveBeenCalledTimes(3)
    expect(update).not.toHaveBeenCalledWith('1', expect.objectContaining({ content: 'stale' }))
    expect(editedStatuses.get('1')?.content).toBe('latest')
  })

  it('drops a late edit fetch after account switch or local deletion', async () => {
    const old = deferred<MastodonStatus>()
    api.getStatus.mockReturnValueOnce(old.promise)
    const update = vi.fn()
    const pending = refreshStreamedStatus('1', update)
    invalidateSessionData()
    old.resolve(row('1', 'previous account'))
    await pending
    expect(update).not.toHaveBeenCalled()
    expect(editedStatuses.size).toBe(0)
    const deleted = deferred<MastodonStatus>()
    api.getStatus.mockReturnValueOnce(deleted.promise)
    const again = refreshStreamedStatus('1', update)
    markStatusDeleted('1')
    deleted.resolve(row('1', 'deleted'))
    await again
    expect(update).not.toHaveBeenCalled()
  })

  it('preserves pending optimistic likes when the edit response has older flags', async () => {
    const liked = deferred<MastodonStatus>()
    api.favourite.mockReturnValueOnce(liked.promise)
    api.getStatus.mockResolvedValue(row('1', 'edited text'))
    const current = row('1', 'before edit')
    const actions = useStatusActions((id, updates) => { if (id === current.id) Object.assign(current, updates) })
    const pending = actions.toggleFavourite(current)
    await refreshStreamedStatus('1')
    expect(current.content).toBe('edited text')
    expect(current.favourited).toBe(true)
    expect(current.favourites_count).toBe(1)
    liked.resolve({ ...current, favourited: true, favourites_count: 1 })
    await pending
    expect(editedStatuses.get('1')?.favourited).toBe(true)
  })

  it('refreshes an edit on a retained tag/list card without a timeline request', async () => {
    const fetcher = vi.fn().mockResolvedValue([row('1', 'old')])
    const list = usePaginatedList<MastodonStatus>(fetcher)
    await list.refresh()
    api.getStatus.mockResolvedValue(row('1', 'new'))
    list.applyStreamed({ event: 'status.update', id: '1' })
    await vi.waitFor(() => expect(list.items.value[0]?.content).toBe('new'))
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(api.getStatus).toHaveBeenCalledWith('1')
    expect(list.newer.value).toEqual([])
  })
})
