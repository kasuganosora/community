import { describe, it, expect, vi } from 'vitest'
import { usePaginatedList } from '../usePaginatedList'
import { mergeById, hasMoreAfter } from '@/shared/utils/pagination'
import { readListSnapshot, saveListSnapshot } from '@/shared/utils/listMemory'

const page = (...ids: number[]) => ids.map(id => ({ id: String(id) }))

describe('mergeById', () => {
  it('appends only unseen items', () => {
    expect(mergeById(page(3, 2), page(2, 1)).map(i => i.id)).toEqual(['3', '2', '1'])
  })
  it('returns the same array when nothing is new', () => {
    const a = page(1)
    expect(mergeById(a, page(1))).toBe(a)
  })
})

describe('hasMoreAfter', () => {
  it('is true only for a full page', () => {
    expect(hasMoreAfter(20, 20)).toBe(true)
    expect(hasMoreAfter(3, 20)).toBe(false)
  })
})

describe('usePaginatedList', () => {
  it('clears retained rows and cached new-post hints when a head poll is forbidden', async () => {
    const fetcher = vi.fn().mockResolvedValue(page(20, 19))
    const list = usePaginatedList(fetcher, 20, 8, { prefetchNewer: true })
    await list.refresh()
    list.applyStreamed({ event: 'update', id: '21' })
    fetcher.mockRejectedValue({ isAxiosError: true, response: { status: 403 } })
    await list.catchUpHead()
    expect(list.items.value).toEqual([])
    expect(list.newer.value).toEqual([])
    expect(list.hasMore.value).toBe(false)
  })
  it('starts the new scope immediately while its previous request is still pending', async () => {
    let old!: (rows: { id: string }[]) => void
    let fresh!: (rows: { id: string }[]) => void
    const fetcher = vi.fn().mockImplementationOnce(() => new Promise(resolve => { old = resolve }))
      .mockImplementationOnce(() => new Promise(resolve => { fresh = resolve }))
    const list = usePaginatedList(fetcher)
    const previous = list.refresh()
    list.reset()
    const current = list.refresh()
    expect(fetcher).toHaveBeenCalledTimes(2)
    old(page(1)); await previous
    expect(list.items.value).toEqual([])
    expect(list.loading.value).toBe(true)
    const joined = list.refresh()
    expect(fetcher).toHaveBeenCalledTimes(2)
    fresh(page(2)); await current; await joined
    expect(list.items.value).toEqual(page(2))
    expect(list.loading.value).toBe(false)
  })
  it('abandons a queued reveal when the list scope changes', async () => {
    const fetcher = vi.fn().mockResolvedValue(page(20))
    const list = usePaginatedList(fetcher, 20, 8, { prefetchNewer: true })
    await list.refresh()
    let finish!: (rows: { id: string }[]) => void
    fetcher.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const read = list.revalidate()
    list.applyStreamed({ event: 'update', id: '21' })
    const reveal = list.revealNewer()
    list.reset()
    finish(page(21, 20))
    await read; await reveal
    expect(list.items.value).toEqual([])
    expect(list.newer.value).toEqual([])
    expect(fetcher).toHaveBeenCalledTimes(2)
  })
  it('reveals new posts after the current window read without a parallel fetch', async () => {
    const old = { id: '20', created_at: '2026-10-26T12:00:00.000Z' }
    const fresh = { id: '21', created_at: '2026-10-26T13:00:00.000Z' }
    const fetcher = vi.fn().mockResolvedValue([old])
    const list = usePaginatedList(fetcher, 20, 8, { prefetchNewer: true })
    await list.refresh()
    let finish!: (rows: typeof old[]) => void
    fetcher.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const read = list.revalidate()
    list.applyStreamed({ event: 'update', id: '21' })
    const reveal = list.revealNewer()
    finish([fresh, old])
    await read; await reveal
    expect(list.items.value.map(row => row.id)).toEqual(['21', '20'])
    expect(list.newer.value).toEqual([])
    expect(fetcher).toHaveBeenCalledTimes(2)
  })
  it('keeps deletes and live new-post hints received during a window read', async () => {
    const rows = [
      { id: '20', created_at: '2026-10-26T12:00:00.000Z' },
      { id: '19', created_at: '2026-10-26T11:00:00.000Z' },
    ]
    const fetcher = vi.fn().mockResolvedValue(rows)
    const list = usePaginatedList(fetcher)
    await list.refresh()
    let finish!: (rows: { id: string; created_at: string }[]) => void
    fetcher.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const pending = list.revalidate()
    list.applyStreamed({ event: 'delete', id: '19' })
    list.applyStreamed({ event: 'update', id: '21' })
    finish(rows)
    await pending
    expect(list.items.value.map(row => row.id)).toEqual(['20'])
    expect(list.newer.value).toEqual([{ id: '21' }])
    await list.refresh()
    expect(list.items.value.map(row => row.id)).toEqual(['20', '19'])
  })

  it('invalidates a cached new-post page when a streamed deletion arrives', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(page(20, 19)).mockResolvedValueOnce(page(21, 20, 19)).mockResolvedValueOnce(page(21, 19))
    const list = usePaginatedList(fetcher, 20, 8, { prefetchNewer: true })
    await list.refresh()
    list.newer.value = [{ id: '21' }]
    await Promise.resolve(); await Promise.resolve()
    list.applyStreamed({ event: 'delete', id: '20' })
    await list.revealNewer()
    expect(list.items.value.map(row => row.id)).toEqual(['21', '19'])
  })
  it('restores publication-ordered pages after eviction and snapshot hydration', async () => {
    const at = Date.parse('2026-10-26T12:00:00.000Z')
    const rows = Array.from({ length: 100 }, (_, i) => ({ id: String(400 - i * 2), created_at: new Date(at - i * 60000).toISOString() }))
    rows.push({ id: '401', created_at: '2026-10-25T12:00:00.000Z' })
    const fetcher = vi.fn(async (cursor: string | undefined, limit: number) => {
      const start = cursor ? rows.findIndex(row => row.id === cursor) + 1 : 0
      return rows.slice(start, start + limit)
    })
    const list = usePaginatedList(fetcher, 20, 2)
    await list.refresh()
    for (let i = 0; i < 3; i++) await list.loadMore()
    expect(list.items.value).toEqual(rows.slice(40, 80))
    saveListSnapshot('publication-page-review', list.items.value, list.hasMore.value, list.windowState())
    const snapshot = readListSnapshot<(typeof rows)[number]>('publication-page-review')!
    const restored = usePaginatedList(fetcher, 20, 2)
    restored.hydrate(snapshot.items, snapshot.hasMore, snapshot.window)
    await restored.loadPrevious()
    expect(restored.items.value).toEqual(rows.slice(20, 60))
    await restored.loadPrevious()
    expect(restored.items.value).toEqual(rows.slice(0, 40))
    expect(restored.hasPrevious.value).toBe(false)
    await restored.loadMore()
    expect(restored.items.value).toEqual(rows.slice(20, 60))
    await restored.loadPrevious()
    expect(restored.items.value).toEqual(rows.slice(0, 40))
    expect(fetcher.mock.calls.some(([cursor]) => cursor === '401')).toBe(false)
  })
  it('loads first page then the next with max_id', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(page(5, 4))
      .mockResolvedValueOnce(page(3))
    const list = usePaginatedList(fetcher, 2)
    await list.refresh()
    expect(list.hasMore.value).toBe(true)
    await list.loadMore()
    expect(fetcher).toHaveBeenLastCalledWith('4', 2)
    expect(list.items.value.map(i => i.id)).toEqual(['5', '4', '3'])
    expect(list.hasMore.value).toBe(false)
    await list.loadMore()
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('stops when the next page adds no new items', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(page(5, 4))
      .mockResolvedValueOnce(page(4, 5))
    const list = usePaginatedList(fetcher, 2)
    await list.refresh()
    await list.loadMore()
    expect(list.items.value.map(item => item.id)).toEqual(['5', '4'])
    expect(list.hasMore.value).toBe(false)
  })

  it('shares an in-flight refresh instead of opening a second read', async () => {
    let resolveOld: (v: { id: string }[]) => void = () => {}
    const fetcher = vi.fn().mockImplementationOnce(() => new Promise(r => { resolveOld = r }))
    const list = usePaginatedList(fetcher, 20)
    const old = list.refresh()
    const again = list.refresh()
    expect(fetcher).toHaveBeenCalledTimes(1)
    resolveOld(page(1))
    await old
    await again
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(list.items.value.map(i => i.id)).toEqual(['1'])
  })

  it('keeps the current page until a refresh resolves', async () => {
    let resolveNext: (v: { id: string }[]) => void = () => {}
    const fetcher = vi.fn()
      .mockResolvedValueOnce(page(1, 2))
      .mockImplementationOnce(() => new Promise(r => { resolveNext = r }))
    const list = usePaginatedList(fetcher, 20)
    await list.refresh()
    const pending = list.refresh()
    expect(list.items.value.map(i => i.id)).toEqual(['1', '2'])
    expect(list.loading.value).toBe(true)
    resolveNext(page(9))
    await pending
    expect(list.items.value.map(i => i.id)).toEqual(['9'])
  })

  it('keeps items when a refresh fails', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(page(1))
      .mockRejectedValueOnce(new Error('boom'))
    const list = usePaginatedList(fetcher, 20)
    await list.refresh()
    await list.refresh()
    expect(list.items.value.map(i => i.id)).toEqual(['1'])
    expect(list.error.value).toBe('boom')
    expect(list.loading.value).toBe(false)
  })

  it('exposes errors', async () => {
    const list = usePaginatedList(() => Promise.reject(new Error('boom')), 20)
    await list.refresh()
    expect(list.error.value).toBe('boom')
    expect(list.loading.value).toBe(false)
  })
})

