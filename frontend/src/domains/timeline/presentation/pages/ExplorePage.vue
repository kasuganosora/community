<script setup lang="ts">
import { ref, watch, onMounted, nextTick } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { timelineApi } from '../../infrastructure/timelineApi'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'
import { readListSnapshot, restoreListScroll, saveListSnapshot } from '@/shared/utils/listMemory'
import TimelineFeed from '../components/TimelineFeed.vue'
import SearchBox from '@/shared/components/widgets/SearchBox.vue'
import TabBar from '@/shared/components/common/TabBar.vue'

type ExploreTab = 'local' | 'federated'
const TABS: { value: ExploreTab; label: string }[] = [
  { value: 'local', label: '本站' },
  { value: 'federated', label: '跨站' },
]

const route = useRoute()
const router = useRouter()
const activeTab = ref<ExploreTab>(route.query.tab === 'federated' ? 'federated' : 'local')

const feed = usePaginatedList((maxId, limit) => timelineApi.getPublicTimeline({
  max_id: maxId,
  limit,
  ...(activeTab.value === 'local' ? { local: true } : {}),
}), TIMELINE_PAGE_SIZE)
const { items: statuses, loading, error, hasMore } = feed

const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

function selectTab(tab: ExploreTab) {
  if (tab === activeTab.value) return
  saveListSnapshot(`explore:${activeTab.value}`, statuses.value, hasMore.value)
  activeTab.value = tab
  router.replace({ query: { ...route.query, tab } }).catch(() => {})
}

watch(activeTab, () => {
  const snap = readListSnapshot<typeof statuses.value[number]>(`explore:${activeTab.value}`)
  if (snap?.items.length) {
    feed.hydrate(snap.items, snap.hasMore)
    nextTick(() => restoreListScroll(snap.scroll))
    return
  }
  feed.reset()
  feed.refresh()
})

onMounted(() => {
  const snap = readListSnapshot<typeof statuses.value[number]>(`explore:${activeTab.value}`)
  if (snap?.items.length) {
    feed.hydrate(snap.items, snap.hasMore)
    nextTick(() => restoreListScroll(snap.scroll))
    return
  }
  feed.refresh()
})

onBeforeRouteLeave(() => saveListSnapshot(`explore:${activeTab.value}`, statuses.value, hasMore.value))

function retryFeed() {
  if (statuses.value.length && hasMore.value) void feed.loadMore()
  else void feed.refresh()
}
</script>

<template>
  <div class="explore-page">
    <div class="page-header">
      <h1 class="page-header__title">探索</h1>
    </div>
    <!-- The right rail (with its search field) is hidden on narrow screens,
         so Explore carries its own search entry there. -->
    <div class="explore-search">
      <SearchBox />
    </div>
    <TabBar
      :model-value="activeTab"
      :options="TABS"
      label="时间线"
      @update:model-value="selectTab"
    />
    <TimelineFeed
      :statuses="statuses"
      :loading="loading"
      :has-more="hasMore"
      :error="error"
      empty-text="还没有公开帖子。"
      @load-more="feed.loadMore"
      @retry="retryFeed"
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

.explore-search {
  padding: 0 var(--space-4);
}

/* The page header is already the sticky bar here. */
.explore-search :deep(.search-box) {
  position: static;
}

/* The right rail shows its own search field from 62.5rem up. */
@media (min-width: 62.5rem) {
  .explore-search {
    display: none;
  }
}
</style>
