import { refreshTimelineHead, representedStatusIDs, revalidateTimeline } from '@/shared/utils/revalidateTimeline'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { createNewerPrefetch, newerPrefetchKey } from '@/shared/utils/newerPrefetch'
import { ref, watch, getCurrentScope, onScopeDispose, type Ref } from 'vue'
import { mergeById, hasMoreAfter, type CursorPage } from '@/shared/utils/pagination'

import { t } from '@/shared/i18n'
export interface PaginationWindow { previousCursors: string[]; droppedHistory: boolean; knownHead?: string[] }

export interface PaginatedList<T> {
  items: Ref<T[]>
  newer: Ref<{ id: string }[]>
  hasPrevious: Ref<boolean>
  loadPrevious: () => Promise<void>
  revalidate: () => Promise<boolean>
  /** One head page: new ids join the pill, the loaded tail is not walked. */
  catchUpHead: () => Promise<boolean>
  /** Apply a streamed status id without fetching the timeline. */
  applyStreamed: (status: { event: 'update' | 'delete' | 'status.update', id: string }) => void
  revealNewer: () => Promise<void> | void
  setPinnedIds: (ids: string[]) => void
  loading: Ref<boolean>
  error: Ref<string | null>
  hasMore: Ref<boolean>
  /** Reload the first page. Existing items stay until the new page arrives. */
  refresh: () => Promise<void>
  /** Invalidate pending responses and clear data when account or list scope changes. */
  reset: () => void
  /** Fetch the next page using its server cursor (or last item id for array APIs). */
  loadMore: () => Promise<void>
  /** Restore a previously shown page without hitting the network. */
  hydrate: (items: T[], hasMore: boolean, window?: PaginationWindow) => void
  windowState: () => PaginationWindow
}

/**
 * Cursor pagination for Mastodon-style lists. Relationship endpoints supply
 * their own cursor; plain-array endpoints use the last item id.
 * Every request bumps a version counter so a slow response from an older
 * request (e.g. before a tab switch) can never overwrite newer data.
 */
