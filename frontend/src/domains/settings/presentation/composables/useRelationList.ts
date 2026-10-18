import { ref, type Ref } from 'vue'
import type { RelationOrder, RelationPage } from '../../infrastructure/moderationApi'
import { useSettingsScope } from './useSettingsScope'
import { apiErrorMessage } from './apiErrorMessage'

/** A Link-header paged list with newest/oldest ordering, scoped to the signed-in session. */
export function useRelationList<T>(fetchPage: (order: RelationOrder, next?: string) => Promise<RelationPage<T>>, key: (item: T) => string, loadErrorText: () => string, opts: { immediate?: boolean } = {}) {
  const items = ref([]) as Ref<T[]>
  const next = ref<string | undefined>()
  const order = ref<RelationOrder>('newest')
  const loading = ref(!!opts.immediate)
  const loaded = ref(false)
  const error = ref('')
  let generation = 0
  let started = false
  const scope = useSettingsScope(() => { generation++; started = false; items.value = []; next.value = undefined; loading.value = false; loaded.value = false; error.value = '' })

  async function fetch(reset: boolean) {
    if (loading.value && !reset && loaded.value) return
    const current = scope()
    const mine = ++generation
    loading.value = true
    error.value = ''
    try {
      const page = await fetchPage(order.value, reset ? undefined : next.value)
      if (!current() || mine !== generation) return
      if (reset) items.value = page.items
      else {
        const seen = new Set(items.value.map(key))
        items.value = [...items.value, ...page.items.filter((item) => !seen.has(key(item)))]
      }
      next.value = page.items.length ? page.next : undefined
      loaded.value = true
    } catch (e) {
      if (current() && mine === generation) error.value = apiErrorMessage(e, loadErrorText())
    } finally {
      if (current() && mine === generation) loading.value = false
    }
  }

  return {
    items, next, order, loading, loaded, error,
    reload: () => fetch(true),
    loadMore: () => fetch(false),
    ensureLoaded: () => (loaded.value || (loading.value && started) ? undefined : (started = true, fetch(true))),
    setOrder: (value: RelationOrder) => { if (order.value !== value) { order.value = value; void fetch(true) } },
    removeItem: (id: string) => { items.value = items.value.filter((item) => key(item) !== id) },
    prepend: (item: T) => { if (!items.value.some((i) => key(i) === key(item))) items.value = order.value === 'newest' ? [item, ...items.value] : items.value },
    scope,
  }
}
