<script setup lang="ts">
import { onMounted } from 'vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { useTimelineStore } from '../stores/timelineStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import TimelineFeed from '../components/TimelineFeed.vue'

const timelineStore = useTimelineStore()

onMounted(() => {
  timelineStore.fetchPublicTimeline()
})

async function handleReblog(status: MastodonStatus) {
  try {
    const updated = status.reblogged
      ? await postApi.unreblog(status.id)
      : await postApi.reblog(status.id)
    timelineStore.updateStatus(status.id, { reblogged: updated.reblogged, reblogs_count: updated.reblogs_count })
  } catch (e) {
    console.error('Failed to toggle reblog:', e)
  }
}

async function handleFavourite(status: MastodonStatus) {
  try {
    const updated = status.favourited
      ? await postApi.unfavourite(status.id)
      : await postApi.favourite(status.id)
    timelineStore.updateStatus(status.id, { favourited: updated.favourited, favourites_count: updated.favourites_count })
  } catch (e) {
    console.error('Failed to toggle favourite:', e)
  }
}

async function handleBookmark(status: MastodonStatus) {
  try {
    const updated = status.bookmarked
      ? await postApi.unbookmark(status.id)
      : await postApi.bookmark(status.id)
    timelineStore.updateStatus(status.id, { bookmarked: updated.bookmarked })
  } catch (e) {
    console.error('Failed to toggle bookmark:', e)
  }
}
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
      @reblog="handleReblog"
      @favourite="handleFavourite"
      @bookmark="handleBookmark"
    />
  </div>
</template>

<style scoped>
.explore-page {
  min-height: 100vh;
}
</style>
