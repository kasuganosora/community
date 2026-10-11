import type { PaginationWindow } from '@/shared/composables/usePaginatedList'
import { watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'

/** In-memory snapshots so a timeline can reopen at the same posts and scroll offset. */
export interface ListSnapshot<T> {
  items: T[]
  hasMore: boolean
  scroll: number
  window?: PaginationWindow
}

const memory = new Map<string, ListSnapshot<unknown>>()

watch(sessionEpoch, () => memory.clear(), { flush: 'sync' })

export function saveListSnapshot<T>(key: string, items: T[], hasMore: boolean, windowState?: PaginationWindow) {
  memory.delete(key)
  const bounded = items.slice(0, 160)
  memory.set(key, {
    items: bounded,
    hasMore,
    window: windowState ? { previousCursors: windowState.previousCursors.slice(-128), droppedHistory: windowState.droppedHistory, knownHead: windowState.knownHead?.slice(0, 20) } : undefined,
    scroll: typeof window === 'undefined' || items.length > bounded.length ? 0 : window.scrollY,
  })
  // Eight route snapshots, and at most eight pages of cached bodies in total.
  while (memory.size > 8 || [...memory.values()].reduce((sum, value) => sum + value.items.length, 0) > 160) memory.delete(memory.keys().next().value!)
}

export function readListSnapshot<T>(key: string): ListSnapshot<T> | undefined {
  const snap = memory.get(key) as ListSnapshot<T> | undefined
  if (!snap) return undefined
  memory.delete(key); memory.set(key, snap)
  return { items: snap.items.slice(), hasMore: snap.hasMore, scroll: snap.scroll, window: snap.window ? { ...snap.window, previousCursors: snap.window.previousCursors.slice(), knownHead: snap.window.knownHead?.slice() } : undefined }
}

export function restoreListScroll(scroll: number) {
  if (scroll <= 0) return
  requestAnimationFrame(() => window.scrollTo(0, scroll))
}