describe('relationship cursor pagination', () => {
  it('uses the server cursor instead of a displayed status id, including short pages', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce({ items: page(999), nextCursor: '42' })
      .mockResolvedValueOnce({ items: page(888, 777) })
    const list = usePaginatedList(fetcher, 2)
    await list.refresh()
    expect(list.hasMore.value).toBe(true)
    await list.loadMore()
    expect(fetcher).toHaveBeenLastCalledWith('42', 2)
    expect(list.items.value.map(i => i.id)).toEqual(['999', '888', '777'])
    expect(list.hasMore.value).toBe(false)
  })
  it('does not open a second refresh while the first is in flight', async () => {
    let resolveOld!: (value: unknown) => void
    const fetcher = vi.fn()
      .mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
      .mockResolvedValueOnce({ items: page(888) })
    const list = usePaginatedList(fetcher, 2)
    const old = list.refresh()
    const again = list.refresh()
    expect(fetcher).toHaveBeenCalledTimes(1)
    resolveOld({ items: page(111), nextCursor: '7' })
    await old
    await again
    await list.loadMore()
    expect(fetcher).toHaveBeenLastCalledWith('7', 2)
  })
})


describe('pagination after removing an item', () => {
  it('retains the relationship cursor when the last visible item is removed', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce({ items: page(900, 800), nextCursor: '42' })
      .mockResolvedValueOnce({ items: page(700) })
    const list = usePaginatedList(fetcher, 2)
    await list.refresh()
    list.items.value = list.items.value.filter(i => i.id !== '800')
    await list.loadMore()
    expect(fetcher).toHaveBeenLastCalledWith('42', 2)
    expect(list.items.value.map(i => i.id)).toEqual(['900', '700'])
    expect(list.hasMore.value).toBe(false)
  })
})

