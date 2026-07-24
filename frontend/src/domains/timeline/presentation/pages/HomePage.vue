<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useTimelineStore } from '../stores/timelineStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import TimelineFeed from '../components/TimelineFeed.vue'

const timelineStore = useTimelineStore()
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
  } catch (e) {
    console.error('Failed to create post:', e)
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
  background: var(--bg-header-blur);
  backdrop-filter: blur(12px);
  border-bottom: 1px solid var(--border-color);
}

.home-header :deep(.page-header__title) {
  padding: var(--space-3) var(--space-4) 0;
}
</style>
