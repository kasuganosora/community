import { computed, onScopeDispose, ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { discoveryApi, isDiscoveryReset, type DiscoveryItem, type DiscoveryPage, type FeedbackAction } from '../../infrastructure/discoveryApi'
import { discoveryInvalidation, discoveryPreference, acceptDiscoveryPreference, invalidateDiscovery } from './discoveryState'

// One remote actor may be stored as several account rows; the server hides
// every row sharing the acct, so the current list does the same.
function sameAuthor(a: MastodonStatus, b: MastodonStatus) {
  return a.account.id === b.account.id || (!!a.account.acct && a.account.acct.toLowerCase() === b.account.acct?.toLowerCase())
}

function containsHiddenTarget(root: MastodonStatus, target: MastodonStatus, action: 'hide_status' | 'hide_author'): boolean {
  const pending = [{ status: root, depth: 0 }], visited = new Set<string>()
  while (pending.length) {
    const { status, depth } = pending.pop()!
    // Discard uncheckable embedded trees rather than leave a hidden quote on
    // screen until the next server request. The server performs full checks.
    if (depth > 4 || visited.size >= 16 || visited.has(status.id)) return true
    visited.add(status.id)
    if (action === 'hide_author' ? sameAuthor(status, target) : status.id === target.id) return true
    for (const child of [status.quote, status.reblog]) if (child) pending.push({ status: child, depth: depth + 1 })
  }
  return false
}

function retryable(e: unknown) {
  const response = (e as { response?: { status?: number; data?: { retryable?: boolean } } })?.response
  return response?.status === 503 && response.data?.retryable === true
}

export function useDiscoveryFeed() {
  const items = ref<DiscoveryItem[]>([])
  const loading = ref(false), error = ref<string | null>(null), resetRequired = ref(false)
  const cursor = ref<string | null>(null), mode = ref<DiscoveryPage['mode']>('rules')
  const disabled = ref(false), autoPaused = ref(false), generation = ref('')
  let requestVersion = 0, controller: AbortController | undefined, ownFeedback = false
  // When the current window was first loaded; the server snapshot lives 10 minutes.
  let loadedAt = 0
  const statuses = computed(() => items.value.map(item => item.status))
  const reasons = computed(() => Object.fromEntries(items.value.filter(item => item.reason).map(item => [item.status.id, item.reason!])))
  const hasMore = computed(() => cursor.value !== null && !resetRequired.value)

  function cancel() { requestVersion++; controller?.abort(); loading.value = false }
  function clear() {
    cancel(); items.value = []; cursor.value = null; error.value = null
    resetRequired.value = false; autoPaused.value = false; disabled.value = false; generation.value = ''
    loadedAt = 0
  }
  /** True when the loaded window can be shown again without refetching. */
  function isFresh(maxAgeMs = 5 * 60_000) {
    return loadedAt > 0 && Date.now() - loadedAt < maxAgeMs && items.value.length > 0 && !resetRequired.value && !error.value && !loading.value
  }
  watch(sessionEpoch, clear, { flush: 'sync' })
  watch(discoveryInvalidation, () => {
    if (ownFeedback) return
    clear()
    resetRequired.value = true
  }, { flush: 'sync' })
  onScopeDispose(cancel)

  async function load(refresh = false, manual = false) {
    if (loading.value || (!refresh && (!hasMore.value || (autoPaused.value && !manual)))) return
    if (refresh) clear()
    loading.value = true; error.value = null; autoPaused.value = false
    const mine = ++requestVersion, epoch = sessionEpoch.value
    let preferenceRevision = discoveryPreference.value?.revision
    const abort = new AbortController(); controller = abort
    let next = refresh ? undefined : cursor.value ?? undefined
    try {
      // A whole logical block may be filtered. Stop automatic traversal after
      // three empty blocks; the explicit button may continue without a loop.
      for (let emptyPages = 0; emptyPages < 3; emptyPages++) {
        let page: DiscoveryPage
        try { page = await discoveryApi.statuses(next, abort.signal) }
        catch (e) {
          // The server sheds load (1s budget / bounded read slots) with a
          // retryable 503; retry once quietly before surfacing an error.
          if (!retryable(e) || abort.signal.aborted) throw e
          await new Promise(resolve => setTimeout(resolve, 1500))
          if (mine !== requestVersion || epoch !== sessionEpoch.value) return
          page = await discoveryApi.statuses(next, abort.signal)
        }
        if (mine !== requestVersion || epoch !== sessionEpoch.value) return
        if ((preferenceRevision !== undefined && discoveryPreference.value?.revision !== preferenceRevision)
          || (discoveryPreference.value && page.preference_revision < discoveryPreference.value.revision)) return
        if (next && generation.value && page.generation !== generation.value) throw { response: { data: { code: 'reset_required' } } }
        acceptDiscoveryPreference({ preference: page.preference, revision: page.preference_revision })
        preferenceRevision = page.preference_revision
        mode.value = page.mode; disabled.value = page.disabled || page.mode === 'disabled'
        generation.value = page.generation
        if (disabled.value) { items.value = []; cursor.value = null; return }
        const seen = new Set(items.value.map(item => item.status.id))
        const additions = page.items.filter(item => {
          if (seen.has(item.status.id)) return false
          seen.add(item.status.id)
          return true
        })
        items.value = [...items.value, ...additions].slice(0, 200)
        if (refresh && !loadedAt) loadedAt = Date.now()
        cursor.value = items.value.length >= 200 ? null : page.next_cursor
        if (next && next === page.next_cursor) throw new Error('推荐分页没有前进，请刷新后重试')
        if (additions.length || !cursor.value) break
        next = cursor.value
        if (emptyPages === 2) autoPaused.value = true
      }
    } catch (e) {
      if (mine !== requestVersion || epoch !== sessionEpoch.value) return
      if (isDiscoveryReset(e)) {
        resetRequired.value = true; cursor.value = null
      } else error.value = e instanceof Error ? e.message : '暂时无法加载推荐，请重试'
    } finally { if (mine === requestVersion) loading.value = false }
  }

  async function feedback(status: MastodonStatus, action: Extract<FeedbackAction, 'hide_status' | 'hide_author'>) {
    const epoch = sessionEpoch.value
    const result = await discoveryApi.addFeedback({ action, target_kind: action === 'hide_author' ? 'account' : 'status', target_key: action === 'hide_author' ? status.account.id : status.id })
    if (epoch !== sessionEpoch.value) return
    cancel()
    items.value = items.value.filter(item => !containsHiddenTarget(item.status, status, action))
    ownFeedback = true
    invalidateDiscovery(result)
    ownFeedback = false
    cursor.value = null; resetRequired.value = true
  }

  function updateStatus(id: string, updates: Partial<MastodonStatus>) { applyStatusUpdate(statuses.value, id, updates) }
  return { items, statuses, reasons, loading, error, resetRequired, mode, disabled, autoPaused, generation, hasMore, clear, cancel, isFresh,
    refresh: () => load(true), loadMore: () => load(), continueLoading: () => load(false, true), feedback, updateStatus }
}