describe('scope transitions and nonadvancing cursors', () => {
  it('clears old-scope content immediately and rejects its pending page', async () => {
    let resolve!: (rows: { id: string }[]) => void
    const fetcher = vi.fn().mockResolvedValueOnce(page(5, 4)).mockImplementationOnce(() => new Promise(r => { resolve = r }))
    const list = usePaginatedList(fetcher, 2)
    await list.refresh()
    const old = list.loadMore()
    list.reset()
    expect(list.items.value).toEqual([])
    resolve(page(3, 2)); await old
    expect(list.items.value).toEqual([])
    expect(list.loading.value).toBe(false)
  })
  it('stops an unchanged cursor so a visible sentinel cannot refetch forever', async () => {
    const fetcher = vi.fn().mockResolvedValue({ items: page(5, 4), nextCursor: '4' })
    const list = usePaginatedList(fetcher, 2)
    await list.refresh(); await list.loadMore(); await list.loadMore()
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(list.items.value.map(s => s.id)).toEqual(['5', '4'])
    expect(list.hasMore.value).toBe(false)
  })
})

describe('newer prefetch', () => {
  it('fetches the first page when newer ids appear and reveals it without a second request', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(page(20))
      .mockResolvedValueOnce(page(21, 20))
    const list = usePaginatedList(fetcher, 2, 8, { prefetchNewer: true })
    await list.refresh()
    list.newer.value = [{ id: '21' }]
    await Promise.resolve()
    await Promise.resolve()
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(fetcher).toHaveBeenLastCalledWith(undefined, 2)
    expect(list.items.value.map(item => item.id)).toEqual(['20'])
    await list.revealNewer()
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(list.items.value.map(item => item.id)).toEqual(['21', '20'])
    expect(list.newer.value).toEqual([])
  })

  it('reuses an in-flight prefetch when revealing', async () => {
    let resolve!: (rows: { id: string }[]) => void
    const fetcher = vi.fn()
      .mockResolvedValueOnce(page(20))
      .mockImplementationOnce(() => new Promise(r => { resolve = r }))
    const list = usePaginatedList(fetcher, 2, 8, { prefetchNewer: true })
    await list.refresh()
    list.newer.value = [{ id: '21' }]
    const reveal = list.revealNewer()
    expect(fetcher).toHaveBeenCalledTimes(2)
    resolve(page(21, 20))
    await reveal
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(list.items.value.map(item => item.id)).toEqual(['21', '20'])
  })

  it('falls back to a fresh fetch when prefetch fails', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(page(20))
      .mockRejectedValueOnce(new Error('prefetch'))
      .mockResolvedValueOnce(page(21, 20))
    const list = usePaginatedList(fetcher, 2, 8, { prefetchNewer: true })
    await list.refresh()
    list.newer.value = [{ id: '21' }]
    await Promise.resolve()
    await Promise.resolve()
    expect(list.error.value).toBeNull()
    await list.revealNewer()
    expect(fetcher).toHaveBeenCalledTimes(3)
    expect(list.items.value.map(item => item.id)).toEqual(['21', '20'])
    expect(list.error.value).toBeNull()
  })

  it('follows a replacement prefetch when a newer id arrives while revealing', async () => {
    let resolveFirst!: (rows: { id: string }[]) => void
    let resolveSecond!: (rows: { id: string }[]) => void
    const fetcher = vi.fn()
      .mockResolvedValueOnce(page(20))
      .mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve }))
      .mockImplementationOnce(() => new Promise(resolve => { resolveSecond = resolve }))
    const list = usePaginatedList(fetcher, 2, 8, { prefetchNewer: true })
    await list.refresh()
    list.newer.value = [{ id: '21' }]
    const reveal = list.revealNewer()
    list.newer.value = [{ id: '22' }, { id: '21' }]
    expect(fetcher).toHaveBeenCalledTimes(3)
    resolveFirst(page(21, 20))
    await Promise.resolve()
    expect(list.items.value.map(item => item.id)).toEqual(['20'])
    resolveSecond(page(22, 21))
    await reveal
    expect(fetcher).toHaveBeenCalledTimes(3)
    expect(list.items.value.map(item => item.id)).toEqual(['22', '21'])
  })
})

