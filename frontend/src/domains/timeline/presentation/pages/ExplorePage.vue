<script setup lang="ts">
import { onMounted } from 'vue'
import { useTimelineStore } from '../stores/timelineStore'
import { useStatusActions } from '@/domains/post/presentation/composables/useStatusActions'
import TimelineFeed from '../components/TimelineFeed.vue'

const timelineStore = useTimelineStore()

// Reuse the shared action handlers — optimistic update via the store.
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => timelineStore.updateStatus(id, updates),
)

onMounted(() => {
  timelineStore.fetchPublicTimeline()
})
</script>

<template>
  <div class="explore-page">
    <div class="page-header">
      <h1 class="page-header__title">Explore</h1>
    </div>
    <TimelineFeed
      :statuses="timelineStore.publicStatuses"
      :loading="timelineStore.isLoading"
      @load-more="timelineStore.loadMorePublic"
      @reblog="toggleReblog"
      @favourite="toggleFavourite"
      @bookmark="toggleBookmark"
    />
  </div>
</template>

<style scoped>
.explore-page {
  min-height: 100dvh;
}
</style>
