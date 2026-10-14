<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, watch, ref, nextTick } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useFeedStream } from '@/shared/composables/useFeedStream'
import { useTimelineStore } from '../stores/timelineStore'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { useStatusActions } from '@/domains/post/presentation/composables/useStatusActions'
import { useDiscoveryFeed } from '@/domains/discovery/presentation/composables/useDiscoveryFeed'
import { discoveryPreference, discoveryInvalidation, loadDiscoveryPreference } from '@/domains/discovery/presentation/composables/discoveryState'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { readListSnapshot, saveListSnapshot, restoreListScroll } from '@/shared/utils/listMemory'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import TimelineFeed from '../components/TimelineFeed.vue'
import TabBar from '@/shared/components/common/TabBar.vue'

const timelineStore = useTimelineStore()
const discovery = useDiscoveryFeed()
const checkingPreference = ref(false)
type TabType = 'for-you' | 'following'
const tabs = computed<{ value: TabType; label: string }[]>(() => [
  { value: 'for-you', label: discoveryPreference.value?.preference === 'personalized' ? '推荐' : '公开发现' },
  { value: 'following', label: '关注' },
])
const activeTab = computed({ get: () => timelineStore.homeTab, set: (tab: TabType) => { timelineStore.homeTab = tab } })
const isDiscovery = computed(() => activeTab.value === 'for-you')
const statuses = computed(() => isDiscovery.value ? discovery.statuses.value : timelineStore.homeStatuses)
const loading = computed(() => isDiscovery.value ? checkingPreference.value || discovery.loading.value : timelineStore.homeTimeline.isLoading)
const error = computed(() => isDiscovery.value ? discovery.error.value : timelineStore.homeTimeline.error)
const hasMore = computed(() => isDiscovery.value ? discovery.hasMore.value : timelineStore.homeTimeline.hasMore)
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions((id, updates) => {
  timelineStore.updateStatus(id, updates)
  discovery.updateStatus(id, updates)
})
let opening = 0
function rememberFollowing() {
  if (!isDiscovery.value) saveListSnapshot('home:following', [], timelineStore.homeTimeline.hasMore)
}
async function openFollowing() {
  if (!timelineStore.homeStatuses.length) { await timelineStore.fetchHomeTimeline(); return }
  const snapshot = readListSnapshot('home:following')
  if (snapshot) await nextTick(() => restoreListScroll(snapshot.scroll))
  await timelineStore.checkNewer('home')
}
async function openTimeline() {
  const mine = ++opening, epoch = sessionEpoch.value
  if (!isDiscovery.value) { await openFollowing(); return }
  checkingPreference.value = true
  try {
    const preference = await loadDiscoveryPreference()
    if (mine !== opening || epoch !== sessionEpoch.value || !isDiscovery.value || !preference) return
    if (preference.preference === 'off' || preference.enabled === false) {
      activeTab.value = 'following'
      await openFollowing()
    } else await discovery.refresh()
  } catch (e) {
    if (mine === opening && epoch === sessionEpoch.value) discovery.error.value = e instanceof Error ? e.message : '无法读取推荐设置，请重试'
  } finally { if (mine === opening) checkingPreference.value = false }
}
function onTabChange(value: TabType) {
  if (value === activeTab.value) return
  rememberFollowing()
  ++opening
  checkingPreference.value = false
  timelineStore.cancelRevalidation()
  discovery.clear()
  activeTab.value = value
  void openTimeline()
}
async function reloadHead() {
  if (isDiscovery.value) { discovery.clear(); await openTimeline() }
  else await timelineStore.fetchHomeTimeline()
  if (!error.value) window.scrollTo(0, 0)
}
function loadMore() { return isDiscovery.value ? discovery.continueLoading() : timelineStore.loadMoreHome() }
function retry() { return statuses.value.length && hasMore.value ? loadMore() : openTimeline() }
async function feedback(status: MastodonStatus, action: 'hide_status' | 'hide_author') {
  try {
    await discovery.feedback(status, action)
    MessagePlugin.success('已更新发现偏好，可在设置中撤销')
  } catch (e) { MessagePlugin.error(e instanceof Error ? e.message : '未能保存，帖子已保留，请重试') }
}
function handlePosted(status: MastodonStatus) { timelineStore.prependStatus(status) }
watch(() => discovery.disabled.value, disabled => {
  if (disabled && isDiscovery.value) { activeTab.value = 'following'; void timelineStore.fetchHomeTimeline() }
})
watch(sessionEpoch, () => { ++opening; void openTimeline() })
watch(discoveryInvalidation, () => {
  if (discoveryPreference.value?.preference === 'off' && isDiscovery.value) {
    activeTab.value = 'following'; void timelineStore.fetchHomeTimeline()
  }
})
// Ranked feeds only change when explicitly refreshed. SSE remains dedicated
// to the chronological following timeline; never refill from publicTimeline.
useFeedStream(computed(() => 'stream=user'), async () => {
  if (!isDiscovery.value) await timelineStore.checkNewer('home')
})
function resume() { if (!document.hidden && isDiscovery.value) { discovery.clear(); void openTimeline() } }
onMounted(() => { void openTimeline(); document.addEventListener('visibilitychange', resume) })
onBeforeRouteLeave(rememberFollowing)
onBeforeUnmount(() => { ++opening; timelineStore.cancelRevalidation(); document.removeEventListener('visibilitychange', resume) })
</script>

