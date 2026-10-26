import { sessionEpoch } from '@/shared/api/sessionScope'
import { createTimelineReadGuard } from '@/shared/utils/timelineReadGuard'
import { refreshTimelineHead, representedStatusIDs, revalidateTimeline } from '@/shared/utils/revalidateTimeline'
import { mergeById } from '@/shared/utils/pagination'
import { createNewerPrefetch, newerPrefetchKey } from '@/shared/utils/newerPrefetch'
import { ref, computed, watch } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonStatus } from '@/shared/types/mastodon'
import type { ManagedTimelineType } from '../../domain/entities'
import { timelineApi } from '../../infrastructure/timelineApi'
import { refreshStreamedStatus } from '@/domains/post/presentation/composables/useStatusActions'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'

import { USE_MOCK } from '@/shared/utils/env'

import { t } from '@/shared/i18n'
interface TimelineState {
  statuses: MastodonStatus[]
  isLoading: boolean
  hasMore: boolean
  error: string | null
  newer: { id: string }[]
  hasPrevious: boolean
}

export const useTimelineStore = defineStore('timeline', () => {
  const auth = useAuthStore()
  // Retain navigation choice within this session, never across account changes.
  const homeTab = ref<'for-you' | 'following' | 'global'>('following')
  // Separate state for each timeline type to avoid shared state issues
  const homeTimeline = ref<TimelineState>({
    statuses: [],
    isLoading: false,
    hasMore: true,
    error: null,
    newer: [],
    hasPrevious: false,
  })
  
  const publicTimeline = ref<TimelineState>({
    statuses: [],
    isLoading: false,
    hasMore: true,
    error: null,
    newer: [],
    hasPrevious: false,
  })

  // Per-timeline request version guard — prevents stale responses from
  // overwriting newer data when fetchTimeline / loadMore race with each other
  const requestVersionMap: Record<ManagedTimelineType, number> = { home: 0, public: 0 }
  const failedRequest: Record<ManagedTimelineType, 'head' | 'more' | 'previous' | null> = { home: null, public: null }
  const knownHead: Partial<Record<ManagedTimelineType, string[]>> = {}
  const previousCursors: Record<ManagedTimelineType, string[]> = { home: [], public: [] }
  const headCursor: Partial<Record<ManagedTimelineType, string>> = {}
  const readGuards = { home: createTimelineReadGuard(), public: createTimelineReadGuard() }
  const droppedHistory: Record<ManagedTimelineType, boolean> = { home: false, public: false }
  const pinned: Record<ManagedTimelineType, Set<string>> = { home: new Set(), public: new Set() }
  const paused: Record<ManagedTimelineType, boolean> = { home: false, public: false }
  const listGeneration: Record<ManagedTimelineType, number> = { home: 0, public: 0 }
  let revealVersion = 0
  // Head page for the new-posts pill. Click applies it; it does not insert early.
  const prefetchedNewer = {
    home: createNewerPrefetch<MastodonStatus[]>(),
    public: createNewerPrefetch<MastodonStatus[]>(),
  }
  
  watch(sessionEpoch, () => {
    ++revealVersion
    homeTab.value = 'following'
    for (const type of ['home', 'public'] as const) {
      delete flight[type]
      prefetchedNewer[type].clear()
      delete headCursor[type]
      ++requestVersionMap[type]
      ++listGeneration[type]
      failedRequest[type] = null
      const timeline = getTimeline(type)
      knownHead[type] = undefined; previousCursors[type] = []; droppedHistory[type] = false; timeline.hasPrevious = false; pinned[type].clear(); paused[type] = false
      timeline.statuses = []; timeline.newer = []; timeline.isLoading = false; timeline.hasMore = true; timeline.error = null
    }
  }, { flush: 'sync' })

  // A post you boosted is already on the timeline. Don't show that boost as a second copy.
  function withoutOwnRepeatBoost(list: MastodonStatus[]) {
    const me = auth.currentUser?.id
    if (!me) return list
    const originals = new Set(list.filter(status => !status.reblog?.id).map(status => status.id))
    return list.filter(status => !(status.reblog?.id && status.account?.id === me && originals.has(status.reblog.id)))
  }

  // Computed accessors for backward compatibility
  const homeStatuses = computed(() => withoutOwnRepeatBoost(homeTimeline.value.statuses))
  const publicStatuses = computed(() => withoutOwnRepeatBoost(publicTimeline.value.statuses))
  const isLoading = computed(() => 
    homeTimeline.value.isLoading || publicTimeline.value.isLoading
  )
  
  // Helper to get the correct timeline ref
  function getTimeline(type: ManagedTimelineType) {
    return type === 'home' ? homeTimeline.value : publicTimeline.value
  }

  const flight: Partial<Record<ManagedTimelineType, Promise<unknown>>> = {}

  /** One timeline HTTP per feed. A second caller waits on that request and does not open another. */
  function startFlight(type: ManagedTimelineType, work: () => Promise<unknown>) {
    const existing = flight[type]
    if (existing) return existing
    const run = work().finally(() => { if (flight[type] === run) delete flight[type] })
    flight[type] = run
    return run
  }

  // Generic fetch function to avoid code duplication
  function fetchTimeline(type: ManagedTimelineType) {
    return startFlight(type, () => fetchTimelineNow(type)).then(() => undefined)
  }

  async function fetchTimelineNow(type: ManagedTimelineType, current = () => true): Promise<boolean> {
    const timeline = getTimeline(type)
    if (pinned[type].size) { timeline.error = t('timeline.timelineStore.s1'); return false }
    const myVersion = ++requestVersionMap[type]
    timeline.isLoading = true
    timeline.error = null
    failedRequest[type] = null
    const read = readGuards[type].begin()
    const hintsAtStart = new Set(timeline.newer.map(row => row.id))
    
    try {
      if (USE_MOCK) {
        const { mockStatuses } = await import('@/shared/api/mockData')
        // Discard stale response — a newer request superseded this one
        if (myVersion !== requestVersionMap[type] || !current()) return false
        timeline.statuses = mockStatuses
        timeline.hasMore = false
        return true
      }
      
      const data = type === 'home'
        ? await timelineApi.getHomeTimeline({ limit: TIMELINE_PAGE_SIZE })
        : await timelineApi.getPublicTimeline({ limit: TIMELINE_PAGE_SIZE })
        
      // Discard stale response — a newer request superseded this one
      if (myVersion !== requestVersionMap[type] || !current()) return false
      commitHead(type, read.filter(data), hintsAtStart)
      return true
    } catch (e) {
      // Only set error if this is still the latest request
      if (myVersion !== requestVersionMap[type] || !current()) return false
      timeline.error = e instanceof Error ? e.message : t('timeline.timelineStore.s2')
      failedRequest[type] = 'head'
      console.error(`Failed to fetch ${type} timeline:`, e)
      return false
    } finally {
      read.close()
      // Only clear loading state if this is still the latest request
      if (myVersion === requestVersionMap[type]) {
        timeline.isLoading = false
      }
    }
  }

  function fetchHomeTimeline() {
    return fetchTimeline('home')
  }

  function fetchPublicTimeline() {
    return fetchTimeline('public')
  }

  // Generic load more function. A window walk already in flight is the next page; do not stack another max_id.
  function loadMore(type: ManagedTimelineType) {
    const timeline = getTimeline(type)
    if (flight[type] || !timeline.hasMore || timeline.isLoading) return
    return startFlight(type, () => loadMoreNow(type)).then(() => undefined)
  }

  async function loadMoreNow(type: ManagedTimelineType) {
    const timeline = getTimeline(type)
    if (!timeline.hasMore || timeline.isLoading) return
    
    if (timeline.statuses.length >= TIMELINE_PAGE_SIZE * 8 && timeline.statuses.slice(0, TIMELINE_PAGE_SIZE).some(item => pinned[type].has(item.id))) { paused[type] = true; timeline.hasMore = false; timeline.error = t('timeline.timelineStore.s1'); return }
    // Bump version so a concurrent fetchTimeline won't have its result
    // clobbered by this loadMore appending to stale state
    const myVersion = ++requestVersionMap[type]
    const lastId = timeline.statuses[timeline.statuses.length - 1]?.id
    if (!lastId) return
    
    timeline.isLoading = true
    timeline.error = null
    failedRequest[type] = null
    const read = readGuards[type].begin()
    
    try {
      const data = type === 'home'
        ? await timelineApi.getHomeTimeline({ max_id: lastId, limit: TIMELINE_PAGE_SIZE })
        : await timelineApi.getPublicTimeline({ max_id: lastId, limit: TIMELINE_PAGE_SIZE })
        
      // Discard stale response — a newer fetch replaced the timeline
      if (myVersion !== requestVersionMap[type]) return
      const before = timeline.statuses.length
      timeline.statuses = mergeById(timeline.statuses, read.filter(data))
      const grew = timeline.statuses.length > before
      trimWindow(type)
      ++listGeneration[type]
      timeline.hasMore = grew && data.length >= TIMELINE_PAGE_SIZE && data[data.length - 1]?.id !== lastId
    } catch (e) {
      if (myVersion !== requestVersionMap[type]) return
      timeline.error = e instanceof Error ? e.message : t('timeline.timelineStore.s3')
      failedRequest[type] = 'more'
      console.error(`Failed to load more ${type} timeline:`, e)
    } finally {
      read.close()
      if (myVersion === requestVersionMap[type]) {
        timeline.isLoading = false
      }
    }
  }

  function loadMoreHome() {
    return loadMore('home')
  }

  function loadMorePublic() {
    return loadMore('public')
  }

  function prependStatus(status: MastodonStatus) {
    const add = (list: MastodonStatus[], newer: { id: string }[], type: ManagedTimelineType) => {
      const index = newer.findIndex(item => item.id === status.id)
      if (index >= 0) newer.splice(index, 1)
      if (!list.some(s => s.id === status.id)) {
        if (list.length >= TIMELINE_PAGE_SIZE * 8 && pinned[type].has(list.at(-1)!.id)) { newer.unshift({ id: status.id }); return }
        list.unshift(status)
        if (list.length > TIMELINE_PAGE_SIZE * 8) { list.length = TIMELINE_PAGE_SIZE * 8; getTimeline(type).hasMore = true }
      }
    }
    add(homeTimeline.value.statuses, homeTimeline.value.newer, 'home')
    // Only public posts belong on the public timeline, including its first post.
    if (status.visibility === 'public') {
      add(publicTimeline.value.statuses, publicTimeline.value.newer, 'public')
    }
  }

  function setPinnedIds(type: ManagedTimelineType, ids: string[]) {
    pinned[type] = new Set(ids.slice(0, 8))
    const timeline = getTimeline(type)
    if (!pinned[type].size && timeline.error === t('timeline.timelineStore.s1')) timeline.error = null
    if (paused[type] && !timeline.statuses.slice(0, TIMELINE_PAGE_SIZE).some(item => pinned[type].has(item.id))) { paused[type] = false; timeline.hasMore = true; timeline.error = null }
  }

  function trimWindow(type: ManagedTimelineType) {
    const timeline = getTimeline(type)
    while (timeline.statuses.length > TIMELINE_PAGE_SIZE * 8) {
      // Keep the actual preceding page boundary. Serial id+1 may identify a
      // backdated remote post and is unrelated to publication order.
      previousCursors[type].push(headCursor[type] ?? '')
      headCursor[type] = timeline.statuses[TIMELINE_PAGE_SIZE - 1]!.id
      if (previousCursors[type].length > 128) { previousCursors[type].shift(); droppedHistory[type] = true }
      timeline.statuses.splice(0, TIMELINE_PAGE_SIZE)
    }
    timeline.hasPrevious = previousCursors[type].length > 0 || droppedHistory[type]
  }

  function loadPrevious(type: ManagedTimelineType) {
    const timeline = getTimeline(type)
    if (flight[type] || timeline.isLoading || !timeline.hasPrevious) return
    return startFlight(type, () => loadPreviousNow(type)).then(() => undefined)
  }

  async function loadPreviousNow(type: ManagedTimelineType) {
    const timeline = getTimeline(type)
    if (timeline.isLoading || !timeline.hasPrevious) return
    if (timeline.statuses.slice(-TIMELINE_PAGE_SIZE).some(item => pinned[type].has(item.id))) { timeline.error = t('timeline.timelineStore.s1'); return }
    const cursor = previousCursors[type].at(-1)
    if (cursor === undefined) { await fetchTimelineNow(type); return }
    const version = ++requestVersionMap[type]
    ++listGeneration[type]
    timeline.isLoading = true
    timeline.error = null
    failedRequest[type] = null
    const read = readGuards[type].begin()
    try {
      const params = { ...(cursor ? { max_id: cursor } : {}), limit: TIMELINE_PAGE_SIZE }
      const data = type === 'home'
        ? await timelineApi.getHomeTimeline(params)
        : await timelineApi.getPublicTimeline(params)
      if (version !== requestVersionMap[type]) return
      previousCursors[type].pop()
      headCursor[type] = cursor || undefined
      timeline.statuses = mergeById(read.filter(data), timeline.statuses).slice(0, TIMELINE_PAGE_SIZE * 8)
      timeline.hasPrevious = previousCursors[type].length > 0 || droppedHistory[type]
      timeline.hasMore = true
    } catch (e) {
      if (version === requestVersionMap[type]) {
        timeline.error = e instanceof Error ? e.message : t('timeline.timelineStore.s4')
        failedRequest[type] = 'previous'
      }
    }
    finally { read.close(); if (version === requestVersionMap[type]) timeline.isLoading = false }
  }

  function retryTimeline(type: ManagedTimelineType) {
    if (getTimeline(type).isLoading) return
    if (failedRequest[type] === 'more') return loadMore(type)
    if (failedRequest[type] === 'previous') return loadPrevious(type)
    return fetchTimeline(type)
  }

  function cancelRevalidation() {
    ++revealVersion
    // The in-flight walk is stale. Drop its flight slot so the tab we just
    // opened starts its own read; joining the cancelled one throws the
    // successful page away and leaves the empty list on screen.
    ++listGeneration.home
    ++listGeneration.public
    delete flight.home
    delete flight.public
  }

  function commitHead(type: ManagedTimelineType, data: MastodonStatus[], hintsAtStart?: Set<string>) {
    const timeline = getTimeline(type)
    ++listGeneration[type]
    delete headCursor[type]
    previousCursors[type] = []
    droppedHistory[type] = false
    timeline.hasPrevious = false
    knownHead[type] = data.map(row => row.id)
    timeline.statuses = mergeById([], data)
    const shown = representedStatusIDs(data)
    streamedNewer = true
    timeline.newer = hintsAtStart ? timeline.newer.filter(row => !hintsAtStart.has(row.id) && !shown.has(row.id)) : []
    streamedNewer = false
    prefetchedNewer[type].clear()
    timeline.hasMore = data.length >= TIMELINE_PAGE_SIZE
    timeline.error = null
    failedRequest[type] = null
  }

  function applyPrefetchedHead(type: ManagedTimelineType, data: MastodonStatus[]) {
    // Drop in-flight reads so they cannot put the pill back over the page we just showed.
    ++requestVersionMap[type]
    commitHead(type, data)
    getTimeline(type).isLoading = false
  }

  function scheduleNewerPrefetch(type: ManagedTimelineType) {
    const key = newerPrefetchKey(getTimeline(type).newer)
    const prefetch = prefetchedNewer[type]
    if (!key) {
      prefetch.clear()
      return
    }
    prefetch.schedule(key, async () => {
      const rows = type === 'home'
        ? await timelineApi.getHomeTimeline({ limit: TIMELINE_PAGE_SIZE })
        : await timelineApi.getPublicTimeline({ limit: TIMELINE_PAGE_SIZE })
      if (!Array.isArray(rows)) throw new Error('timeline prefetch failed')
      return rows
    })
  }

  // Streamed pill updates must not prefetch a head page per status.
  let streamedNewer = false
  watch(() => newerPrefetchKey(homeTimeline.value.newer), () => { if (!streamedNewer) scheduleNewerPrefetch('home') }, { flush: 'sync' })
  watch(() => newerPrefetchKey(publicTimeline.value.newer), () => { if (!streamedNewer) scheduleNewerPrefetch('public') }, { flush: 'sync' })

  function fetchPage(type: ManagedTimelineType, maxId: string | undefined, limit: number) {
    const cursor = maxId ? { max_id: maxId, limit } : { limit }
    return type === 'home' ? timelineApi.getHomeTimeline(cursor) : timelineApi.getPublicTimeline(cursor)
  }

  function mergePill(type: ManagedTimelineType, incoming: { id: string }[], visible: { id: string }[]) {
    const shown = representedStatusIDs(visible)
    const seen = new Set<string>()
    const merged: { id: string }[] = []
    for (const item of [...incoming, ...getTimeline(type).newer]) {
      if (shown.has(item.id) || seen.has(item.id) || merged.length >= TIMELINE_PAGE_SIZE) continue
      seen.add(item.id)
      merged.push(item)
    }
    return merged
  }

  async function readTimeline(type: ManagedTimelineType, scope: 'head' | 'window') {
    const timeline = getTimeline(type)
    // The page's own load is already the fresh read. Retrying here starts a second walk.
    if (timeline.isLoading) return true
    const generation = ++listGeneration[type]
    const version = requestVersionMap[type]
    const valid = () => generation === listGeneration[type] && version === requestVersionMap[type]
    const read = readGuards[type].begin()
    try {
      if (scope === 'head') {
        const result = await refreshTimelineHead(timeline.statuses.slice(), async (maxId, limit) => read.filter(await fetchPage(type, maxId, limit)), valid, knownHead[type])
        if (result?.inaccessible && valid()) { commitHead(type, []); prefetchedNewer[type].clear(); return true }
        if (result && valid()) {
          if (!knownHead[type]) knownHead[type] = result.statuses.slice(0, TIMELINE_PAGE_SIZE).map(row => row.id)
          timeline.statuses = read.filter(result.statuses)
          const newer = mergePill(type, result.newer.filter(row => !read.wasRemoved(row.id)), timeline.statuses)
          const key = newerPrefetchKey(newer)
          const headIds = new Set(result.head.map(row => row.id))
          if (key && newer.every(item => headIds.has(item.id))) prefetchedNewer[type].adopt(key, read.filter(result.head))
          streamedNewer = true
          timeline.newer = newer
          streamedNewer = false
        }
        return true
      }
      let seenHead: MastodonStatus[] | undefined
      const result = await revalidateTimeline(timeline.statuses.slice(), async (maxId, limit) => {
        const page = read.filter(await fetchPage(type, maxId, limit))
        if (maxId === undefined) seenHead = page
        return page
      }, valid, knownHead[type])
      if (result?.inaccessible && valid()) { commitHead(type, []); prefetchedNewer[type].clear(); return true }
      if (result && valid()) {
        if (!knownHead[type]) knownHead[type] = result.statuses.slice(0, TIMELINE_PAGE_SIZE).map(row => row.id)
        timeline.statuses = read.filter(result.statuses)
        const newer = mergePill(type, result.newer.filter(row => !read.wasRemoved(row.id)), timeline.statuses)
        const key = newerPrefetchKey(newer)
        if (seenHead && key && newer.every(item => seenHead!.some(row => row.id === item.id))) prefetchedNewer[type].adopt(key, read.filter(seenHead))
        streamedNewer = true
        timeline.newer = newer
        streamedNewer = false
      }
      return true
    } finally { read.close() }
  }

  /** One home or public read at a time. A second caller waits on that read instead of starting another walk. */
  function checkNewer(type: ManagedTimelineType, scope: 'head' | 'window' = 'window') {
    return startFlight(type, () => readTimeline(type, scope)).then(() => true)
  }

  function refreshEdited(id: string) {
    void refreshStreamedStatus(id, updateStatus)
  }

  /** Apply one streamed status id. New posts stay behind the pill; this does not fetch the timeline. */
  function applyStreamed(type: ManagedTimelineType, status: { event: 'update' | 'delete' | 'status.update', id: string }) {
    const id = status.id
    if (!/^\d+$/.test(id)) return
    const timeline = getTimeline(type)
    // Delete and in-place edits match the card or the boost it wraps.
    // A reply parent or quote that is only previewed inside another card stays put.
    const displayed = (row: { id: string, reblog?: { id?: string } | null }) => row.id === id || row.reblog?.id === id
    if (status.event === 'delete') {
      readGuards[type].remove(id)
      prefetchedNewer[type].clear()
      timeline.statuses = timeline.statuses.filter(row => !displayed(row))
      streamedNewer = true
      timeline.newer = timeline.newer.filter(row => row.id !== id)
      streamedNewer = false
      return
    }
    if (status.event === 'status.update') {
      // The edited card may only exist in the pending head, outside the
      // retained reading window. Do not reveal its old cached body.
      prefetchedNewer[type].clear()
      if (timeline.statuses.some(displayed)) refreshEdited(id)
      return
    }
    if (representedStatusIDs(timeline.statuses).has(id) || timeline.newer.some(row => row.id === id) || knownHead[type]?.includes(id)) return
    streamedNewer = true
    timeline.newer = [...timeline.newer.slice(-(TIMELINE_PAGE_SIZE - 1)), { id }]
    streamedNewer = false
  }

  function revealNewer(type: ManagedTimelineType): Promise<boolean> | boolean {
    const timeline = getTimeline(type)
    if (!timeline.newer.length) return false
    const epoch = sessionEpoch.value, scope = revealVersion
    const current = () => epoch === sessionEpoch.value && scope === revealVersion
    const pending = flight[type]
    if (pending) {
      return pending.catch(() => {}).then(() => current() ? revealNewer(type) : false)
    }
    if (pinned[type].size) { timeline.error = t('timeline.timelineStore.s1'); return false }
    const token = newerPrefetchKey(timeline.newer)
    const cached = prefetchedNewer[type].ready(token)
    if (cached) {
      applyPrefetchedHead(type, cached)
      return true
    }
    return revealPrefetchOrFetch(type, current)
  }

  async function revealPrefetchOrFetch(type: ManagedTimelineType, current: () => boolean): Promise<boolean> {
    const prefetch = prefetchedNewer[type]
    const rows = await prefetch.consume()
    if (!current()) return false
    const timeline = getTimeline(type)
    const token = newerPrefetchKey(timeline.newer)
    if (!timeline.newer.length) return false
    if (rows && token && prefetch.ready(token) === rows && !pinned[type].size) {
      applyPrefetchedHead(type, rows)
      return true
    }
    // Another read may have started while waiting for the prefetch. Queue the
    // reveal behind it rather than treating that unrelated read as a success.
    if (flight[type]) return revealNewer(type)
    return startFlight(type, () => fetchTimelineNow(type, current)) as Promise<boolean>
  }

  function updateStatus(statusId: string, updates: Partial<MastodonStatus>) {
    const updateInList = (list: MastodonStatus[]) => {
      for (const item of list) {
        // Match the top-level status ...
        if (item.id === statusId) {
          Object.assign(item, updates)
        }
        // ... or the reblog wrapper's inner status. PostCard emits the
        // displayStatus (the reblog content) for actions, so the id being
        // updated is the inner status id, not the wrapper's.
        if (item.reblog?.id === statusId) {
          Object.assign(item.reblog, updates)
        }
      }
    }
    updateInList(homeTimeline.value.statuses)
    updateInList(publicTimeline.value.statuses)
  }

  return {
    homeTab,
    homeStatuses, 
    publicStatuses, 
    isLoading,
    homeTimeline: computed(() => homeTimeline.value),
    publicTimeline: computed(() => publicTimeline.value),
    fetchHomeTimeline, 
    fetchPublicTimeline,
    loadMoreHome, 
    loadMorePublic,
    retryTimeline,
    checkNewer,
    applyStreamed,
    cancelRevalidation,
    loadPrevious,
    setPinnedIds,
    revealNewer,
    prependStatus, 
    updateStatus,
  }
})
