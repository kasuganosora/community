import { sessionEpoch } from '@/shared/api/sessionScope'
import { mergeById } from '@/shared/utils/pagination'
import { ref, computed, watch } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonStatus } from '@/shared/types/mastodon'
import type { ManagedTimelineType } from '../../domain/entities'
import { timelineApi } from '../../infrastructure/timelineApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'

import { USE_MOCK } from '@/shared/utils/env'

interface TimelineState {
  statuses: MastodonStatus[]
  isLoading: boolean
  hasMore: boolean
  error: string | null
  newer: MastodonStatus[]
}

export const useTimelineStore = defineStore('timeline', () => {
  const auth = useAuthStore()
  // Separate state for each timeline type to avoid shared state issues
  const homeTimeline = ref<TimelineState>({
    statuses: [],
    isLoading: false,
    hasMore: true,
    error: null,
    newer: [],
  })
  
  const publicTimeline = ref<TimelineState>({
    statuses: [],
    isLoading: false,
    hasMore: true,
    error: null,
    newer: [],
  })

  // Per-timeline request version guard — prevents stale responses from
  // overwriting newer data when fetchTimeline / loadMore race with each other
  const requestVersionMap: Record<ManagedTimelineType, number> = { home: 0, public: 0 }
  const listGeneration: Record<ManagedTimelineType, number> = { home: 0, public: 0 }
  
  watch(sessionEpoch, () => {
    for (const type of ['home', 'public'] as const) {
      ++requestVersionMap[type]
      ++listGeneration[type]
      const timeline = getTimeline(type)
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
    const myVersion = ++requestVersionMap[type]
    timeline.isLoading = true
    timeline.error = null
    
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
      ++listGeneration[type]
      timeline.statuses = mergeById([], data)
      timeline.newer = []
      timeline.hasMore = data.length >= TIMELINE_PAGE_SIZE
    } catch (e) {
      // Only set error if this is still the latest request
      if (myVersion !== requestVersionMap[type]) return
      timeline.error = e instanceof Error ? e.message : '时间线加载失败'
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
    
    // Bump version so a concurrent fetchTimeline won't have its result
    // clobbered by this loadMore appending to stale state
    const myVersion = ++requestVersionMap[type]
    const lastId = timeline.statuses[timeline.statuses.length - 1]?.id
    if (!lastId) return
    
    timeline.isLoading = true
    timeline.error = null
    
    try {
      const data = type === 'home'
        ? await timelineApi.getHomeTimeline({ max_id: lastId, limit: TIMELINE_PAGE_SIZE })
        : await timelineApi.getPublicTimeline({ max_id: lastId, limit: TIMELINE_PAGE_SIZE })
        
      // Discard stale response — a newer fetch replaced the timeline
      if (myVersion !== requestVersionMap[type]) return
      const before = timeline.statuses.length
      timeline.statuses = mergeById(timeline.statuses, data)
      const grew = timeline.statuses.length > before
      timeline.hasMore = grew && data.length >= TIMELINE_PAGE_SIZE && data[data.length - 1]?.id !== lastId
    } catch (e) {
      if (myVersion !== requestVersionMap[type]) return
      timeline.error = e instanceof Error ? e.message : '加载更多失败'
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
    const add = (list: MastodonStatus[], newer: MastodonStatus[]) => {
      const index = newer.findIndex(item => item.id === status.id)
      if (index >= 0) newer.splice(index, 1)
      if (!list.some(s => s.id === status.id)) list.unshift(status)
    }
    add(homeTimeline.value.statuses, homeTimeline.value.newer)
    // Only public posts belong on the public timeline, including its first post.
    if (status.visibility === 'public') {
      add(publicTimeline.value.statuses, publicTimeline.value.newer)
    }
  }

  async function checkNewer(type: ManagedTimelineType) {
    const timeline = getTimeline(type)
    const sinceId = timeline.statuses[0]?.id
    if (!sinceId || timeline.isLoading) return
    const generation = listGeneration[type]
    try {
      const data = type === 'home'
        ? await timelineApi.getHomeTimeline({ since_id: sinceId, limit: TIMELINE_PAGE_SIZE })
        : await timelineApi.getPublicTimeline({ since_id: sinceId, limit: TIMELINE_PAGE_SIZE })
      if (generation !== listGeneration[type] || timeline.statuses[0]?.id !== sinceId) return
      const seen = new Set(timeline.statuses.map(status => status.id))
      const fresh = data.filter(status => {
        if (seen.has(status.id)) return false
        seen.add(status.id)
        return true
      })
      fresh.sort((a, b) => (a.id.length === b.id.length ? (a.id < b.id ? 1 : -1) : b.id.length - a.id.length))
      timeline.newer = fresh
    } catch (e) {
      console.error(`Failed to check newer ${type} posts:`, e)
    }
  }

  function revealNewer(type: ManagedTimelineType) {
    const timeline = getTimeline(type)
    if (!timeline.newer.length) return
    timeline.statuses = mergeById(timeline.newer, timeline.statuses)
    timeline.newer = []
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
    homeStatuses, 
    publicStatuses, 
    isLoading,
    homeTimeline: computed(() => homeTimeline.value),
    publicTimeline: computed(() => publicTimeline.value),
    fetchHomeTimeline, 
    fetchPublicTimeline,
    loadMoreHome, 
    loadMorePublic,
    checkNewer,
    revealNewer,
    prependStatus, 
    updateStatus,
  }
})
