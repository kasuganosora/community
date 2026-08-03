<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useTimelineStore } from '../stores/timelineStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import type { MastodonStatus } from '@/shared/types/mastodon'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import TimelineFeed from '../components/TimelineFeed.vue'

const timelineStore = useTimelineStore()
const composerRef = ref<InstanceType<typeof PostComposer>>()
type TabType = 'for-you' | 'following'
const activeTab = ref<TabType>('for-you')

const statuses = computed(() => {
  return activeTab.value === 'for-you'
    ? timelineStore.publicStatuses
    : timelineStore.homeStatuses
})

function onTabChange(val: TabType) {
  activeTab.value = val
  loadTimeline()
}

function loadTimeline() {
  if (activeTab.value === 'for-you') {
    timelineStore.fetchPublicTimeline()
  } else {
    timelineStore.fetchHomeTimeline()
  }
}

async function handleLoadMore() {
  if (activeTab.value === 'for-you') {
    await timelineStore.loadMorePublic()
  } else {
    await timelineStore.loadMoreHome()
  }
}

async function handlePosted(content: string) {
  try {
    const status = await postApi.create({ status: content, visibility: 'public' })
    timelineStore.prependStatus(status)
    composerRef.value?.onPostSuccess()
  } catch (e) {
    console.error('Failed to create post:', e)
    composerRef.value?.onPostError('发布失败，请重试')
  }
}

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

onMounted(() => {
  loadTimeline()
})
</script>

<template>
  <div class="home-page">
    <!-- Sticky header with tabs -->
    <div class="home-header">
      <h1 class="page-header__title">Home</h1>
      <div class="tab-bar">
        <button
          class="tab-bar__item"
          :class="{ 'tab-bar__item--active': activeTab === 'for-you' }"
          @click="onTabChange('for-you')"
        >
          <span class="tab-bar__text">For you</span>
          <div v-if="activeTab === 'for-you'" class="tab-bar__indicator" />
        </button>
        <button
          class="tab-bar__item"
          :class="{ 'tab-bar__item--active': activeTab === 'following' }"
          @click="onTabChange('following')"
        >
          <span class="tab-bar__text">Following</span>
          <div v-if="activeTab === 'following'" class="tab-bar__indicator" />
        </button>
      </div>
    </div>

    <!-- Compose box -->
    <PostComposer ref="composerRef" @posted="handlePosted" />

    <!-- Timeline -->
    <TimelineFeed
      :statuses="statuses"
      :loading="timelineStore.isLoading"
      :has-more="activeTab === 'for-you'
        ? timelineStore.publicTimeline.hasMore
        : timelineStore.homeTimeline.hasMore"
      @load-more="handleLoadMore"
      @reblog="handleReblog"
      @favourite="handleFavourite"
      @bookmark="handleBookmark"
    />
  </div>
</template>

<style scoped>
.home-page {
  min-height: 100vh;
}

.home-header {
  position: sticky;
  top: 0;
  z-index: 10;
  background: var(--bg-header-blur);
  backdrop-filter: blur(12px);
  border-bottom: 1px solid var(--border-color);
}

.home-header :deep(.page-header__title) {
  padding: var(--space-3) var(--space-4) 0;
}
</style>
