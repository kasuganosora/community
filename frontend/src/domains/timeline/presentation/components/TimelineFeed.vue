<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import PostCard from '@/shared/components/post/PostCard.vue'
import TimelineRow from './TimelineRow.vue'
import { useTimelineWindow } from '../composables/useTimelineWindow'
import type { DiscoveryReason } from '@/domains/discovery/infrastructure/discoveryApi'

const props = withDefaults(defineProps<{
  statuses: MastodonStatus[]
  scopeKey?: string
  hasPrevious?: boolean
  loading?: boolean
  hasMore?: boolean
  error?: string | null
  emptyTitle?: string
  emptyText?: string
  recommendation?: boolean
  recommendationReasons?: Record<string, DiscoveryReason>
  autoLoad?: boolean
}>(), { autoLoad: true })

const emit = defineEmits<{
  loadMore: []
  loadPrevious: []
  reloadHead: []
  pinnedIds: [ids: string[]]
  retry: []
  reblog: [status: MastodonStatus]
  favourite: [status: MastodonStatus]
  bookmark: [status: MastodonStatus]
  recommendationFeedback: [status: MastodonStatus, action: 'hide_status' | 'hide_author']
}>()

const sentinel = ref<HTMLElement>()
const region = ref<HTMLElement>()
const windowed = useTimelineWindow(
  () => props.statuses,
  () => props.scopeKey ?? window.location.pathname,
  () => !!props.hasPrevious,
  region,
  ids => emit('pinnedIds', ids),
)
const { visible, tail, prefix, scrollTop, renderVersion } = windowed
const distantHistory = computed(() => !!props.hasPrevious && scrollTop.value < prefix.value - 6000)
// Re-read an evicted page using its API cursor, never a pixel-derived ID.
watch([scrollTop, () => props.loading, () => props.hasPrevious], () => {
  if (props.hasPrevious && !distantHistory.value && !props.loading && !props.error && scrollTop.value <= prefix.value + 600) emit('loadPrevious')
}, { flush: 'post' })

let observer: IntersectionObserver | null = null
// Start the next page well before the end so scrolling does not stall; the
// "加载更多" button stays as a fallback (no IntersectionObserver, errors).
const PREFETCH_PX = 1200

function loadIfVisible() {
  if (props.autoLoad === false || props.loading || props.error || props.hasMore === false || props.statuses.length === 0 || !sentinel.value) return
  const rect = sentinel.value.getBoundingClientRect()
  if (rect.top <= window.innerHeight + PREFETCH_PX && rect.bottom >= -PREFETCH_PX) emit('loadMore')
}

// A completed request can leave the sentinel in view without another intersection transition.
watch([() => props.loading, () => props.statuses.length, () => props.hasMore], loadIfVisible, { flush: 'post' })
onMounted(() => {
  if (typeof IntersectionObserver === 'undefined') return
  observer = new IntersectionObserver(() => loadIfVisible(), { rootMargin: `${PREFETCH_PX}px 0px` })
  if (sentinel.value) observer.observe(sentinel.value)
})

onUnmounted(() => {
  observer?.disconnect()
})
</script>

<template>
  <div class="timeline-feed">
    <button v-if="hasPrevious" type="button" class="timeline-previous btn btn--secondary btn--sm" :disabled="loading" @click="emit('loadPrevious')">{{ $t('timeline.TimelineFeed.s1') }}</button>
    <div ref="region" class="timeline-window" role="feed" :aria-label="$t('timeline.TimelineFeed.s2')" :aria-busy="loading">
      <div v-if="distantHistory" class="timeline-released" role="status">
        <div><p>{{ $t('timeline.TimelineFeed.s3') }}</p><button type="button" class="btn btn--secondary btn--sm" :disabled="loading" @click="emit('reloadHead')">{{ $t('timeline.TimelineFeed.s4') }}</button></div>
      </div>
      <template v-for="row in visible" :key="`${renderVersion}:${row.status.id}`">
        <div v-if="row.gap" :style="{ height: `${row.gap}px` }" aria-hidden="true" />
        <TimelineRow :id="row.status.id" :ref="element => windowed.bind(row.status.id, element)" :flags="windowed.rowFlags(row.status.id)" :request-pin="windowed.pin">
          <PostCard
            :status="row.status"
            :recommendation="recommendation"
            :recommendation-reason="recommendationReasons?.[row.status.id]"
            :aria-posinset="hasPrevious ? undefined : row.index + 1"
            :aria-setsize="hasMore || hasPrevious ? -1 : statuses.length"
            @reblog="emit('reblog', $event)"
            @favourite="emit('favourite', $event)"
            @bookmark="emit('bookmark', $event)"
            @recommendation-feedback="(status, action) => emit('recommendationFeedback', status, action)"
          />
        </TimelineRow>
      </template>
      <div v-if="tail" :style="{ height: `${tail}px` }" aria-hidden="true" />
    </div>

    <div ref="sentinel" class="timeline-sentinel" />

    <div v-if="loading" class="timeline-loading">
      <t-loading />
    </div>

    <div v-if="error && statuses.length" class="timeline-inline" role="alert">
      <span>{{ $t('timeline.TimelineFeed.s5', [error]) }}</span>
      <button type="button" class="btn btn--secondary btn--sm" :disabled="loading" @click="emit('retry')">{{ $t('timeline.TimelineFeed.s6') }}</button>
    </div>

    <div v-else-if="error && !loading" class="empty-state" role="alert">
      <div class="empty-state__icon">
        <t-icon name="error-circle" :size="48" />
      </div>
      <h2 class="empty-state__title">{{ $t('timeline.TimelineFeed.s7') }}</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="emit('retry')">{{ $t('timeline.TimelineFeed.s6') }}</button>
    </div>

    <div v-else-if="!loading && statuses.length === 0" class="empty-state">
      <div class="empty-state__icon">
        <t-icon name="chat" :size="48" />
      </div>
      <h2 class="empty-state__title">{{ emptyTitle ?? $t('timeline.TimelineFeed.s8') }}</h2>
      <p class="empty-state__text">{{ emptyText ?? $t('timeline.TimelineFeed.s9') }}</p>
    </div>

    <div v-else-if="!loading && hasMore && statuses.length" class="timeline-inline">
      <button type="button" class="btn btn--secondary btn--sm" @click="emit('loadMore')">{{ $t('timeline.TimelineFeed.s10') }}</button>
    </div>
  </div>
</template>

<style scoped>
.timeline-feed {
  min-height: 12.5rem;
}

.timeline-window {
  /* ResizeObserver compensates once, including late media/CW changes. */
  overflow-anchor: none;
}

.timeline-released { position: sticky; top: 5rem; height: 0; z-index: 1; }
.timeline-released > div { margin: 0 var(--gutter); padding: var(--space-4); background: var(--color-bg); border: 1px solid var(--color-separator); border-radius: var(--radius-lg); text-align: center; }
.timeline-released p { margin-bottom: var(--space-3); color: var(--color-label-secondary); }

.timeline-previous { margin: var(--space-3) var(--gutter); }

.timeline-sentinel {
  height: 1px;
}

.timeline-loading {
  display: flex;
  justify-content: center;
  padding: var(--space-8);
}

.timeline-inline {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding: var(--space-4) var(--gutter);
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
}
</style>
