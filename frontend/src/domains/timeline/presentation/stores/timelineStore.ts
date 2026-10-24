import { sessionEpoch } from '@/shared/api/sessionScope'
import { revalidateTimeline } from '@/shared/utils/revalidateTimeline'
import { mergeById } from '@/shared/utils/pagination'
import { createNewerPrefetch, newerPrefetchKey } from '@/shared/utils/newerPrefetch'
import { ref, computed, watch } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonStatus } from '@/shared/types/mastodon'
import type { ManagedTimelineType } from '../../domain/entities'
import { timelineApi } from '../../infrastructure/timelineApi'
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
  const droppedHistory: Record<ManagedTimelineType, boolean> = { home: false, public: false }
  const pinned: Record<ManagedTimelineType, Set<string>> = { home: new Set(), public: new Set() }
  const paused: Record<ManagedTimelineType, boolean> = { home: false, public: false }
  const listGeneration: Record<ManagedTimelineType, number> = { home: 0, public: 0 }
  // Head page for the new-posts pill. Click applies it; it does not insert early.
  const prefetchedNewer = {
    home: createNewerPrefetch<MastodonStatus[]>(),
    public: createNewerPrefetch<MastodonStatus[]>(),
  }
  
  watch(sessionEpoch, () => {
    homeTab.value = 'following'
    for (const type of ['home', 'public'] as const) {
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

  // Generic fetch function to avoid code duplication
  async function fetchTimeline(type: ManagedTimelineType) {
    const timeline = getTimeline(type)
    if (pinned[type].size) { timeline.error = t('timeline.timelineStore.s1'); return }
    const myVersion = ++requestVersionMap[type]
    timeline.isLoading = true
    timeline.error = null
    failedRequest[type] = null
    
    try {
      if (USE_MOCK) {
        const { mockStatuses } = await import('@/shared/api/mockData')
        // Discard stale response — a newer request superseded this one
        if (myVersion !== requestVersionMap[type]) return
        timeline.statuses = mockStatuses
        timeline.hasMore = false
        return
      }
      
      const data = type === 'home'
        ? await timelineApi.getHomeTimeline({ limit: TIMELINE_PAGE_SIZE })
        : await timelineApi.getPublicTimeline({ limit: TIMELINE_PAGE_SIZE })
        
      // Discard stale response — a newer request superseded this one
      if (myVersion !== requestVersionMap[type]) return
      commitHead(type, data)
    } catch (e) {
      // Only set error if this is still the latest request
      if (myVersion !== requestVersionMap[type]) return
      timeline.error = e instanceof Error ? e.message : t('timeline.timelineStore.s2')
      failedRequest[type] = 'head'
      console.error(`Failed to fetch ${type} timeline:`, e)
    } finally {
      // Only clear loading state if this is still the latest request
      if (myVersion === requestVersionMap[type]) {
        timeline.isLoading = false
      }
    }
  }

  async function fetchHomeTimeline() {
    await fetchTimeline('home')
  }

  async function fetchPublicTimeline() {
    await fetchTimeline('public')
  }

  // Generic load more function
  async function loadMore(type: ManagedTimelineType) {
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
    
    try {
      const data = type === 'home'
        ? await timelineApi.getHomeTimeline({ max_id: lastId, limit: TIMELINE_PAGE_SIZE })
        : await timelineApi.getPublicTimeline({ max_id: lastId, limit: TIMELINE_PAGE_SIZE })
        
      // Discard stale response — a newer fetch replaced the timeline
      if (myVersion !== requestVersionMap[type]) return
      const before = timeline.statuses.length
      timeline.statuses = mergeById(timeline.statuses, data)
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
      if (myVersion === requestVersionMap[type]) {
        timeline.isLoading = false
      }
    }
  }

  async function loadMoreHome() {
    await loadMore('home')
  }

  async function loadMorePublic() {
    await loadMore('public')
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
      const first = timeline.statuses[0]!.id
      if (/^\d+$/.test(first)) previousCursors[type].push((BigInt(first) + 1n).toString())
      if (previousCursors[type].length > 128) { previousCursors[type].shift(); droppedHistory[type] = true }
      timeline.statuses.splice(0, TIMELINE_PAGE_SIZE)
    }
    timeline.hasPrevious = previousCursors[type].length > 0 || droppedHistory[type]
  }

  async function loadPrevious(type: ManagedTimelineType) {
    const timeline = getTimeline(type)
    if (timeline.isLoading || !timeline.hasPrevious) return
    if (timeline.statuses.slice(-TIMELINE_PAGE_SIZE).some(item => pinned[type].has(item.id))) { timeline.error = t('timeline.timelineStore.s1'); return }
    const cursor = previousCursors[type].at(-1)
    if (!cursor) { await fetchTimeline(type); return }
    const version = ++requestVersionMap[type]
    ++listGeneration[type]
    timeline.isLoading = true
    timeline.error = null
    failedRequest[type] = null
    try {
      const data = type === 'home'
        ? await timelineApi.getHomeTimeline({ max_id: cursor, limit: TIMELINE_PAGE_SIZE })
        : await timelineApi.getPublicTimeline({ max_id: cursor, limit: TIMELINE_PAGE_SIZE })
      if (version !== requestVersionMap[type]) return
      previousCursors[type].pop()
      timeline.statuses = mergeById(data, timeline.statuses).slice(0, TIMELINE_PAGE_SIZE * 8)
      timeline.hasPrevious = previousCursors[type].length > 0 || droppedHistory[type]
      timeline.hasMore = true
    } catch (e) {
      if (version === requestVersionMap[type]) {
        timeline.error = e instanceof Error ? e.message : t('timeline.timelineStore.s4')
        failedRequest[type] = 'previous'
      }
    }
    finally { if (version === requestVersionMap[type]) timeline.isLoading = false }
  }

  function retryTimeline(type: ManagedTimelineType) {
    if (getTimeline(type).isLoading) return
    if (failedRequest[type] === 'more') return loadMore(type)
    if (failedRequest[type] === 'previous') return loadPrevious(type)
    return fetchTimeline(type)
  }

  function cancelRevalidation() { ++listGeneration.home; ++listGeneration.public }

  function commitHead(type: ManagedTimelineType, data: MastodonStatus[]) {
    const timeline = getTimeline(type)
    ++listGeneration[type]
    previousCursors[type] = []
    droppedHistory[type] = false
    timeline.hasPrevious = false
    knownHead[type] = data.map(row => row.id)
    timeline.statuses = mergeById([], data)
    timeline.newer = []
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

  watch(() => newerPrefetchKey(homeTimeline.value.newer), () => scheduleNewerPrefetch('home'), { flush: 'sync' })
  watch(() => newerPrefetchKey(publicTimeline.value.newer), () => scheduleNewerPrefetch('public'), { flush: 'sync' })

  async function checkNewer(type: ManagedTimelineType) {
    const timeline = getTimeline(type)
    if (timeline.isLoading) return false
    const generation = ++listGeneration[type]
    const version = requestVersionMap[type]
    const valid = () => generation === listGeneration[type] && version === requestVersionMap[type]
    const result = await revalidateTimeline(timeline.statuses.slice(),
      (max_id, limit) => type === 'home' ? timelineApi.getHomeTimeline({ max_id, limit }) : timelineApi.getPublicTimeline({ max_id, limit }), valid, knownHead[type])
    if (result && valid()) {
      if (!knownHead[type]) knownHead[type] = result.statuses.slice(0, TIMELINE_PAGE_SIZE).map(row => row.id)
      timeline.statuses = result.statuses
      timeline.newer = result.newer.map(({ id }) => ({ id }))
    }
    return true
  }

  function revealNewer(type: ManagedTimelineType): Promise<void> | void {
    const timeline = getTimeline(type)
    if (!timeline.newer.length) return
    if (pinned[type].size) return fetchTimeline(type)
    const token = newerPrefetchKey(timeline.newer)
    const cached = prefetchedNewer[type].ready(token)
    if (cached) {
      applyPrefetchedHead(type, cached)
      return
    }
    return revealPrefetchOrFetch(type)
  }

  async function revealPrefetchOrFetch(type: ManagedTimelineType) {
    const prefetch = prefetchedNewer[type]
    const rows = await prefetch.consume()
    const timeline = getTimeline(type)
    const token = newerPrefetchKey(timeline.newer)
    if (!timeline.newer.length) return
    if (rows && token && prefetch.ready(token) === rows && !pinned[type].size) {
      applyPrefetchedHead(type, rows)
      return
    }
    await fetchTimeline(type)
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
    cancelRevalidation,
    loadPrevious,
    setPinnedIds,
    revealNewer,
    prependStatus, 
    updateStatus,
  }
})
