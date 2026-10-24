<script setup lang="ts">
import NewPostsPill from '@/shared/components/common/NewPostsPill.vue'
import { computed, onMounted, onBeforeUnmount, watch, ref, nextTick } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { MessagePlugin } from '@/shared/ui/feedback'
import { useFeedStream } from '@/shared/composables/useFeedStream'
import { useTimelineStore } from '../stores/timelineStore'
import type { MastodonStatus } from '@/shared/types/mastodon'
import type { ManagedTimelineType } from '../../domain/entities'
import { useStatusActions } from '@/domains/post/presentation/composables/useStatusActions'
import { useDiscoveryFeed } from '@/domains/discovery/presentation/composables/useDiscoveryFeed'
import { discoveryPreference, discoveryInvalidation, loadDiscoveryPreference } from '@/domains/discovery/presentation/composables/discoveryState'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { readListSnapshot, saveListSnapshot, restoreListScroll } from '@/shared/utils/listMemory'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import TimelineFeed from '../components/TimelineFeed.vue'
import TabBar from '@/shared/components/common/TabBar.vue'

import { t } from '@/shared/i18n'
const timelineStore = useTimelineStore()
const discovery = useDiscoveryFeed()
const checkingPreference = ref(false)
type TabType = 'for-you' | 'following' | 'global'
const tabs = computed<{ value: TabType; label: string }[]>(() => [
  { value: 'following', label: t('timeline.HomePage.s13') },
  { value: 'for-you', label: t('timeline.HomePage.s14') },
  { value: 'global', label: t('timeline.HomePage.s15') },
])
const activeTab = computed({ get: () => timelineStore.homeTab, set: (tab: TabType) => { timelineStore.homeTab = tab } })
const isDiscovery = computed(() => activeTab.value === 'for-you')
const chronologicalType = computed<ManagedTimelineType>(() => activeTab.value === 'global' ? 'public' : 'home')
const chronologicalTimeline = computed(() => chronologicalType.value === 'public' ? timelineStore.publicTimeline : timelineStore.homeTimeline)
const statuses = computed(() => isDiscovery.value ? discovery.statuses.value : chronologicalType.value === 'public' ? timelineStore.publicStatuses : timelineStore.homeStatuses)
const loading = computed(() => isDiscovery.value ? checkingPreference.value || discovery.loading.value : chronologicalTimeline.value.isLoading)
const error = computed(() => isDiscovery.value ? discovery.error.value : chronologicalTimeline.value.error)
const hasMore = computed(() => isDiscovery.value ? discovery.hasMore.value : chronologicalTimeline.value.hasMore)
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions((id, updates) => {
  timelineStore.updateStatus(id, updates)
  discovery.updateStatus(id, updates)
})
let opening = 0
let discoveryScroll: { generation: string; scroll: number } | undefined
function rememberTimeline() {
  if (isDiscovery.value) discoveryScroll = { generation: discovery.generation.value, scroll: window.scrollY }
  else saveListSnapshot(`home:${activeTab.value}`, [], chronologicalTimeline.value.hasMore)
}
function fetchChronological() {
  return chronologicalType.value === 'public' ? timelineStore.fetchPublicTimeline() : timelineStore.fetchHomeTimeline()
}
async function openChronological() {
  const type = chronologicalType.value
  if (!chronologicalTimeline.value.statuses.length) { await fetchChronological(); return }
  const snapshot = readListSnapshot(`home:${activeTab.value}`)
  if (snapshot) await nextTick(() => restoreListScroll(snapshot.scroll))
  await timelineStore.checkNewer(type)
}
async function openTimeline() {
  const mine = ++opening, epoch = sessionEpoch.value
  if (!isDiscovery.value) { await openChronological(); return }
  // Returning to a recent recommendation window reuses it (and its scroll
  // position) instead of re-requesting and re-rendering the whole feed.
  if (discovery.isFresh()) {
    // Cheap revision check so feedback made elsewhere applies on return.
    const current = await loadDiscoveryPreference(true).catch(() => null)
    if (mine !== opening || epoch !== sessionEpoch.value) return
    if (current && discovery.isFresh(undefined, current.revision)) {
      const snapshot = discoveryScroll
      await nextTick()
      if (mine === opening && epoch === sessionEpoch.value && isDiscovery.value && snapshot?.generation === discovery.generation.value) {
        window.scrollTo(0, snapshot.scroll)
      }
      return
    }
  }
  discoveryScroll = undefined
  discovery.clear()
  checkingPreference.value = true
  try {
    const preference = await loadDiscoveryPreference()
    if (mine !== opening || epoch !== sessionEpoch.value || !isDiscovery.value || !preference) return
    if (preference.preference === 'off' || preference.enabled === false) {
      activeTab.value = 'following'
      await openChronological()
    } else await discovery.refresh()
  } catch (e) {
    if (mine === opening && epoch === sessionEpoch.value) discovery.error.value = e instanceof Error ? e.message : t('timeline.HomePage.s16')
  } finally { if (mine === opening) checkingPreference.value = false }
}
function onTabChange(value: TabType) {
  if (value === activeTab.value) return
  rememberTimeline()
  ++opening
  checkingPreference.value = false
  timelineStore.cancelRevalidation()
  discovery.cancel()
  activeTab.value = value
  void openTimeline()
}
function revealNewer() {
  window.scrollTo({ top: 0, behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' })
  return timelineStore.revealNewer(chronologicalType.value)
}
async function reloadHead() {
  if (isDiscovery.value) { discovery.clear(); await openTimeline() }
  else await fetchChronological()
  if (!error.value) window.scrollTo(0, 0)
}
function loadMore() { return isDiscovery.value ? discovery.continueLoading() : chronologicalType.value === 'public' ? timelineStore.loadMorePublic() : timelineStore.loadMoreHome() }
function retry() {
  if (!isDiscovery.value) return timelineStore.retryTimeline(chronologicalType.value)
  return statuses.value.length && hasMore.value ? loadMore() : openTimeline()
}
async function feedback(status: MastodonStatus, action: 'hide_status' | 'hide_author') {
  try {
    await discovery.feedback(status, action)
    MessagePlugin.success(t('timeline.HomePage.s17'))
  } catch (e) { MessagePlugin.error(e instanceof Error ? e.message : t('timeline.HomePage.s18')) }
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
// Ranked feeds only change when explicitly refreshed. Each chronological
// timeline subscribes to its own invalidations and retains its own cursors.
useFeedStream(computed(() => chronologicalType.value === 'public' ? 'stream=public' : 'stream=user'), async () => {
  if (!isDiscovery.value) await timelineStore.checkNewer(chronologicalType.value)
})
// Only rebuild after the window went stale while hidden, not on every app/tab switch.
function resume() {
  if (!document.hidden && isDiscovery.value && !checkingPreference.value && !discovery.loading.value && !discovery.isFresh()) void openTimeline()
}
onMounted(() => { void openTimeline(); document.addEventListener('visibilitychange', resume) })
onBeforeRouteLeave(rememberTimeline)
onBeforeUnmount(() => { ++opening; timelineStore.cancelRevalidation(); document.removeEventListener('visibilitychange', resume) })
</script>

<template>
  <div class="home-page">
    <div class="home-header">
      <h1 class="page-header__title">{{ $t('timeline.HomePage.s1') }}</h1>
      <TabBar :model-value="activeTab" :options="tabs" :label="$t('timeline.HomePage.s2')" @update:model-value="onTabChange" />
    </div>
    <PostComposer @posted="handlePosted" />
    <div v-if="isDiscovery && discovery.mode.value === 'chronological_fallback'" class="discovery-note" role="status">
      {{ $t('timeline.HomePage.s3') }}
      <button type="button" @click="onTabChange('global')">{{ $t('timeline.HomePage.s4') }}</button>
    </div>
    <NewPostsPill :show="!isDiscovery && chronologicalTimeline.newer.length > 0" top="6.5rem" :reveal="revealNewer" />
    <TimelineFeed
      :statuses="statuses" :scope-key="isDiscovery ? `discovery:${sessionEpoch}:${discovery.generation.value}` : chronologicalType"
      :has-previous="!isDiscovery && chronologicalTimeline.hasPrevious"
      :loading="loading" :has-more="hasMore" :error="error"
      :auto-load="!isDiscovery || !discovery.autoPaused.value"
      :recommendation="isDiscovery" :recommendation-reasons="isDiscovery ? discovery.reasons.value : undefined"
      :empty-text="isDiscovery ? $t('timeline.HomePage.s5') : activeTab === 'global' ? $t('timeline.HomePage.s6') : undefined"
      @pinned-ids="ids => { if (!isDiscovery) timelineStore.setPinnedIds(chronologicalType, ids) }"
      @reload-head="reloadHead" @load-previous="timelineStore.loadPrevious(chronologicalType)"
      @load-more="loadMore" @retry="retry" @recommendation-feedback="feedback"
      @reblog="toggleReblog" @favourite="toggleFavourite" @bookmark="toggleBookmark"
    />
    <div v-if="isDiscovery && discovery.resetRequired.value" class="discovery-note" role="status">
      {{ $t('timeline.HomePage.s7') }}
      <button type="button" class="btn btn--secondary btn--sm" @click="reloadHead">{{ $t('timeline.HomePage.s8') }}</button>
    </div>
    <div v-else-if="isDiscovery && discovery.autoPaused.value" class="discovery-note" role="status">
      {{ $t('timeline.HomePage.s9') }}
      <button type="button" class="btn btn--secondary btn--sm" @click="discovery.continueLoading">{{ $t('timeline.HomePage.s10') }}</button>
    </div>
    <div v-else-if="isDiscovery && !loading && !hasMore && statuses.length && discovery.mode.value !== 'chronological_fallback'" class="discovery-note" role="status">
      {{ $t('timeline.HomePage.s11') }}
      <button type="button" @click="reloadHead">{{ $t('timeline.HomePage.s12') }}</button>
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

.discovery-note { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); padding: var(--space-3) var(--gutter); color: var(--color-label-secondary); font-size: var(--text-footnote); border-bottom: 1px solid var(--color-separator); }
.discovery-note a, .discovery-note button { display: inline-flex; align-items: center; color: var(--color-accent-text); min-height: var(--hit-target); }
.discovery-note button:active:not(:disabled):not(:focus-visible) { opacity: .7; }
</style>
