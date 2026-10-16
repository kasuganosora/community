<script setup lang="ts">
import NewPostsPill from '@/shared/components/common/NewPostsPill.vue'
import { ref, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { timelineApi } from '../../infrastructure/timelineApi'
import { tagApi, type TagInfo } from '../../infrastructure/tagApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { useFeedStream } from '@/shared/composables/useFeedStream'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'
import { readListSnapshot, restoreListScroll, saveListSnapshot } from '@/shared/utils/listMemory'
import TimelineFeed from '../components/TimelineFeed.vue'
import SearchBox from '@/shared/components/widgets/SearchBox.vue'
import TabBar from '@/shared/components/common/TabBar.vue'
import { useVisitorPolicy } from '@/shared/composables/useVisitorPolicy'

type ExploreTab = 'local' | 'federated'
const TABS: { value: ExploreTab; label: string }[] = [
  { value: 'local', label: '本站' },
  { value: 'federated', label: '跨站' },
]

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const followedTags = ref<TagInfo[]>([])
const activeTab = ref<ExploreTab>(route.query.tab === 'federated' ? 'federated' : 'local')
const { policy: visitorPolicy } = useVisitorPolicy()
const visitor = computed(() => !authStore.currentUser)
const visitorBlocked = computed(() => visitor.value && visitorPolicy.value === 'none')
const tabs = computed(() => visitor.value && visitorPolicy.value === 'local' ? TABS.filter(t => t.value === 'local') : TABS)
watch(tabs, t => { if (!t.some(x => x.value === activeTab.value)) activeTab.value = 'local' })

const feed = usePaginatedList((maxId, limit) => timelineApi.getPublicTimeline({
  max_id: maxId,
  limit,
  ...(activeTab.value === 'local' ? { local: true } : {}),
}), TIMELINE_PAGE_SIZE, 8)
const { items: statuses, loading, error, hasMore, newer, hasPrevious } = feed
const streamTarget = computed(() => activeTab.value === 'local' ? 'stream=public:local' : 'stream=public')
useFeedStream(streamTarget, feed.revalidate)
async function reloadHead() { await feed.refresh(); if (!error.value) window.scrollTo(0, 0) }

const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

function selectTab(tab: ExploreTab) {
  if (tab === activeTab.value) return
  saveListSnapshot(`explore:${activeTab.value}`, statuses.value, hasMore.value, feed.windowState())
  activeTab.value = tab
  router.replace({ query: { ...route.query, tab } }).catch(() => {})
}

watch(activeTab, () => {
  const snap = readListSnapshot<typeof statuses.value[number]>(`explore:${activeTab.value}`)
  if (snap?.items.length) {
    feed.hydrate(snap.items, snap.hasMore, snap.window)
    nextTick(() => restoreListScroll(snap.scroll))
    return
  }
  feed.reset()
  feed.refresh()
})

let tagVersion = 0
async function loadFollowedTags() {
  const version = ++tagVersion, epoch = sessionEpoch.value
  followedTags.value = []
  if (!authStore.currentUser) return
  try {
    const rows = await tagApi.followed()
    if (version === tagVersion && epoch === sessionEpoch.value) followedTags.value = rows
  } catch { /* Keep the current session's empty state on failure. */ }
}
watch(sessionEpoch, () => {
  ++tagVersion; followedTags.value = []
  const epoch = sessionEpoch.value
  void nextTick(() => { if (epoch === sessionEpoch.value) void loadFollowedTags() })
}, { flush: 'sync' })
onBeforeUnmount(() => { ++tagVersion })
onMounted(() => {
  void loadFollowedTags()
  const snap = readListSnapshot<typeof statuses.value[number]>(`explore:${activeTab.value}`)
  if (snap?.items.length) {
    feed.hydrate(snap.items, snap.hasMore, snap.window)
    nextTick(() => restoreListScroll(snap.scroll))
    return
  }
  feed.refresh()
})

onBeforeRouteLeave(() => saveListSnapshot(`explore:${activeTab.value}`, statuses.value, hasMore.value, feed.windowState()))

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
      <SearchBox live />
    </div>
    <div v-if="followedTags.length" class="explore-followed">
      <span>关注的话题</span>
      <router-link v-for="item in followedTags" :key="item.name" :to="`/tags/${encodeURIComponent(item.name)}`">#{{ item.name }}</router-link>
    </div>
    <p v-if="visitorBlocked" class="explore-visitor-note" role="status">本站仅向登录用户展示帖子。<router-link to="/login">登录</router-link>后即可浏览。</p>
    <p v-else-if="visitor && visitorPolicy === 'local'" class="explore-visitor-note">未登录时仅显示本站内容。<router-link to="/login">登录</router-link>后可浏览跨站帖子。</p>
    <template v-if="!visitorBlocked">
    <TabBar
      :model-value="activeTab"
      :options="tabs"
      label="时间线"
      @update:model-value="selectTab"
    />
    <NewPostsPill :show="newer.length > 0" @click="reloadHead" />
    <TimelineFeed
      :statuses="statuses"
      :scope-key="streamTarget"
      :has-previous="hasPrevious"
      @reload-head="reloadHead"
      @load-previous="feed.loadPrevious"
      @pinned-ids="feed.setPinnedIds"
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
    </template>
  </div>
</template>

<style scoped>
.explore-visitor-note { margin: 8px 16px; padding: 10px 12px; border-radius: 10px; background: var(--color-fill); color: var(--color-label-secondary); font-size: 13px; line-height: 1.6; }
.explore-page {
  min-height: 100dvh;
}

.explore-search {
  padding: 0 var(--space-4);
}

.explore-followed {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  align-items: center;
  padding: var(--space-2) var(--space-4) 0;
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
}
.explore-followed a { color: var(--color-accent-text); }

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
