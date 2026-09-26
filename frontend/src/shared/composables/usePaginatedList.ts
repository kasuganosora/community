import { ref, type Ref } from 'vue'
import { mergeById, hasMoreAfter } from '@/shared/utils/pagination'

export interface PaginatedList<T> {
  items: Ref<T[]>
  loading: Ref<boolean>
  error: Ref<string | null>
  hasMore: Ref<boolean>
  /** Reload the first page (replaces items). */
  refresh: () => Promise<void>
  /** Fetch the next page using the id of the last item as max_id. */
  loadMore: () => Promise<void>
}

/**
 * Cursor pagination for Mastodon-style lists (max_id = id of the last item).
 * Every request bumps a version counter so a slow response from an older
 * request (e.g. before a tab switch) can never overwrite newer data.
 */
export function usePaginatedList<T extends { id: string }>(
  fetchPage: (maxId: string | undefined, limit: number) => Promise<T[]>,
  pageSize = 20,
): PaginatedList<T> {
  const items = ref([]) as Ref<T[]>
  const loading = ref(false)
  const error = ref<string | null>(null)
  const hasMore = ref(true)
  let version = 0

  async function run(append: boolean) {
    const my = ++version
    const maxId = append ? items.value[items.value.length - 1]?.id : undefined
    loading.value = true
    error.value = null
    if (!append) {
      items.value = []
      hasMore.value = true
    }
    try {
      const page = await fetchPage(maxId, pageSize)
      if (my !== version) return
      items.value = append ? mergeById(items.value, page) : page
      hasMore.value = hasMoreAfter(page.length, pageSize)
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
    loadMore: async () => {
      if (loading.value || !hasMore.value || items.value.length === 0) return
      await run(true)
    },
  }
}
