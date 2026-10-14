import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope } from 'vue'
import { discoveryApi, type DiscoveryPage } from '../../../infrastructure/discoveryApi'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { discoveryPreference, invalidateDiscovery } from '../discoveryState'
import { useDiscoveryFeed } from '../useDiscoveryFeed'
import type { MastodonStatus } from '@/shared/types/mastodon'

const status = (id: string, author = 'author') => ({ id, account: { id: author } }) as MastodonStatus
const page = (ids: string[], next: string | null = null): DiscoveryPage => ({
  items: ids.map(id => ({ status: status(id) })), next_cursor: next, generation: 'snapshot-one', mode: 'rules',
  algorithm: 'rules-v1', preference: 'generic', preference_revision: 1, disabled: false,
})
const scopes: ReturnType<typeof effectScope>[] = []
function setup() { const scope = effectScope(); scopes.push(scope); return scope.run(() => useDiscoveryFeed())! }
beforeEach(() => { sessionEpoch.value++; discoveryPreference.value = { preference: 'generic', revision: 1 } })
afterEach(() => { scopes.splice(0).forEach(scope => scope.stop()); vi.restoreAllMocks(); vi.useRealTimers() })

describe('ranked discovery pagination', () => {
  it('retries a load-shedding 503 once and reports freshness', async () => {
    vi.useFakeTimers()
    const busy = { response: { status: 503, data: { retryable: true } } }
    const api = vi.spyOn(discoveryApi, 'statuses').mockRejectedValueOnce(busy).mockResolvedValueOnce(page(['1']))
    const feed = setup()
    const done = feed.refresh()
    await vi.advanceTimersByTimeAsync(1500)
    await done
    expect(api).toHaveBeenCalledTimes(2)
    expect(feed.error.value).toBeNull()
    expect(feed.isFresh()).toBe(true)
    await vi.advanceTimersByTimeAsync(5 * 60_000)
    expect(feed.isFresh()).toBe(false)
    vi.useRealTimers()
  })

  it('keeps server ranking and uses the opaque cursor rather than a status ID', async () => {
    const api = vi.spyOn(discoveryApi, 'statuses').mockResolvedValueOnce(page(['20', '80'], 'opaque-cursor')).mockResolvedValueOnce(page(['80', '10']))
    const feed = setup()
    await feed.refresh(); await feed.loadMore()
    expect(api.mock.calls[1]?.[0]).toBe('opaque-cursor')
    expect(feed.statuses.value.map(row => row.id)).toEqual(['20', '80', '10'])
    expect(feed.hasMore.value).toBe(false)
  })

  it.each(['cancel', 'dispose'] as const)('does not retry a superseded request after %s during the retry delay', async action => {
    vi.useFakeTimers()
    const api = vi.spyOn(discoveryApi, 'statuses')
      .mockRejectedValueOnce({ response: { status: 503, data: { retryable: true } } })
      .mockResolvedValueOnce(page(['new']))
    const old = setup(), request = old.refresh()
    await vi.advanceTimersByTimeAsync(0)
    if (action === 'dispose') scopes[0]!.stop()
    else old.cancel()
    const current = action === 'dispose' ? setup() : old
    await current.refresh()
    await vi.advanceTimersByTimeAsync(1500)
    await request
    expect(api).toHaveBeenCalledTimes(2)
    expect(api.mock.calls[0]![1]!.aborted).toBe(true)
    expect(current.statuses.value.map(row => row.id)).toEqual(['new'])
    expect(current.isFresh()).toBe(true)
  })

  it('invalidates a cached window immediately when account or language preference changes', async () => {
    vi.spyOn(discoveryApi, 'statuses').mockResolvedValue(page(['1'], 'more'))
    const feed = setup()
    await feed.refresh()
    expect(feed.isFresh()).toBe(true)
    invalidateDiscovery({ preference: 'generic', revision: 2, languages: ['ja'] })
    expect(feed.isFresh()).toBe(false)
    expect(feed.statuses.value).toEqual([])
    expect(feed.hasMore.value).toBe(false)
    vi.mocked(discoveryApi.statuses).mockResolvedValue({ ...page(['2']), preference_revision: 2 })
    await feed.refresh()
    expect(feed.isFresh()).toBe(true)
    sessionEpoch.value++
    expect(feed.isFresh()).toBe(false)
    expect(feed.statuses.value).toEqual([])
  })

  it('bounds automatic traversal of empty blocks and permits explicit continuation', async () => {
    const api = vi.spyOn(discoveryApi, 'statuses')
      .mockResolvedValueOnce(page([], 'one')).mockResolvedValueOnce(page([], 'two')).mockResolvedValueOnce(page([], 'three'))
      .mockResolvedValueOnce(page(['4']))
    const feed = setup()
    await feed.refresh(); await feed.loadMore()
    expect(api).toHaveBeenCalledTimes(3)
    expect(feed.autoPaused.value).toBe(true)
    expect(feed.hasMore.value).toBe(true)
    await feed.continueLoading()
    expect(api.mock.calls[3]?.[0]).toBe('three')
    expect(feed.statuses.value.map(row => row.id)).toEqual(['4'])
  })

  it('accepts the server current preference revision across empty-block continuation', async () => {
    vi.spyOn(discoveryApi, 'statuses')
      .mockResolvedValueOnce({ ...page([], 'more'), preference_revision: 2 })
      .mockResolvedValueOnce({ ...page(['7']), preference_revision: 2 })
    const feed = setup()
    await feed.refresh()
    expect(feed.statuses.value.map(row => row.id)).toEqual(['7'])
    expect(discoveryPreference.value?.revision).toBe(2)
  })

  it('asks for a refresh on expired snapshots without silently replacing the ranked list', async () => {
    const api = vi.spyOn(discoveryApi, 'statuses').mockResolvedValueOnce(page(['1'], 'old')).mockRejectedValueOnce({ response: { status: 409, data: { code: 'reset_required' } } })
    const feed = setup()
    await feed.refresh(); await feed.loadMore(); await feed.loadMore()
    expect(api).toHaveBeenCalledTimes(2)
    expect(feed.resetRequired.value).toBe(true)
    expect(feed.statuses.value.map(row => row.id)).toEqual(['1'])
  })

  it('discards late results after changing accounts', async () => {
    let finish!: (value: DiscoveryPage) => void
    vi.spyOn(discoveryApi, 'statuses').mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const feed = setup(), request = feed.refresh()
    sessionEpoch.value++
    finish(page(['private-old-session']))
    await request
    expect(feed.statuses.value).toEqual([])
    expect(feed.loading.value).toBe(false)
  })

  it('discards late results and old cursors after changing recommendation preference', async () => {
    let finish!: (value: DiscoveryPage) => void
    vi.spyOn(discoveryApi, 'statuses').mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const feed = setup(), request = feed.refresh()
    invalidateDiscovery({ preference: 'off', revision: 2 })
    finish(page(['old-preference']))
    await request
    expect(feed.statuses.value).toEqual([])
    expect(feed.resetRequired.value).toBe(true)
  })

  it('keeps cards on feedback failure, removes an author only after success, and expires pagination', async () => {
    const initial = page(['1', '2', '3'], 'more')
    initial.items[2]!.status = status('3', 'another-author')
    vi.spyOn(discoveryApi, 'statuses').mockResolvedValue(initial)
    vi.spyOn(discoveryApi, 'addFeedback').mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ preference: 'generic', revision: 2 })
    const feed = setup()
    await feed.refresh()
    await expect(feed.feedback(status('1'), 'hide_author')).rejects.toThrow('offline')
    expect(feed.statuses.value).toHaveLength(3)
    await feed.feedback(status('1'), 'hide_author')
    expect(feed.statuses.value.map(row => row.id)).toEqual(['3'])
    expect(feed.resetRequired.value).toBe(true)
    expect(feed.hasMore.value).toBe(false)
  })

  it.each(['hide_status', 'hide_author'] as const)('immediately removes quoted and reblogged hidden targets for %s', async action => {
    const target = status('hidden', 'hidden-author')
    const quote = { ...status('quote', 'another'), quote: target }
    const nested = { ...status('outer', 'third'), quote: { ...status('inner', 'fourth'), reblog: target } }
    const cycle = status('cycle', 'fifth'); cycle.quote = cycle
    vi.spyOn(discoveryApi, 'statuses').mockResolvedValue({ ...page([], 'next'), items: [target, quote, nested, cycle, status('allowed', 'allowed-author')].map(value => ({ status: value })) })
    vi.spyOn(discoveryApi, 'addFeedback').mockResolvedValue({ preference: 'generic', revision: 2 })
    const feed = setup()
    await feed.refresh(); await feed.feedback(target, action)
    expect(feed.statuses.value.map(row => row.id)).toEqual(['allowed'])
  })

  it('hides every row of the same remote actor and refetches from the head on the next refresh', async () => {
    const acct = (id: string, accountId: string, name: string) => ({ id, account: { id: accountId, acct: name } }) as MastodonStatus
    const first = { ...page([], 'cursor-1'), items: [acct('1', '114', 'kumori@bgme.me'), acct('2', '115', 'Kumori@bgme.me'), acct('3', '9', 'other@example.com')].map(value => ({ status: value })) }
    const api = vi.spyOn(discoveryApi, 'statuses').mockResolvedValueOnce(first).mockResolvedValueOnce({ ...page(['4']), preference_revision: 2 })
    vi.spyOn(discoveryApi, 'addFeedback').mockResolvedValue({ preference: 'generic', revision: 2 })
    const feed = setup()
    await feed.refresh()
    await feed.feedback(first.items[0]!.status, 'hide_author')
    expect(feed.statuses.value.map(row => row.id)).toEqual(['3'])
    expect(feed.isFresh()).toBe(false)
    await feed.refresh()
    expect(api.mock.calls[1]?.[0]).toBeUndefined()
    expect(feed.statuses.value.map(row => row.id)).toEqual(['4'])
  })

  it('preserves explicit one-page degraded mode and has no fabricated next cursor', async () => {
    vi.spyOn(discoveryApi, 'statuses').mockResolvedValue({ ...page(['1']), mode: 'chronological_fallback' })
    const feed = setup()
    await feed.refresh()
    expect(feed.mode.value).toBe('chronological_fallback')
    expect(feed.hasMore.value).toBe(false)
  })
})
