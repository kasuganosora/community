import { ref } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { timelineApi } from '../../infrastructure/timelineApi'
import { mockStatuses } from '@/shared/api/mockData'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'

const USE_MOCK = !import.meta.env.VITE_API_URL

export const useTimelineStore = defineStore('timeline', () => {
  const homeStatuses = ref<MastodonStatus[]>([])
  const publicStatuses = ref<MastodonStatus[]>([])
  const isLoading = ref(false)
  const hasMore = ref(true)

  async function fetchHomeTimeline() {
    isLoading.value = true
    try {
      if (USE_MOCK) {
        homeStatuses.value = mockStatuses
        hasMore.value = false
        return
      }
      const data = await timelineApi.getHomeTimeline({ limit: TIMELINE_PAGE_SIZE })
      homeStatuses.value = data
      hasMore.value = data.length >= TIMELINE_PAGE_SIZE
    } finally {
      isLoading.value = false
    }
  }

  async function fetchPublicTimeline() {
    isLoading.value = true
    try {
      if (USE_MOCK) {
        publicStatuses.value = mockStatuses
        hasMore.value = false
        return
      }
      const data = await timelineApi.getPublicTimeline({ limit: TIMELINE_PAGE_SIZE })
      publicStatuses.value = data
      hasMore.value = data.length >= TIMELINE_PAGE_SIZE
    } finally {
      isLoading.value = false
    }
  }

  async function loadMoreHome() {
    if (!hasMore.value || isLoading.value) return
    const lastId = homeStatuses.value[homeStatuses.value.length - 1]?.id
    if (!lastId) return
    isLoading.value = true
    try {
      const data = await timelineApi.getHomeTimeline({ max_id: lastId, limit: TIMELINE_PAGE_SIZE })
      homeStatuses.value.push(...data)
      hasMore.value = data.length >= TIMELINE_PAGE_SIZE
    } finally {
      isLoading.value = false
    }
  }

  async function loadMorePublic() {
    if (!hasMore.value || isLoading.value) return
    const lastId = publicStatuses.value[publicStatuses.value.length - 1]?.id
    if (!lastId) return
    isLoading.value = true
    try {
      const data = await timelineApi.getPublicTimeline({ max_id: lastId, limit: TIMELINE_PAGE_SIZE })
      publicStatuses.value.push(...data)
      hasMore.value = data.length >= TIMELINE_PAGE_SIZE
    } finally {
      isLoading.value = false
    }
  }

  function prependStatus(status: MastodonStatus) {
    homeStatuses.value.unshift(status)
  }

  function updateStatus(statusId: string, updates: Partial<MastodonStatus>) {
    const updateInList = (list: MastodonStatus[]) => {
      const idx = list.findIndex(s => s.id === statusId)
      const item = list[idx]
      if (idx !== -1 && item) {
        Object.assign(item, updates)
      }
    }
    updateInList(homeStatuses.value)
    updateInList(publicStatuses.value)
  }

  return {
    homeStatuses, publicStatuses, isLoading, hasMore,
    fetchHomeTimeline, fetchPublicTimeline,
    loadMoreHome, loadMorePublic,
    prependStatus, updateStatus,
  }
})
