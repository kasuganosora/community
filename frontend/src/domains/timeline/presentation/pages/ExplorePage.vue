<script setup lang="ts">
import { onMounted } from 'vue'
import { useTimelineStore } from '../stores/timelineStore'
import TimelineFeed from '../components/TimelineFeed.vue'

const timelineStore = useTimelineStore()

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
    />
  </div>
</template>

<style scoped>
.explore-page {
  min-height: 100vh;
}
</style>
