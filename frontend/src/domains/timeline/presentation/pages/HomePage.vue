<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { useFeedStream } from '@/shared/composables/useFeedStream'
import { useTimelineStore } from '../stores/timelineStore'
import type { MastodonStatus } from '@/shared/types/mastodon'
import type { ManagedTimelineType } from '../../domain/entities'
import { useStatusActions } from '@/domains/post/presentation/composables/useStatusActions'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import TimelineFeed from '../components/TimelineFeed.vue'
import TabBar from '@/shared/components/common/TabBar.vue'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'
import { saveListSnapshot, readListSnapshot, restoreListScroll } from '@/shared/utils/listMemory'

const timelineStore = useTimelineStore()
type TabType = 'for-you' | 'following'
const HOME_TABS: { value: TabType; label: string }[] = [
  { value: 'for-you', label: '推荐' },
  { value: 'following', label: '关注' },
]
const activeTab = computed({
  get: () => timelineStore.homeTab,
  set: (tab: TabType) => { timelineStore.homeTab = tab },
})

const currentTimeline = computed(() => activeTab.value === 'for-you'
  ? timelineStore.publicTimeline
  : timelineStore.homeTimeline)

const statuses = computed(() => {
  return activeTab.value === 'for-you'
    ? timelineStore.publicStatuses
    : timelineStore.homeStatuses
})

const newerCount = computed(() => currentTimeline.value.newer.length)
const newerLabel = computed(() => newerCount.value >= TIMELINE_PAGE_SIZE
  ? '有很多新帖子'
  : `${newerCount.value} 条新帖子`)

function timelineType(tab: TabType = activeTab.value): ManagedTimelineType {
  return tab === 'for-you' ? 'public' : 'home'
}

function rememberPosition(tab: TabType = activeTab.value) {
  const timeline = tab === 'for-you' ? timelineStore.publicTimeline : timelineStore.homeTimeline
  saveListSnapshot(`home:${tab}`, [], timeline.hasMore)
}

function restorePosition(tab: TabType = activeTab.value) {
  const snap = readListSnapshot(`home:${tab}`)
  if (snap) restoreListScroll(snap.scroll)
}

// Reuse the shared action handlers — optimistic update via the store.
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => timelineStore.updateStatus(id, updates),
)

function onTabChange(val: TabType) {
  rememberPosition()
  timelineStore.cancelRevalidation()
  activeTab.value = val
  openTimeline(false)
}

async function loadTimeline() {
  if (activeTab.value === 'for-you') {
    await timelineStore.fetchPublicTimeline()
  } else {
    await timelineStore.fetchHomeTimeline()
  }
}

function openTimeline(restore: boolean) {
  if (statuses.value.length === 0) {
    loadTimeline()
    return
  }
  if (restore) nextTick(() => restorePosition())
  void timelineStore.checkNewer(timelineType()).catch(() => {})
}

async function reloadHead() { await loadTimeline(); if (!currentTimeline.value.error) window.scrollTo(0, 0) }

async function handleLoadMore() {
  if (activeTab.value === 'for-you') {
    await timelineStore.loadMorePublic()
  } else {
    await timelineStore.loadMoreHome()
  }
}

function retryTimeline() {
  if (statuses.value.length > 0 && currentTimeline.value.hasMore) void handleLoadMore()
  else loadTimeline()
}

async function revealNew() {
  await timelineStore.revealNewer(timelineType())
  if (!currentTimeline.value.error) window.scrollTo(0, 0)
  rememberPosition()
}

function handlePosted(status: MastodonStatus) {
  timelineStore.prependStatus(status)
}

useFeedStream(computed(() => `stream=${timelineType() === 'home' ? 'user' : 'public'}`),
  () => timelineStore.checkNewer(timelineType()))
onMounted(() => openTimeline(true))
onBeforeRouteLeave(() => rememberPosition())
onBeforeUnmount(() => timelineStore.cancelRevalidation())
</script>

<template>
  <div class="home-page">
    <!-- Sticky header with tabs -->
    <div class="home-header">
      <h1 class="page-header__title">首页</h1>
      <TabBar
        :model-value="activeTab"
        :options="HOME_TABS"
        label="时间线"
        @update:model-value="onTabChange"
      />
    </div>

    <!-- Compose box -->
    <PostComposer @posted="handlePosted" />

    <button v-if="newerCount" type="button" class="home-new" @click="revealNew">
      {{ newerLabel }}，点击查看
    </button>

    <!-- Timeline -->
    <TimelineFeed
      :statuses="statuses"
      :scope-key="timelineType()"
      :has-previous="currentTimeline.hasPrevious"
      @pinned-ids="ids => timelineStore.setPinnedIds(timelineType(), ids)"
      @reload-head="reloadHead"
      @load-previous="timelineStore.loadPrevious(timelineType())"
      :loading="currentTimeline.isLoading"
      :has-more="currentTimeline.hasMore"
      :error="currentTimeline.error"
      :empty-text="activeTab === 'for-you' ? '还没有人发布公开帖子。' : undefined"
      @load-more="handleLoadMore"
      @retry="retryTimeline"
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

.home-new {
  position: sticky;
  top: 6.5rem;
  z-index: calc(var(--z-bar) - 1);
  display: block;
  margin: var(--space-3) auto 0;
  min-height: 2.25rem;
  padding: 0 var(--space-4);
  border-radius: var(--radius-full);
  background: var(--color-accent);
  color: #fff;
  font-size: var(--text-footnote);
  font-weight: var(--weight-semibold);
  box-shadow: var(--shadow-2);
}
</style>
