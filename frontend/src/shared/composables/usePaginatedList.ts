import { refreshTimelineHead, representedStatusIDs, revalidateTimeline } from '@/shared/utils/revalidateTimeline'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { createTimelineReadGuard } from '@/shared/utils/timelineReadGuard'
import { refreshStreamedStatus } from '@/domains/post/presentation/composables/useStatusActions'
import { createNewerPrefetch, newerPrefetchKey } from '@/shared/utils/newerPrefetch'
import { ref, watch, getCurrentScope, onScopeDispose, type Ref } from 'vue'
import { mergeById, hasMoreAfter, type CursorPage } from '@/shared/utils/pagination'
import { dedupeMirroredStatuses, sharesStatusIdentity, type StreamedStatusRef } from '@/shared/utils/statusIdentity'

import { t } from '@/shared/i18n'
export interface PaginatedTimelineGap {
  afterId: string
  beforeId: string
  isLoading: boolean
}

export interface PaginationWindow {
  previousCursors: string[]
  droppedHistory: boolean
  knownHead?: string[]
  headCursor?: string
  gap?: PaginatedTimelineGap | null
}

export interface PaginatedList<T> {
  items: Ref<T[]>
  newer: Ref<{ id: string }[]>
  hasPrevious: Ref<boolean>
  gap: Ref<PaginatedTimelineGap | null>
  loadPrevious: () => Promise<void>
  loadGap: () => Promise<void>
  revalidate: () => Promise<boolean>
  /** One head page: new ids join the pill, the loaded tail is not walked. */
  catchUpHead: () => Promise<boolean>
  /** Apply a streamed status id without fetching the timeline. */
  applyStreamed: (status: StreamedStatusRef & { event: 'update' | 'delete' | 'status.update' }) => void
  revealNewer: (options?: { silent?: boolean; preserveWindow?: boolean }) => Promise<boolean> | boolean
  setPinnedIds: (ids: string[]) => void
  loading: Ref<boolean>
  error: Ref<string | null>
  hasMore: Ref<boolean>
  /** Reload the first page. Existing items stay until the new page arrives. */
  refresh: (options?: { preserveWindow?: boolean } | Event) => Promise<void>
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
  const gap = ref<PaginatedTimelineGap | null>(null)
  const history: string[] = []
  let droppedHistory = false
  let knownHead: string[] | undefined
  let pinned = new Set<string>()
  let paused = false
  const loading = ref(false)
  const error = ref<string | null>(null)
  const hasMore = ref(true)
  let version = 0
  let scopeVersion = 0
  let flight: Promise<unknown> | undefined
  let nextCursor: string | undefined
  // Exclusive boundary immediately before the retained window. An empty
  // history entry represents the first page, fetched without max_id.
  let headCursor: string | undefined
  const readGuard = createTimelineReadGuard()
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
        return Array.isArray(result) ? dedupeMirroredStatuses(result) : { ...result, items: dedupeMirroredStatuses(result.items) }
      })
    }, { flush: 'sync' })
  }

  function commitFreshPage(result: T[] | CursorPage<T>) {
    ++version
    const page = Array.isArray(result) ? result : result.items
    knownHead = page.map(row => row.id)
    headCursor = undefined
    history.length = 0
    droppedHistory = false
    hasPrevious.value = false
    gap.value = null
    newer.value = []
    nextCursor = Array.isArray(result) ? page.at(-1)?.id : result.nextCursor
    items.value = dedupeMirroredStatuses(mergeById([], page))
    hasMore.value = Array.isArray(result) ? hasMoreAfter(page.length, pageSize) : !!nextCursor
    loading.value = false
    error.value = null
    prefetch?.clear()
  }

  function commitPreservedPage(result: T[] | CursorPage<T>, hintsAtStart?: Set<string>) {
    if (!items.value.length) {
      commitFreshPage(result)
      return
    }
    ++version
    const page = Array.isArray(result) ? result : result.items
    if (hasPrevious.value) {
      knownHead = page.map(row => row.id)
      const shown = representedStatusIDs(page)
      streamedNewer = true
      newer.value = hintsAtStart
        ? newer.value.filter(row => !hintsAtStart.has(row.id) && !shown.has(row.id))
        : []
      streamedNewer = false
      prefetch?.clear()
      loading.value = false
      error.value = null
      return
    }
    const previousItems = items.value
    const oldFirstId = previousItems[0]!.id
    const prevGap = gap.value
    const existingIds = representedStatusIDs(previousItems)
    const overlaps = page.some(row => existingIds.has(row.id) || previousItems.some(prev => sharesStatusIdentity(prev, row)))
    const connected = !page.length || overlaps || (Array.isArray(result) ? !hasMoreAfter(page.length, pageSize) : !result.nextCursor)
    const merged = dedupeMirroredStatuses(mergeById(page, previousItems))
    const capLimit = Number.isFinite(maxPages) ? maxPages * pageSize : Infinity
    let maxKeep = capLimit
    if (Number.isFinite(capLimit) && merged.length > capLimit) {
      for (let i = merged.length - 1; i >= capLimit; i--) {
        if (pinned.has(merged[i]!.id)) {
          maxKeep = i + 1
          break
        }
      }
    }
    const capped = merged.length > maxKeep ? merged.slice(0, maxKeep) : merged
    if (merged.length > capped.length) {
      hasMore.value = true
      if (Array.isArray(result)) nextCursor = capped.at(-1)?.id
    } else if (Array.isArray(result)) {
      nextCursor = capped.at(-1)?.id
    }

    knownHead = page.map(row => row.id)
    items.value = capped

    const oldFirstIdx = capped.findIndex(row => row.id === oldFirstId)
    if (!connected && oldFirstIdx > 0) {
      gap.value = { afterId: capped[oldFirstIdx - 1]!.id, beforeId: oldFirstId, isLoading: false }
    } else if (prevGap) {
      const afterIdx = capped.findIndex(row => row.id === prevGap.afterId)
      const beforeIdx = capped.findIndex(row => row.id === prevGap.beforeId)
      gap.value = afterIdx >= 0 && beforeIdx > afterIdx ? prevGap : null
    } else {
      gap.value = null
    }

    const shown = representedStatusIDs(items.value)
    streamedNewer = true
    newer.value = hintsAtStart
      ? newer.value.filter(row => !hintsAtStart.has(row.id) && !shown.has(row.id))
      : newer.value.filter(row => !shown.has(row.id))
    streamedNewer = false
    prefetch?.clear()
    loading.value = false
    error.value = null
  }

  function reset() {
    ++scopeVersion
    ++version
    // Old scope requests still finish under their version guard. They must
    // not occupy the new filter/account's flight slot or cached head.
    flight = undefined
    prefetch?.clear()
    headCursor = undefined
    knownHead = undefined; history.length = 0; droppedHistory = false; hasPrevious.value = false; gap.value = null; newer.value = []; pinned.clear(); paused = false
    items.value = []; loading.value = false; error.value = null; hasMore.value = true; nextCursor = undefined
  }
  watch(sessionEpoch, reset, { flush: 'sync' })
  if (getCurrentScope()) onScopeDispose(reset)

  function setPinnedIds(ids: string[]) {
    pinned = new Set(ids.slice(0, 8))
    if (!pinned.size && error.value === t('shared.usePaginatedList.s1')) error.value = null
    if (paused && !items.value.slice(0, pageSize).some(item => pinned.has(item.id))) { paused = false; hasMore.value = true; error.value = null }
  }

  async function run(append: boolean, preserveWindow = false): Promise<boolean> {
    if (!append && !preserveWindow && pinned.size) { error.value = t('shared.usePaginatedList.s1'); return false }
    if (append && items.value.length >= maxPages * pageSize && items.value.slice(0, pageSize).some(item => pinned.has(item.id))) {
      paused = true; hasMore.value = false; error.value = t('shared.usePaginatedList.s1'); return false
    }
    const my = ++version
    const maxId = append ? nextCursor : undefined
    const showLoading = append || !preserveWindow || items.value.length === 0
    if (showLoading) loading.value = true
    error.value = null
    const read = readGuard.begin()
    const hintsAtStart = new Set(newer.value.map(row => row.id))
    try {
      const result = await fetchPage(maxId, pageSize)
      if (my !== version) return false
      if (!append && preserveWindow && items.value.length > 0) {
        const filteredResult = Array.isArray(result)
          ? read.filter(result)
          : { ...result, items: read.filter(result.items) }
        commitPreservedPage(filteredResult, hintsAtStart)
        return true
      }
      const page = Array.isArray(result) ? result : result.items
      if (!append) { knownHead = page.map(row => row.id); headCursor = undefined; history.length = 0; droppedHistory = false; hasPrevious.value = false; gap.value = null
        const shown = representedStatusIDs(page)
        streamedNewer = true
        newer.value = newer.value.filter(row => !hintsAtStart.has(row.id) && !shown.has(row.id))
        streamedNewer = false
        prefetch?.clear()
      }
      const before = append ? items.value.length : 0
      nextCursor = Array.isArray(result) ? page[page.length - 1]?.id : result.nextCursor
      items.value = dedupeMirroredStatuses(mergeById(append ? items.value : [], read.filter(page)))
      const grew = items.value.length > before
      while (items.value.length > maxPages * pageSize) {
        history.push(headCursor ?? '')
        headCursor = items.value[pageSize - 1]!.id
        if (history.length > 128) { history.shift(); droppedHistory = true }
        items.value.splice(0, pageSize)
      }
      hasPrevious.value = history.length > 0 || droppedHistory
      // A page that adds nothing would otherwise keep the sentinel requesting forever.
      hasMore.value = (Array.isArray(result) ? hasMoreAfter(page.length, pageSize) : !!nextCursor)
        && (!append || nextCursor !== maxId)
        && (!append || grew)
      return true
    } catch (e) {
      if (my !== version) return false
      error.value = e instanceof Error ? e.message : t('shared.usePaginatedList.s2')
      return false
    } finally {
      read.close()
      if (my === version) loading.value = false
    }
  }

  async function loadPrevious() {
    if (flight || loading.value || !hasPrevious.value) return
    await startFlight(() => loadPreviousNow())
  }
  async function loadPreviousNow() {
    const cursor = history.at(-1)
    if (cursor === undefined) { await run(false); return }
    if (items.value.slice(-pageSize).some(item => pinned.has(item.id))) { error.value = t('shared.usePaginatedList.s1'); return }
    const my = ++version
    loading.value = true
    const read = readGuard.begin()
    try {
      const result = await fetchPage(cursor || undefined, pageSize)
      if (my !== version) return
      const page = Array.isArray(result) ? result : result.items
      history.pop()
      headCursor = cursor || undefined
      items.value = dedupeMirroredStatuses(mergeById(read.filter(page), items.value)).slice(0, maxPages * pageSize)
      hasPrevious.value = history.length > 0 || droppedHistory
      nextCursor = items.value.at(-1)?.id
      hasMore.value = true
    } catch (e) { if (my === version) error.value = e instanceof Error ? e.message : t('shared.usePaginatedList.s2') }
    finally { read.close(); if (my === version) loading.value = false }
  }

  async function loadGap() {
    if (flight || loading.value || !gap.value || gap.value.isLoading) return
    await startFlight(() => loadGapNow())
  }
  async function loadGapNow() {
    const currentGap = gap.value
    if (!currentGap || currentGap.isLoading) return
    const initialAfter = items.value.findIndex(row => row.id === currentGap.afterId)
    const initialBefore = items.value.findIndex(row => row.id === currentGap.beforeId)
    if (initialAfter < 0 || initialBefore <= initialAfter) {
      gap.value = null
      return
    }
    const my = ++version
    gap.value = { ...currentGap, isLoading: true }
    error.value = null
    const read = readGuard.begin()
    try {
      const result = await fetchPage(currentGap.afterId, pageSize)
      if (my !== version) return
      const rawPage = Array.isArray(result) ? result : result.items
      const page = dedupeMirroredStatuses(read.filter(rawPage))
      const beforeIdx = items.value.findIndex(row => row.id === currentGap.beforeId)
      if (beforeIdx <= 0) {
        gap.value = null
        return
      }
      const upper = items.value.slice(0, beforeIdx)
      const lower = items.value.slice(beforeIdx)
      const lowerIds = representedStatusIDs(lower)
      const overlapsLower = page.some(row => lowerIds.has(row.id) || lower.some(prev => sharesStatusIdentity(prev, row)))
      const lastFetchedId = page[page.length - 1]?.id
      const bridged = overlapsLower
        || (Array.isArray(result) ? !hasMoreAfter(page.length, pageSize) : !result.nextCursor)
        || !lastFetchedId
        || lastFetchedId === currentGap.afterId
      const combined = dedupeMirroredStatuses(mergeById(dedupeMirroredStatuses(mergeById(upper, page)), lower))
      const newBeforeIdx = combined.findIndex(row => row.id === currentGap.beforeId)
      const capLimit = Number.isFinite(maxPages) ? maxPages * pageSize : Infinity
      let maxKeep = Number.isFinite(capLimit)
        ? Math.max(capLimit, newBeforeIdx >= 0 ? newBeforeIdx + pageSize : 0)
        : Infinity
      if (Number.isFinite(maxKeep) && combined.length > maxKeep) {
        for (let i = combined.length - 1; i >= maxKeep; i--) {
          if (pinned.has(combined[i]!.id)) {
            maxKeep = i + 1
            break
          }
        }
      }
      const capped = combined.length > maxKeep ? combined.slice(0, maxKeep) : combined
      if (combined.length > capped.length) {
        hasMore.value = true
        if (Array.isArray(result)) nextCursor = capped.at(-1)?.id
      }
      items.value = capped
      if (bridged || newBeforeIdx <= 0) {
        gap.value = null
      } else {
        const nextAfterId = capped[newBeforeIdx - 1]!.id
        gap.value = nextAfterId === currentGap.afterId
          ? null
          : { afterId: nextAfterId, beforeId: currentGap.beforeId, isLoading: false }
      }
    } catch (e) {
      if (my === version) {
        if (gap.value) gap.value = { ...gap.value, isLoading: false }
        error.value = e instanceof Error ? e.message : t('shared.usePaginatedList.s2')
      }
    } finally {
      read.close()
    }
  }

  function startFlight<T>(work: () => Promise<T>): Promise<T> {
    const existing = flight
    if (existing) return existing as Promise<T>
    const run = work().finally(() => { if (flight === run) flight = undefined })
    flight = run
    return run
  }
  async function readRows(cursor: string | undefined, limit: number) {
    const response = await fetchPage(cursor, limit)
    return dedupeMirroredStatuses(Array.isArray(response) ? response : response.items)
  }
  function begin(scope: 'head' | 'window') {
    if (flight) return flight.then(() => true)
    // An in-flight page load is already the fresh read. A false return would queue another walk behind it.
    if (loading.value) return Promise.resolve(true)
    return startFlight(() => scope === 'head' ? readHead() : readWindow())
  }
  async function readWindow() {
    const my = ++version
    const read = readGuard.begin()
    try {
      let seenHead: T[] | undefined
      const result = await revalidateTimeline(items.value.slice(), async (cursor, limit) => {
        const page = read.filter(await readRows(cursor, limit))
        if (cursor === undefined) seenHead = page
        return page
      }, () => my === version, knownHead)
      if (result?.inaccessible && my === version) { commitFreshPage([]); return true }
      if (result && my === version) {
        if (!knownHead) knownHead = result.statuses.slice(0, pageSize).map(row => row.id)
        items.value = dedupeMirroredStatuses(read.filter(result.statuses))
        const newerRows = mergeNewer(result.newer.filter(row => !read.wasRemoved(row.id)))
        const key = newerPrefetchKey(newerRows)
        if (seenHead && key && newerRows.every(item => seenHead!.some(row => row.id === item.id))) prefetch?.adopt(key, read.filter(seenHead))
        streamedNewer = true
        newer.value = newerRows
        streamedNewer = false
        nextCursor = items.value.at(-1)?.id
      }
      return true
    } finally { read.close() }
  }

  function mergeNewer(incoming: { id: string }[]) {
    const shown = representedStatusIDs(items.value)
    const seen = new Set<string>()
    const merged: { id: string }[] = []
    for (const item of [...incoming, ...newer.value]) {
      if (shown.has(item.id) || seen.has(item.id) || merged.length >= pageSize) continue
      seen.add(item.id)
      merged.push({ id: item.id })
    }
    return merged
  }

  async function readHead() {
    const my = ++version
    const read = readGuard.begin()
    try {
      const result = await refreshTimelineHead(items.value.slice(), async (cursor, limit) => read.filter(await readRows(cursor, limit)), () => my === version, knownHead)
      if (result?.inaccessible && my === version) { commitFreshPage([]); return true }
      if (result && my === version) {
        if (!knownHead) knownHead = result.statuses.slice(0, pageSize).map(row => row.id)
        items.value = dedupeMirroredStatuses(read.filter(result.statuses))
        const merged = mergeNewer(result.newer.filter(row => !read.wasRemoved(row.id)))
        const key = newerPrefetchKey(merged)
        const headIds = new Set(result.head.map(row => row.id))
        if (key && merged.every(item => headIds.has(item.id))) prefetch?.adopt(key, read.filter(result.head))
        streamedNewer = true
        newer.value = merged
        streamedNewer = false
        nextCursor = items.value.at(-1)?.id
      }
      return true
    } finally { read.close() }
  }
  function revalidate() { return begin('window') }
  function catchUpHead() { return begin('head') }
  function applyStreamed(status: StreamedStatusRef & { event: 'update' | 'delete' | 'status.update' }) {
    const id = status.id
    if (!/^\d+$/.test(id)) return
    const visible = (row: { id: string, reblog?: { id?: string } | null }) => row.id === id || row.reblog?.id === id
    if (status.event === 'delete') {
      readGuard.remove(id)
      prefetch?.clear()
      if (gap.value) {
        const afterIdx = items.value.findIndex(row => row.id === gap.value!.afterId)
        const beforeIdx = items.value.findIndex(row => row.id === gap.value!.beforeId)
        const keptUpper = afterIdx >= 0 ? items.value.slice(0, afterIdx + 1).filter(row => !visible(row)) : []
        const keptLower = beforeIdx >= 0 ? items.value.slice(beforeIdx).filter(row => !visible(row)) : []
        gap.value = keptUpper.length && keptLower.length
          ? { ...gap.value, afterId: keptUpper.at(-1)!.id, beforeId: keptLower[0]!.id }
          : null
      }
      items.value = items.value.filter(row => !visible(row))
      streamedNewer = true
      newer.value = newer.value.filter(row => row.id !== id)
      streamedNewer = false
      if (nextCursor && !items.value.some(row => row.id === nextCursor)) nextCursor = items.value.at(-1)?.id
      return
    }
    if (status.event === 'status.update') {
      prefetch?.clear()
      if (items.value.some(visible)) {
        const scope = version
        void refreshStreamedStatus(id, (target, updates) => {
          if (scope !== version) return
          for (const row of items.value) {
            if (row.id === target) Object.assign(row, updates)
            const reblog = (row as { reblog?: { id: string } | null }).reblog
            if (reblog?.id === target) Object.assign(reblog, updates)
          }
        })
      }
      return
    }
    if (status.event !== 'update') return
    if (representedStatusIDs(items.value).has(id) || items.value.some(row => sharesStatusIdentity(row, status)) || newer.value.some(row => row.id === id) || knownHead?.includes(id)) return
    streamedNewer = true
    newer.value = [...newer.value.slice(-(pageSize - 1)), { id }].slice(-pageSize)
    streamedNewer = false
  }
  function revealNewer(options?: { silent?: boolean; preserveWindow?: boolean }): Promise<boolean> | boolean {
    if (!newer.value.length) return false
    const preserve = !!options?.preserveWindow
    if (flight) {
      const scope = scopeVersion
      return flight.catch(() => {}).then(() => scope === scopeVersion ? revealNewer(options) : false)
    }
    if (!preserve && pinned.size) {
      if (!options?.silent) error.value = t('shared.usePaginatedList.s1')
      return false
    }
    const cached = prefetch?.ready(prefetchToken())
    if (cached) {
      if (preserve) commitPreservedPage(cached)
      else commitFreshPage(cached)
      return true
    }
    return revealPrefetchOrFetch(options)
  }

  async function revealPrefetchOrFetch(options?: { silent?: boolean; preserveWindow?: boolean }): Promise<boolean> {
    const preserve = !!options?.preserveWindow
    const scope = scopeVersion
    const rows = await prefetch?.consume()
    if (scope !== scopeVersion || !newer.value.length) return false
    const token = prefetchToken()
    if (rows && token && prefetch?.ready(token) === rows && (preserve || !pinned.size)) {
      if (preserve) commitPreservedPage(rows)
      else commitFreshPage(rows)
      return true
    }
    if (flight) return revealNewer(options)
    return startFlight(() => run(false, preserve))
  }

  function hydrate(next: T[], more: boolean, window?: PaginationWindow) {
    ++scopeVersion
    flight = undefined
    prefetch?.clear()
    history.splice(0, history.length, ...(window?.previousCursors ?? []).slice(-128))
    droppedHistory = window?.droppedHistory ?? false
    headCursor = window?.headCursor
    knownHead = window?.knownHead?.slice() ?? next.slice(0, pageSize).map(row => row.id)
    hasPrevious.value = history.length > 0 || droppedHistory
    gap.value = window?.gap ? { ...window.gap, isLoading: false } : null
    newer.value = []; pinned.clear(); paused = false
    ++version
    items.value = dedupeMirroredStatuses(next.slice(0, maxPages * pageSize))
    hasMore.value = more
    nextCursor = items.value.at(-1)?.id
    loading.value = false
    error.value = null
  }

  return {
    items,
    newer,
    hasPrevious,
    gap,
    loadPrevious,
    loadGap,
    revalidate,
    catchUpHead,
    applyStreamed,
    revealNewer,
    setPinnedIds,
    loading,
    error,
    hasMore,
    refresh: (options) => {
      const preserve = !!options && 'preserveWindow' in options && !!options.preserveWindow && !hasPrevious.value
      return flight ? flight.then(() => undefined) : startFlight(() => run(false, preserve)).then(() => undefined)
    },
    reset,
    hydrate,
    windowState: () => ({
      previousCursors: history.slice(),
      droppedHistory,
      knownHead: knownHead?.slice(),
      headCursor,
      gap: gap.value ? { ...gap.value, isLoading: false } : null,
    }),
    loadMore: async () => {
      if (flight || loading.value || !hasMore.value || items.value.length === 0) return
      await startFlight(() => run(true))
    },
  }
}