describe('bounded bidirectional timeline pages', () => {
  const all = Array.from({ length: 5000 }, (_, i) => ({ id: String(5000 - i) }))
  const fetchPage = async (cursor: string | undefined, limit: number) => all.filter(row => !cursor || BigInt(row.id) < BigInt(cursor)).slice(0, limit)
  it('keeps at most eight pages after 5000 rows and can reload an evicted page in order', async () => {
    const list = usePaginatedList(fetchPage, 20, 8)
    await list.refresh()
    for (let i = 1; i < 250; i++) { await list.loadMore(); expect(list.items.value.length).toBeLessThanOrEqual(160) }
    expect(list.items.value[0]?.id).toBe('160')
    expect(list.hasPrevious.value).toBe(true)
    await list.loadPrevious()
    expect(list.items.value[0]?.id).toBe('180')
    expect(list.items.value.at(-1)?.id).toBe('21')
    await list.loadMore()
    expect(list.items.value[0]?.id).toBe('160')
    expect(list.items.value.at(-1)?.id).toBe('1')
  })
  it('protects an actively edited page until its pin is released', async () => {
    const list = usePaginatedList(fetchPage, 20, 8)
    await list.refresh(); for (let i = 1; i < 8; i++) await list.loadMore()
    list.setPinnedIds(['5000']); await list.loadMore()
    expect(list.items.value[0]?.id).toBe('5000')
    expect(list.hasMore.value).toBe(false)
    expect(list.error.value).toContain('编辑')
    list.setPinnedIds([]); await list.loadMore()
    expect(list.items.value[0]?.id).toBe('4980')
  })
  it('does not load the next page while a window read is in flight', async () => {
    let release!: (rows: { id: string }[]) => void
    const fetcher = vi.fn()
      .mockResolvedValueOnce(page(20, 19))
      .mockImplementationOnce(() => new Promise(resolve => { release = resolve }))
    const list = usePaginatedList(fetcher, 2, 8)
    await list.refresh()
    const pending = list.revalidate()
    await list.loadMore()
    expect(fetcher).toHaveBeenCalledTimes(2)
    release(page(20, 19))
    fetcher.mockResolvedValueOnce(page(19))
    await pending
    expect(fetcher).toHaveBeenCalledTimes(3)
  })
  it('does not start a second window read while one is in flight, and a streamed id does not fetch', async () => {
    let release!: (rows: { id: string }[]) => void
    const fetcher = vi.fn()
      .mockResolvedValueOnce(page(20, 19))
      .mockImplementationOnce(() => new Promise(resolve => { release = resolve }))
    const list = usePaginatedList(fetcher, 20, 8)
    await list.refresh()
    const first = list.revalidate()
    const second = list.revalidate()
    expect(fetcher).toHaveBeenCalledTimes(2)
    release(page(20))
    fetcher.mockResolvedValueOnce(page(20))
    await first
    await second
    expect(fetcher).toHaveBeenCalledTimes(3)
    list.applyStreamed({ event: 'update', id: '21' })
    list.applyStreamed({ event: 'delete', id: '19' })
    expect(fetcher).toHaveBeenCalledTimes(3)
    expect(list.newer.value.map(row => row.id)).toEqual(['21'])
    expect(list.items.value.map(row => row.id)).toEqual(['20'])
  })
  it('does not pill an id already shown inside a reply or boost', async () => {
    const fetcher = vi.fn().mockResolvedValue([{ id: '20', reply: { id: '13' } }, { id: '19', reblog: { id: '18' } }])
    const list = usePaginatedList(fetcher, 20, 8)
    await list.refresh()
    list.applyStreamed({ event: 'update', id: '13' })
    list.applyStreamed({ event: 'update', id: '18' })
    list.applyStreamed({ event: 'update', id: '21' })
    expect(list.newer.value.map(row => row.id)).toEqual(['21'])
    expect(list.items.value.map(row => row.id)).toEqual(['20', '19'])
  })
  it('does not let a late revalidation restore a previous scope', async () => {
    let finish!: (rows: {id:string}[]) => void
    const fetcher = vi.fn().mockResolvedValueOnce(page(20, 19)).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const list = usePaginatedList(fetcher, 20, 8)
    await list.refresh(); const pending = list.revalidate(); list.reset()
    finish(page(21, 20)); await pending
    expect(list.items.value).toEqual([]); expect(list.newer.value).toEqual([])
  })
})