export function usePaginatedList<T extends { id: string }>(
  fetchPage: (maxId: string | undefined, limit: number) => Promise<T[] | CursorPage<T>>,
  pageSize = 20,
  maxPages = Infinity,
  options?: { prefetchNewer?: boolean },
): PaginatedList<T> {
  const items = ref([]) as Ref<T[]>
  const newer = ref<{ id: string }[]>([])
  const hasPrevious = ref(false)
  const history: string[] = []
  let droppedHistory = false
  let knownHead: string[] | undefined
  let pinned = new Set<string>()
  let paused = false
  const loading = ref(false)
  const error = ref<string | null>(null)
  const hasMore = ref(true)
  let version = 0
  let nextCursor: string | undefined
  const prefetch = options?.prefetchNewer ? createNewerPrefetch<T[] | CursorPage<T>>() : undefined
  let streamedNewer = false

  function prefetchToken() {
    return newerPrefetchKey(newer.value)
  }

  if (prefetch) {
    watch(prefetchToken, key => {
      if (streamedNewer) return
      if (!key) {
        prefetch.clear()
        return
      }
      prefetch.schedule(key, async () => {
        const result = await fetchPage(undefined, pageSize)
        if (!Array.isArray(result) && !result?.items) throw new Error('timeline prefetch failed')
        return result
      })
    }, { flush: 'sync' })
  }

  function commitFreshPage(result: T[] | CursorPage<T>) {
    ++version
    const page = Array.isArray(result) ? result : result.items
    knownHead = page.map(row => row.id)
    history.length = 0
    droppedHistory = false
    hasPrevious.value = false
    newer.value = []
    nextCursor = Array.isArray(result) ? page.at(-1)?.id : result.nextCursor
    items.value = mergeById([], page)
    hasMore.value = Array.isArray(result) ? hasMoreAfter(page.length, pageSize) : !!nextCursor
    loading.value = false
    error.value = null
    prefetch?.clear()
  }

  function reset() {
    ++version
    knownHead = undefined; history.length = 0; droppedHistory = false; hasPrevious.value = false; newer.value = []; pinned.clear(); paused = false
    items.value = []; loading.value = false; error.value = null; hasMore.value = true; nextCursor = undefined
  }
  watch(sessionEpoch, reset, { flush: 'sync' })
  if (getCurrentScope()) onScopeDispose(reset)

  function setPinnedIds(ids: string[]) {
    pinned = new Set(ids.slice(0, 8))
    if (!pinned.size && error.value === t('shared.usePaginatedList.s1')) error.value = null
    if (paused && !items.value.slice(0, pageSize).some(item => pinned.has(item.id))) { paused = false; hasMore.value = true; error.value = null }
  }

  async function run(append: boolean) {
    if (!append && pinned.size) { error.value = t('shared.usePaginatedList.s1'); return }
    if (append && items.value.length >= maxPages * pageSize && items.value.slice(0, pageSize).some(item => pinned.has(item.id))) {
      paused = true; hasMore.value = false; error.value = t('shared.usePaginatedList.s1'); return
    }
    const my = ++version
    const maxId = append ? nextCursor : undefined
    loading.value = true
    error.value = null
    try {
      const result = await fetchPage(maxId, pageSize)
      if (my !== version) return
      const page = Array.isArray(result) ? result : result.items
      if (!append) { knownHead = page.map(row => row.id); history.length = 0; droppedHistory = false; hasPrevious.value = false; newer.value = [] }
      const before = append ? items.value.length : 0
      nextCursor = Array.isArray(result) ? page[page.length - 1]?.id : result.nextCursor
      items.value = mergeById(append ? items.value : [], page)
      const grew = items.value.length > before
      while (items.value.length > maxPages * pageSize) {
        const id = items.value[0]!.id
        if (/^\d+$/.test(id)) history.push((BigInt(id) + 1n).toString())
        if (history.length > 128) { history.shift(); droppedHistory = true }
        items.value.splice(0, pageSize)
      }
      hasPrevious.value = history.length > 0 || droppedHistory
      // A page that adds nothing would otherwise keep the sentinel requesting forever.
      hasMore.value = (Array.isArray(result) ? hasMoreAfter(page.length, pageSize) : !!nextCursor)
        && (!append || nextCursor !== maxId)
        && (!append || grew)
    } catch (e) {
      if (my !== version) return
      error.value = e instanceof Error ? e.message : t('shared.usePaginatedList.s2')
    } finally {
      if (my === version) loading.value = false
    }
  }

  async function loadPrevious() {
    if (flight || loading.value || !hasPrevious.value) return
    await startFlight(() => loadPreviousNow())
  }
  async function loadPreviousNow() {
    const cursor = history.at(-1)
    if (!cursor) { await run(false); return }
    if (items.value.slice(-pageSize).some(item => pinned.has(item.id))) { error.value = t('shared.usePaginatedList.s1'); return }
    const my = ++version
    loading.value = true
    try {
      const result = await fetchPage(cursor, pageSize)
      if (my !== version) return
      const page = Array.isArray(result) ? result : result.items
      history.pop()
      items.value = mergeById(page, items.value).slice(0, maxPages * pageSize)
      hasPrevious.value = history.length > 0 || droppedHistory
      nextCursor = items.value.at(-1)?.id
      hasMore.value = true
    } catch (e) { if (my === version) error.value = e instanceof Error ? e.message : t('shared.usePaginatedList.s2') }
    finally { if (my === version) loading.value = false }
  }

  let flight: Promise<unknown> | undefined
  function startFlight<T>(work: () => Promise<T>): Promise<T> {
    const existing = flight
    if (existing) return existing as Promise<T>
    const run = work().finally(() => { if (flight === run) flight = undefined })
    flight = run
    return run
  }
  async function readRows(cursor: string | undefined, limit: number) {
    const response = await fetchPage(cursor, limit)
    return Array.isArray(response) ? response : response.items
  }
  function begin(scope: 'head' | 'window') {
    if (flight) return flight.then(() => true)
    // An in-flight page load is already the fresh read. A false return would queue another walk behind it.
    if (loading.value) return Promise.resolve(true)
    return startFlight(() => scope === 'head' ? readHead() : readWindow())
  }
  async function readWindow() {
    const my = ++version
    let seenHead: T[] | undefined
    const result = await revalidateTimeline(items.value.slice(), async (cursor, limit) => {
      const page = await readRows(cursor, limit)
      if (cursor === undefined) seenHead = page
      return page
    }, () => my === version, knownHead)
    if (result && my === version) {
      if (!knownHead) knownHead = result.statuses.slice(0, pageSize).map(row => row.id)
      items.value = result.statuses
      const newerRows = result.newer.map(({ id }) => ({ id }))
      const key = newerPrefetchKey(newerRows)
      if (seenHead && key && newerRows.every(item => seenHead!.some(row => row.id === item.id))) prefetch?.adopt(key, seenHead)
      streamedNewer = true
      newer.value = newerRows
      streamedNewer = false
      nextCursor = items.value.at(-1)?.id
    }
    return true
  }
  async function readHead() {
    const my = ++version
    const result = await refreshTimelineHead(items.value.slice(), readRows, () => my === version, knownHead)
    if (result && my === version) {
      if (!knownHead) knownHead = result.statuses.slice(0, pageSize).map(row => row.id)
      items.value = result.statuses
      const shown = representedStatusIDs(result.statuses)
      const seen = new Set<string>()
      const merged: { id: string }[] = []
      for (const item of [...result.newer, ...newer.value]) {
        if (shown.has(item.id) || seen.has(item.id) || merged.length >= pageSize) continue
        seen.add(item.id)
        merged.push({ id: item.id })
      }
      const key = newerPrefetchKey(merged)
      const headIds = new Set(result.head.map(row => row.id))
      if (key && merged.every(item => headIds.has(item.id))) prefetch?.adopt(key, result.head)
      streamedNewer = true
      newer.value = merged
      streamedNewer = false
      nextCursor = items.value.at(-1)?.id
    }
    return true
  }
  function revalidate() { return begin('window') }
  function catchUpHead() { return begin('head') }
  function applyStreamed(status: { event: 'update' | 'delete' | 'status.update', id: string }) {
    const id = status.id
    if (!/^\d+$/.test(id)) return
    const visible = (row: { id: string, reblog?: { id?: string } | null }) => row.id === id || row.reblog?.id === id
    if (status.event === 'delete') {
      items.value = items.value.filter(row => !visible(row))
      streamedNewer = true
      newer.value = newer.value.filter(row => row.id !== id)
      streamedNewer = false
      if (nextCursor && !items.value.some(row => row.id === nextCursor)) nextCursor = items.value.at(-1)?.id
      return
    }
    if (status.event !== 'update') return
    if (representedStatusIDs(items.value).has(id) || newer.value.some(row => row.id === id) || knownHead?.includes(id)) return
    if (newer.value.length >= pageSize) return
    streamedNewer = true
    newer.value = [...newer.value, { id }]
    streamedNewer = false
  }
  function revealNewer(): Promise<void> | void {
    if (!prefetch) {
      ++version
      // A fresh first page restores a continuous cursor window, including any gap.
      return run(false)
    }
    if (!newer.value.length) return
    if (pinned.size) return run(false)
    const token = prefetchToken()
    const cached = prefetch.ready(token)
    if (cached) {
      commitFreshPage(cached)
      return
    }
    return revealPrefetchOrFetch()
  }

  async function revealPrefetchOrFetch() {
    if (!prefetch) return run(false)
    const rows = await prefetch.consume()
    const token = prefetchToken()
    if (!newer.value.length) return
    if (rows && token && prefetch.ready(token) === rows && !pinned.size) {
      commitFreshPage(rows)
      return
    }
    await run(false)
  }

  function hydrate(next: T[], more: boolean, window?: PaginationWindow) {
    history.splice(0, history.length, ...(window?.previousCursors ?? []).slice(-128))
    droppedHistory = window?.droppedHistory ?? false
    knownHead = window?.knownHead?.slice() ?? next.slice(0, pageSize).map(row => row.id)
    hasPrevious.value = history.length > 0 || droppedHistory
    newer.value = []; pinned.clear(); paused = false
    ++version
    items.value = next.slice(0, maxPages * pageSize)
    hasMore.value = more
    nextCursor = items.value.at(-1)?.id
    loading.value = false
    error.value = null
  }

  return {
    items,
    newer,
    hasPrevious,
    loadPrevious,
    revalidate,
    catchUpHead,
    applyStreamed,
    revealNewer,
    setPinnedIds,
    loading,
    error,
    hasMore,
    refresh: () => flight ? flight.then(() => undefined) : startFlight(() => run(false)).then(() => undefined),
    reset,
    hydrate,
    windowState: () => ({ previousCursors: history.slice(), droppedHistory, knownHead: knownHead?.slice() }),
    loadMore: async () => {
      if (flight || loading.value || !hasMore.value || items.value.length === 0) return
      await startFlight(() => run(true))
    },
  }
}
