<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useTimelineStore } from '../stores/timelineStore'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import TimelineFeed from '../components/TimelineFeed.vue'

const timelineStore = useTimelineStore()
const activeTab = ref('for-you')

const statuses = computed(() => {
  return activeTab.value === 'for-you'
    ? timelineStore.publicStatuses
    : timelineStore.homeStatuses
})

function onTabChange(val: string) {
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

function handleLoadMore() {
  if (activeTab.value === 'for-you') {
    timelineStore.loadMorePublic()
  } else {
    timelineStore.loadMoreHome()
  }
}

function handlePosted(content: string) {
  // TODO: Call real API to create status
  console.log('New post:', content)
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
    <PostComposer @posted="handlePosted" />

    <!-- Timeline -->
    <TimelineFeed
      :statuses="statuses"
      :loading="timelineStore.isLoading"
      @load-more="handleLoadMore"
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
  background: rgba(0, 0, 0, 0.65);
  backdrop-filter: blur(12px);
  border-bottom: 1px solid var(--border-color);
}

.home-header :deep(.page-header__title) {
  padding: 12px 16px 0;
}
</style>