describe('revealing a snapshot', () => {
  it('retains hints that arrive during the clicked head read, even beyond the hint cap', async () => {
    let finish!: (rows: { id: string }[]) => void
    const fetcher = vi.fn().mockResolvedValueOnce([{ id: '1' }]).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const list = usePaginatedList(fetcher, 2, 8, { prefetchNewer: true })
    await list.refresh()
    list.applyStreamed({ event: 'update', id: '2' })
    const reveal = list.revealNewer()
    await Promise.resolve()
    for (const id of ['3', '4', '5']) list.applyStreamed({ event: 'update', id })
    finish([{ id: '2' }, { id: '1' }])
    expect(await reveal).toBe(true)
    expect(list.items.value.map(row => row.id)).toEqual(['2', '1'])
    expect(list.newer.value).toEqual([{ id: '4' }, { id: '5' }])
  })
  it('does not apply a previous scope prefetch or consume the new scope hints', async () => {
    let finish!: (rows: { id: string }[]) => void
    const fetcher = vi.fn().mockResolvedValueOnce([{ id: '1' }]).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const list = usePaginatedList(fetcher, 2, 8, { prefetchNewer: true })
    await list.refresh()
    list.newer.value = [{ id: '2' }]
    const reveal = list.revealNewer()
    list.reset()
    list.hydrate([{ id: '10' }], false)
    list.applyStreamed({ event: 'update', id: '11' })
    finish([{ id: '2' }, { id: '1' }])
    expect(await reveal).toBe(false)
    expect(list.items.value).toEqual([{ id: '10' }])
    expect(list.newer.value).toEqual([{ id: '11' }])
    expect(fetcher).toHaveBeenCalledTimes(2)
  })
})


it('invalidates a ready head when an edited post is only in that pending page', async () => {
  const fetcher = vi.fn().mockResolvedValueOnce([{ id: '1', content: 'old' }])
    .mockResolvedValueOnce([{ id: '2', content: 'before edit' }])
    .mockResolvedValueOnce([{ id: '2', content: 'after edit' }])
  const list = usePaginatedList(fetcher, 20, 8, { prefetchNewer: true })
  await list.refresh()
  list.newer.value = [{ id: '2' }]
  await Promise.resolve(); await Promise.resolve()
  list.applyStreamed({ event: 'status.update', id: '2' })
  await list.revealNewer()
  expect(list.items.value).toEqual([{ id: '2', content: 'after edit' }])
  expect(fetcher).toHaveBeenCalledTimes(3)
})
