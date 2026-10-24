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
import { defineAsyncComponent } from 'vue'
const ExploreGroups = defineAsyncComponent(() => import('@/domains/group/presentation/components/ExploreGroups.vue'))

import { t } from '@/shared/i18n'
type ExploreTab = 'local' | 'federated' | 'groups'
type FeedTab = Exclude<ExploreTab, 'groups'>
const TABS: { value: ExploreTab; label: string }[] = [
  { value: 'local', label: t('timeline.ExplorePage.s10') },
  { value: 'federated', label: t('timeline.ExplorePage.s11') },
  { value: 'groups', label: t('timeline.ExplorePage.s12') },
]

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const followedTags = ref<TagInfo[]>([])
const initialTab: ExploreTab = route.query.tab === 'federated' || route.query.tab === 'groups' ? route.query.tab : 'local'
const activeTab = ref<ExploreTab>(initialTab)
// The post feed keeps the last post tab while the groups tab is shown.
const feedTab = ref<FeedTab>(initialTab === 'groups' ? 'local' : initialTab)
watch(activeTab, tab => {
  if (tab === 'groups') return
  if (feedTab.value !== tab) feedTab.value = tab
  else if (!statuses.value.length && !loading.value) void feed.refresh() // opened directly on the groups tab
})
const { policy: visitorPolicy } = useVisitorPolicy()
const visitor = computed(() => !authStore.currentUser)
const visitorBlocked = computed(() => visitor.value && visitorPolicy.value === 'none')
// Visitors limited to local content still see local groups (the server filters them).
const tabs = computed(() => visitor.value && visitorPolicy.value === 'local' ? TABS.filter(t => t.value !== 'federated') : TABS)
watch(tabs, t => { if (!t.some(x => x.value === activeTab.value)) activeTab.value = 'local' })

const feed = usePaginatedList((maxId, limit) => timelineApi.getPublicTimeline({
  max_id: maxId,
  limit,
  ...(feedTab.value === 'local' ? { local: true } : {}),
}), TIMELINE_PAGE_SIZE, 8, { prefetchNewer: true })
const { items: statuses, loading, error, hasMore, newer, hasPrevious } = feed
const streamTarget = computed(() => feedTab.value === 'local' ? 'stream=public:local' : 'stream=public')
useFeedStream(streamTarget, feed.revalidate)
async function reloadHead() { await feed.refresh(); if (!error.value) window.scrollTo(0, 0) }
function revealNewerPosts() {
  const pending = feed.revealNewer()
  const scroll = () => { if (!error.value) window.scrollTo(0, 0) }
  if (pending) return pending.then(scroll)
  scroll()
}

const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

function selectTab(tab: ExploreTab) {
  if (tab === activeTab.value) return
  if (activeTab.value !== 'groups') saveListSnapshot(`explore:${activeTab.value}`, statuses.value, hasMore.value, feed.windowState())
  activeTab.value = tab
  router.replace({ query: { ...route.query, tab } }).catch(() => {})
}

watch(feedTab, () => {
  const snap = readListSnapshot<typeof statuses.value[number]>(`explore:${feedTab.value}`)
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
  if (activeTab.value === 'groups') return
  const snap = readListSnapshot<typeof statuses.value[number]>(`explore:${feedTab.value}`)
  if (snap?.items.length) {
    feed.hydrate(snap.items, snap.hasMore, snap.window)
    nextTick(() => restoreListScroll(snap.scroll))
    return
  }
  feed.refresh()
})

onBeforeRouteLeave(() => { if (activeTab.value !== 'groups') saveListSnapshot(`explore:${activeTab.value}`, statuses.value, hasMore.value, feed.windowState()) })

function retryFeed() {
  if (statuses.value.length && hasMore.value) void feed.loadMore()
  else void feed.refresh()
}
</script>

<template>
  <div class="explore-page">
    <div class="page-header">
      <h1 class="page-header__title">{{ $t('timeline.ExplorePage.s1') }}</h1>
    </div>
    <!-- The right rail (with its search field) is hidden on narrow screens,
         so Explore carries its own search entry there. -->
    <div class="explore-search">
      <SearchBox live />
    </div>
    <div v-if="followedTags.length" class="explore-followed">
      <span>{{ $t('timeline.ExplorePage.s2') }}</span>
      <router-link v-for="item in followedTags" :key="item.name" :to="`/tags/${encodeURIComponent(item.name)}`">#{{ item.name }}</router-link>
    </div>
    <p v-if="visitorBlocked" class="explore-visitor-note" role="status">{{ $t('timeline.ExplorePage.s3') }}<router-link to="/login">{{ $t('timeline.ExplorePage.s4') }}</router-link>{{ $t('timeline.ExplorePage.s5') }}</p>
    <p v-else-if="visitor && visitorPolicy === 'local'" class="explore-visitor-note">{{ $t('timeline.ExplorePage.s6') }}<router-link to="/login">{{ $t('timeline.ExplorePage.s4') }}</router-link>{{ $t('timeline.ExplorePage.s7') }}</p>
    <template v-if="!visitorBlocked">
    <TabBar
      :model-value="activeTab"
      :options="tabs"
      :label="$t('timeline.ExplorePage.s8')"
      @update:model-value="selectTab"
    />
    <ExploreGroups v-if="activeTab === 'groups'" />
    <template v-else>
    <NewPostsPill :show="newer.length > 0" :reveal="revealNewerPosts" />
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
      :empty-text="$t('timeline.ExplorePage.s9')"
      @load-more="feed.loadMore"
      @retry="retryFeed"
      @reblog="toggleReblog"
      @favourite="toggleFavourite"
      @bookmark="toggleBookmark"
    />
    </template>
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
