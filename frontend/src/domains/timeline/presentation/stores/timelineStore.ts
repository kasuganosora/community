import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { timelineApi } from '../../infrastructure/timelineApi'
import { mockStatuses } from '@/shared/api/mockData'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'

const USE_MOCK = !import.meta.env.VITE_API_URL

export type TimelineType = 'home' | 'public'

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
  
  // Computed accessors for backward compatibility
  const homeStatuses = computed(() => homeTimeline.value.statuses)
  const publicStatuses = computed(() => publicTimeline.value.statuses)
  const isLoading = computed(() => 
    homeTimeline.value.isLoading || publicTimeline.value.isLoading
  )
  
  // Helper to get the correct timeline ref
  function getTimeline(type: TimelineType) {
    return type === 'home' ? homeTimeline.value : publicTimeline.value
  }

  // Generic fetch function to avoid code duplication
  async function fetchTimeline(type: TimelineType) {
    const timeline = getTimeline(type)
    timeline.isLoading = true
    timeline.error = null
    
    try {
      if (USE_MOCK) {
        timeline.statuses = mockStatuses
        timeline.hasMore = false
        return
      }
      
      const data = type === 'home'
        ? await timelineApi.getHomeTimeline({ limit: TIMELINE_PAGE_SIZE })
        : await timelineApi.getPublicTimeline({ limit: TIMELINE_PAGE_SIZE })
        
      timeline.statuses = data
      timeline.hasMore = data.length >= TIMELINE_PAGE_SIZE
    } catch (e) {
      timeline.error = e instanceof Error ? e.message : 'Failed to load timeline'
      console.error(`Failed to fetch ${type} timeline:`, e)
    } finally {
      timeline.isLoading = false
    }
  }

  async function fetchHomeTimeline() {
    await fetchTimeline('home')
  }

  async function fetchPublicTimeline() {
    await fetchTimeline('public')
  }

  // Generic load more function
  async function loadMore(type: TimelineType) {
    const timeline = getTimeline(type)
    if (!timeline.hasMore || timeline.isLoading) return
    
    const lastId = timeline.statuses[timeline.statuses.length - 1]?.id
    if (!lastId) return
    
    timeline.isLoading = true
    timeline.error = null
    
    try {
      const data = type === 'home'
        ? await timelineApi.getHomeTimeline({ max_id: lastId, limit: TIMELINE_PAGE_SIZE })
        : await timelineApi.getPublicTimeline({ max_id: lastId, limit: TIMELINE_PAGE_SIZE })
        
      timeline.statuses.push(...data)
      timeline.hasMore = data.length >= TIMELINE_PAGE_SIZE
    } catch (e) {
      timeline.error = e instanceof Error ? e.message : 'Failed to load more'
      console.error(`Failed to load more ${type} timeline:`, e)
    } finally {
      timeline.isLoading = false
    }
  }

  async function loadMoreHome() {
    await loadMore('home')
  }

  async function loadMorePublic() {
    await loadMore('public')
  }

  function prependStatus(status: MastodonStatus) {
    homeTimeline.value.statuses.unshift(status)
  }

  function updateStatus(statusId: string, updates: Partial<MastodonStatus>) {
    const updateInList = (list: MastodonStatus[]) => {
      const idx = list.findIndex(s => s.id === statusId)
      if (idx !== -1) {
        const item = list[idx]
        if (item) {
          Object.assign(item, updates)
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
