import { sessionEpoch } from '@/shared/api/sessionScope'
import { ref, watch, type Ref } from 'vue'
import { mergeById, hasMoreAfter, type CursorPage } from '@/shared/utils/pagination'

export interface PaginatedList<T> {
  items: Ref<T[]>
  loading: Ref<boolean>
  error: Ref<string | null>
  hasMore: Ref<boolean>
  /** Reload the first page. Existing items stay until the new page arrives. */
  refresh: () => Promise<void>
  /** Invalidate pending responses and clear data when account or list scope changes. */
  reset: () => void
  /** Fetch the next page using its server cursor (or last item id for array APIs). */
  loadMore: () => Promise<void>
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
): PaginatedList<T> {
  const items = ref([]) as Ref<T[]>
  const loading = ref(false)
  const error = ref<string | null>(null)
  const hasMore = ref(true)
  let version = 0
  let nextCursor: string | undefined

  function reset() {
    ++version
    items.value = []; loading.value = false; error.value = null; hasMore.value = true; nextCursor = undefined
  }
  watch(sessionEpoch, reset, { flush: 'sync' })

  async function run(append: boolean) {
    const my = ++version
    const maxId = append ? nextCursor : undefined
    loading.value = true
    error.value = null
    try {
      const result = await fetchPage(maxId, pageSize)
      if (my !== version) return
      const page = Array.isArray(result) ? result : result.items
      nextCursor = Array.isArray(result) ? page[page.length - 1]?.id : result.nextCursor
      items.value = mergeById(append ? items.value : [], page)
      hasMore.value = (Array.isArray(result) ? hasMoreAfter(page.length, pageSize) : !!nextCursor) && (!append || nextCursor !== maxId)
    } catch (e) {
      if (my !== version) return
      error.value = e instanceof Error ? e.message : '加载失败'
    } finally {
      if (my === version) loading.value = false
    }
  }

  return {
    items,
    loading,
    error,
    hasMore,
    refresh: () => run(false),
    reset,
    loadMore: async () => {
      if (loading.value || !hasMore.value || items.value.length === 0) return
      await run(true)
    },
  }
}
