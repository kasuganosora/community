import { revalidateTimeline } from '@/shared/utils/revalidateTimeline'
import { sessionEpoch } from '@/shared/api/sessionScope'
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
  revealNewer: () => Promise<void>
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
    if (loading.value || !hasPrevious.value) return
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

  async function revalidate() {
    if (loading.value) return false
    const my = ++version
    const result = await revalidateTimeline(items.value.slice(), async (cursor, limit) => {
      const response = await fetchPage(cursor, limit)
      return Array.isArray(response) ? response : response.items
    }, () => my === version, knownHead)
    if (result && my === version) {
      if (!knownHead) knownHead = result.statuses.slice(0, pageSize).map(row => row.id)
      items.value = result.statuses
      newer.value = result.newer.map(({ id }) => ({ id }))
      nextCursor = items.value.at(-1)?.id
    }
    return true
  }
  async function revealNewer() {
    ++version
    // A fresh first page restores a continuous cursor window, including any gap.
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
    revealNewer,
    setPinnedIds,
    loading,
    error,
    hasMore,
    refresh: () => run(false),
    reset,
    hydrate,
    windowState: () => ({ previousCursors: history.slice(), droppedHistory, knownHead: knownHead?.slice() }),
    loadMore: async () => {
      if (loading.value || !hasMore.value || items.value.length === 0) return
      await run(true)
    },
  }
}
