import { sessionEpoch } from '@/shared/api/sessionScope'
import { mergeById } from '@/shared/utils/pagination'
import { ref, computed, watch } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonStatus } from '@/shared/types/mastodon'
import type { ManagedTimelineType } from '../../domain/entities'
import { timelineApi } from '../../infrastructure/timelineApi'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'

import { USE_MOCK } from '@/shared/utils/env'

interface TimelineState {
  statuses: MastodonStatus[]
  isLoading: boolean
  hasMore: boolean
  error: string | null
}

export const useTimelineStore = defineStore('timeline', () => {
  // Separate state for each timeline type to avoid shared state issues
  const homeTimeline = ref<TimelineState>({
    statuses: [],
    isLoading: false,
    hasMore: true,
    error: null,
  })
  
  const publicTimeline = ref<TimelineState>({
    statuses: [],
    isLoading: false,
    hasMore: true,
    error: null,
  })

  // Per-timeline request version guard — prevents stale responses from
  // overwriting newer data when fetchTimeline / loadMore race with each other
  const requestVersionMap: Record<ManagedTimelineType, number> = { home: 0, public: 0 }
  
  watch(sessionEpoch, () => {
    for (const type of ['home', 'public'] as const) {
      ++requestVersionMap[type]
      const timeline = getTimeline(type)
      timeline.statuses = []; timeline.isLoading = false; timeline.hasMore = true; timeline.error = null
    }
  }, { flush: 'sync' })

  // Computed accessors for backward compatibility
  const homeStatuses = computed(() => homeTimeline.value.statuses)
  const publicStatuses = computed(() => publicTimeline.value.statuses)
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
      timeline.statuses = mergeById([], data)
      timeline.hasMore = data.length >= TIMELINE_PAGE_SIZE
    } catch (e) {
      // Only set error if this is still the latest request
      if (myVersion !== requestVersionMap[type]) return
      timeline.error = e instanceof Error ? e.message : 'Failed to load timeline'
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
      timeline.statuses = mergeById(timeline.statuses, data)
      timeline.hasMore = data.length >= TIMELINE_PAGE_SIZE && data[data.length - 1]?.id !== lastId
    } catch (e) {
      if (myVersion !== requestVersionMap[type]) return
      timeline.error = e instanceof Error ? e.message : 'Failed to load more'
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
    const add = (list: MastodonStatus[]) => {
      if (!list.some(s => s.id === status.id)) list.unshift(status)
    }
    add(homeTimeline.value.statuses)
    // Only public posts belong on the public timeline, including its first post.
    if (status.visibility === 'public') {
      add(publicTimeline.value.statuses)
    }
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
    prependStatus, 
    updateStatus,
  }
})