<template>
  <div class="home-page">
    <div class="home-header">
      <h1 class="page-header__title">首页</h1>
      <TabBar :model-value="activeTab" :options="tabs" label="时间线" @update:model-value="onTabChange" />
    </div>
    <PostComposer @posted="handlePosted" />
    <div v-if="!isDiscovery && (discoveryPreference?.preference === 'off' || discoveryPreference?.enabled === false)" class="discovery-note">
      {{ discoveryPreference?.enabled === false ? '本站尚未开放内容发现。' : '你已关闭内容发现，首页显示关注时间线。' }}
      <router-link to="/settings?section=discovery">发现设置</router-link>
    </div>
    <div v-if="isDiscovery" class="discovery-note">
      <span>{{ discoveryPreference?.preference === 'personalized' ? '根据你关注的话题和公开互动发现内容。' : '发现近期公开内容，不使用个人互动排序。' }}</span>
      <router-link to="/settings?section=discovery">发现设置</router-link>
      <button type="button" :disabled="loading" @click="reloadHead">刷新</button>
    </div>
    <div v-if="isDiscovery && discovery.mode.value === 'chronological_fallback'" class="discovery-note" role="status">
      发现服务暂时降级，本次仅显示一页近期公开帖。
      <router-link to="/explore">浏览公开时间线</router-link>
    </div>
    <button v-if="!isDiscovery && timelineStore.homeTimeline.newer.length" type="button" class="home-new" @click="timelineStore.revealNewer('home')">有新帖子，点击查看</button>
    <TimelineFeed
      :statuses="statuses" :scope-key="isDiscovery ? `discovery:${sessionEpoch}:${discovery.generation.value}` : 'home'"
      :has-previous="!isDiscovery && timelineStore.homeTimeline.hasPrevious"
      :loading="loading" :has-more="hasMore" :error="error"
      :auto-load="!isDiscovery || !discovery.autoPaused.value"
      :recommendation="isDiscovery" :recommendation-reasons="isDiscovery ? discovery.reasons.value : undefined"
      :empty-text="isDiscovery ? '暂时没有符合发现条件的内容，可以关注更多话题，或稍后刷新。' : undefined"
      @pinned-ids="ids => { if (!isDiscovery) timelineStore.setPinnedIds('home', ids) }"
      @reload-head="reloadHead" @load-previous="timelineStore.loadPrevious('home')"
      @load-more="loadMore" @retry="retry" @recommendation-feedback="feedback"
      @reblog="toggleReblog" @favourite="toggleFavourite" @bookmark="toggleBookmark"
    />
    <div v-if="isDiscovery && discovery.resetRequired.value" class="discovery-note" role="status">
      本轮推荐已失效或偏好已更新，请刷新后继续。
      <button type="button" class="btn btn--secondary btn--sm" @click="reloadHead">刷新发现</button>
    </div>
    <div v-else-if="isDiscovery && discovery.autoPaused.value" class="discovery-note" role="status">
      这一部分内容已被过滤，可以继续查看下一部分。
      <button type="button" class="btn btn--secondary btn--sm" @click="discovery.continueLoading">继续加载</button>
    </div>
    <div v-else-if="isDiscovery && !loading && !hasMore && statuses.length && discovery.mode.value !== 'chronological_fallback'" class="discovery-note" role="status">
      本轮发现已看完。
      <button type="button" @click="reloadHead">刷新新一轮</button>
    </div>
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
.discovery-note { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); padding: var(--space-3) var(--gutter); color: var(--color-label-secondary); font-size: var(--text-footnote); border-bottom: 1px solid var(--color-separator); }
.discovery-note a, .discovery-note button { display: inline-flex; align-items: center; color: var(--color-accent-text); min-height: var(--hit-target); }
.discovery-note button:active:not(:disabled):not(:focus-visible) { opacity: .7; }
</style>
