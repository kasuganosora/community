import { describe, expect, it, vi } from 'vitest'
import { revalidateTimeline } from './revalidateTimeline'
const rows = (...ids: number[]) => ids.map(id => ({ id: String(id) }))
describe('HTTP-authoritative timeline windows', () => {
  it('removes revoked rows and catches delayed lower-ID commits without inserting above the reader', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(rows(120, 100, 95, 80)).mockResolvedValueOnce(rows(100, 95, 80))
    const result = await revalidateTimeline(rows(100, 90, 80), fetcher, () => true)
    expect(result?.statuses).toEqual(rows(100, 80))
    expect(result?.newer).toEqual(rows(120, 95))
    expect(fetcher).toHaveBeenLastCalledWith('101', 20)
  })
  it('rereads deep windows independently of a busy new head and preserves full ID precision', async () => {
    const first = '18446744073709551610'
    const fetcher = vi.fn().mockResolvedValueOnce([{ id: '18446744073709551615' }]).mockResolvedValueOnce([{ id: first }])
    const result = await revalidateTimeline([{ id: first }], fetcher, () => true)
    expect(fetcher).toHaveBeenLastCalledWith('18446744073709551611', 20)
    expect(result?.statuses).toEqual([{ id: first }])
  })
  it('discards a late scope response before requesting the next page', async () => {
    const fetcher = vi.fn().mockResolvedValue(rows(3))
    expect(await revalidateTimeline(rows(2, 1), fetcher, () => false)).toBeNull()
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
})

it('clears private cached cards when a list becomes forbidden instead of treating it as an offline gap', async () => {
  const forbidden = { isAxiosError: true, response: { status: 403 } }
  const result = await revalidateTimeline(rows(20, 19), vi.fn().mockRejectedValue(forbidden), () => true)
  expect(result).toEqual({ statuses: [], newer: [] })
})

it('does not label previously read, evicted head pages as new posts while browsing a deep window', async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(rows(100, 99)).mockResolvedValueOnce(rows(50, 49))
  const result = await revalidateTimeline(rows(50, 49), fetcher, () => true, ['100', '99'])
  expect(result).toEqual({ statuses: rows(50, 49), newer: [] })
})

it('uses fresh HTTP bodies over stale cross-page edits but preserves mutations that finish during the read', async () => {
  const { editedStatuses } = await import('@/domains/post/presentation/composables/useStatusActions')
  const stale = { id: '20', content: 'old', favourited: false } as import('@/shared/types/mastodon').MastodonStatus
  editedStatuses.set('20', stale)
  await revalidateTimeline(rows(20), vi.fn().mockResolvedValue(rows(20)), () => true)
  expect(editedStatuses.has('20')).toBe(false)
  editedStatuses.set('20', stale)
  let finish!: (value: { id: string }[]) => void
  const fetcher = vi.fn().mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValue(rows(20))
  const pending = revalidateTimeline(rows(20), fetcher, () => true)
  editedStatuses.get('20')!.favourited = true
  finish(rows(20)); await pending
  expect(editedStatuses.get('20')?.favourited).toBe(true)
  editedStatuses.clear()
})

it('does not evict valid old tail cards when delayed inserts fill more than the old page count', async () => {
  const current = rows(1000, 900, 800)
  const data = Array.from({ length: 201 }, (_, i) => ({ id: String(1000 - i) }))
  const fetcher = vi.fn(async (cursor: string | undefined, limit: number) => data.filter(row => !cursor || BigInt(row.id) < BigInt(cursor)).slice(0, limit))
  const result = await revalidateTimeline(current, fetcher, () => true, ['1000', '900', '800'])
  expect(result?.statuses).toEqual(current)
  expect(result?.newer).toHaveLength(20)
  expect(fetcher).toHaveBeenCalledTimes(4) // head + one bounded verification per sparse old ID
})

it('does not queue a head row that is already on screen inside a reply, boost, or quote', async () => {
  const { refreshTimelineHead } = await import('./revalidateTimeline')
  const current = [{ id: '2', reply: { id: '1' } }, { id: '4', reblog: { id: '3' } }, { id: '6', quote: { id: '5' } }]
  const fetcher = vi.fn().mockResolvedValue([{ id: '1' }, { id: '3' }, { id: '5' }, { id: '2' }, { id: '7' }])
  const result = await refreshTimelineHead(current, fetcher, () => true, ['2', '4', '6'])
  expect(result?.newer).toEqual([{ id: '7' }])
  expect(result?.statuses.map(status => status.id)).toEqual(['2', '4', '6'])
})

it('does not treat an already-visible parent as a new row when reconciling the window', async () => {
  const current = [{ id: '20', reply: { id: '13' } }, { id: '19' }]
  const fetcher = vi.fn().mockResolvedValueOnce([{ id: '13' }, { id: '20' }, { id: '19' }]).mockResolvedValueOnce([{ id: '20' }, { id: '19' }])
  const result = await revalidateTimeline(current, fetcher, () => true, ['20', '19'])
  expect(result?.statuses.map(status => status.id)).toEqual(['20', '19'])
  expect(result?.newer).toEqual([])
})

it('refreshes the head in one request and keeps the loaded tail', async () => {
  const { refreshTimelineHead } = await import('./revalidateTimeline')
  const fetcher = vi.fn().mockResolvedValue([{ id: '30', content: 'fresh' }, { id: '20', content: 'edited' }])
  const result = await refreshTimelineHead([{ id: '20', content: 'old' }, { id: '10', content: 'tail' }], fetcher, () => true, ['20'])
  expect(fetcher).toHaveBeenCalledTimes(1)
  expect(fetcher).toHaveBeenCalledWith(undefined, 20)
  expect(result?.newer).toEqual([{ id: '30' }])
  expect(result?.statuses).toEqual([{ id: '20', content: 'edited' }, { id: '10', content: 'tail' }])
})

it('detects delayed lower-ID head content even when the original head page has been evicted', async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(rows(100, 95, 90)).mockResolvedValueOnce(rows(50, 49))
  const result = await revalidateTimeline(rows(50, 49), fetcher, () => true, ['100', '90'])
  expect(result?.statuses).toEqual(rows(50, 49))
  expect(result?.newer).toEqual(rows(95))
})
