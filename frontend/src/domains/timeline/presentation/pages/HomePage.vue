<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useTimelineStore } from '../stores/timelineStore'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { useStatusActions } from '@/domains/post/presentation/composables/useStatusActions'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import TimelineFeed from '../components/TimelineFeed.vue'

const timelineStore = useTimelineStore()
type TabType = 'for-you' | 'following'
const activeTab = ref<TabType>('for-you')

const currentTimeline = computed(() => activeTab.value === 'for-you'
  ? timelineStore.publicTimeline
  : timelineStore.homeTimeline)

const statuses = computed(() => {
  return activeTab.value === 'for-you'
    ? timelineStore.publicStatuses
    : timelineStore.homeStatuses
})

// Reuse the shared action handlers — optimistic update via the store.
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => timelineStore.updateStatus(id, updates),
)

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

function handlePosted(status: MastodonStatus) {
  timelineStore.prependStatus(status)
}

onMounted(() => {
  loadTimeline()
})
</script>

<template>
  <div class="home-page">
    <!-- Sticky header with tabs -->
    <div class="home-header">
      <h1 class="page-header__title">首页</h1>
      <div class="tab-bar" role="tablist">
        <button
          role="tab"
          :aria-selected="activeTab === 'for-you'"
          class="tab-bar__item"
          :class="{ 'tab-bar__item--active': activeTab === 'for-you' }"
          @click="onTabChange('for-you')"
        >
          <span class="tab-bar__text">推荐</span>
          <div v-if="activeTab === 'for-you'" class="tab-bar__indicator" />
        </button>
        <button
          role="tab"
          :aria-selected="activeTab === 'following'"
          class="tab-bar__item"
          :class="{ 'tab-bar__item--active': activeTab === 'following' }"
          @click="onTabChange('following')"
        >
          <span class="tab-bar__text">关注</span>
          <div v-if="activeTab === 'following'" class="tab-bar__indicator" />
        </button>
      </div>
    </div>

    <!-- Compose box -->
    <PostComposer @posted="handlePosted" />

    <!-- Timeline -->
    <TimelineFeed
      :statuses="statuses"
      :loading="currentTimeline.isLoading"
      :has-more="currentTimeline.hasMore"
      :error="currentTimeline.error"
      :empty-text="activeTab === 'for-you' ? '还没有人发布公开帖子。' : undefined"
      @load-more="handleLoadMore"
      @retry="loadTimeline"
      @reblog="toggleReblog"
      @favourite="toggleFavourite"
      @bookmark="toggleBookmark"
    />
  </div>
</template>

<style scoped>
.home-page {
  min-height: 100dvh;
}

/* Translucent navigation bar: title + segmented tabs on one material.
   The hairline under it only appears once the feed scrolls beneath
   (see html[data-scrolled] in global.css). */
.home-header {
  position: sticky;
  top: 0;
  z-index: var(--z-bar);
  padding-top: var(--safe-top);
  background: var(--material-bar);
  -webkit-backdrop-filter: var(--material-blur-bar);
  backdrop-filter: var(--material-blur-bar);
  box-shadow: 0 var(--hairline) 0 transparent;
  transition: box-shadow var(--duration-fast) var(--ease-out);
}

.home-header :deep(.page-header__title) {
  padding: var(--space-3) var(--gutter) var(--space-1);
  font-size: var(--text-title-3);
  line-height: var(--leading-title-3);
  letter-spacing: var(--tracking-title-3);
  font-weight: var(--weight-bold);
}

@media (max-width: 43.74rem) {
  /* Phone: large title, and composing moves to the FAB + sheet. */
  .home-header :deep(.page-header__title) {
    padding-top: var(--space-2);
    font-size: var(--text-large-title);
    line-height: var(--leading-large-title);
    letter-spacing: var(--tracking-large-title);
  }

  .home-page > .composer {
    display: none;
  }
}
</style>
