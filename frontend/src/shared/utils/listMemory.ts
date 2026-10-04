/** In-memory snapshots so a timeline can reopen at the same posts and scroll offset. */
export interface ListSnapshot<T> {
  items: T[]
  hasMore: boolean
  scroll: number
}

const memory = new Map<string, ListSnapshot<unknown>>()

export function saveListSnapshot<T>(key: string, items: T[], hasMore: boolean) {
  memory.set(key, {
    items: items.slice(),
    hasMore,
    scroll: typeof window === 'undefined' ? 0 : window.scrollY,
  })
}

export function readListSnapshot<T>(key: string): ListSnapshot<T> | undefined {
  const snap = memory.get(key) as ListSnapshot<T> | undefined
  if (!snap) return undefined
  return { items: snap.items.slice(), hasMore: snap.hasMore, scroll: snap.scroll }
}

export function restoreListScroll(scroll: number) {
  if (scroll <= 0) return
  requestAnimationFrame(() => window.scrollTo(0, scroll))
}
