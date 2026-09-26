import { describe, it, expect, vi } from 'vitest'
import { usePaginatedList } from '../usePaginatedList'
import { mergeById, hasMoreAfter } from '@/shared/utils/pagination'

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

  it('ignores a stale response after refresh', async () => {
    let resolveOld: (v: { id: string }[]) => void = () => {}
    const fetcher = vi.fn()
      .mockImplementationOnce(() => new Promise(r => { resolveOld = r }))
      .mockResolvedValueOnce(page(9))
    const list = usePaginatedList(fetcher, 20)
    const old = list.refresh()
    await list.refresh()
    resolveOld(page(1))
    await old
    expect(list.items.value.map(i => i.id)).toEqual(['9'])
  })

  it('exposes errors', async () => {
    const list = usePaginatedList(() => Promise.reject(new Error('boom')), 20)
    await list.refresh()
    expect(list.error.value).toBe('boom')
    expect(list.loading.value).toBe(false)
  })
})
