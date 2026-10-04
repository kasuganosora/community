<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import PostCard from '@/shared/components/post/PostCard.vue'

const props = defineProps<{
  statuses: MastodonStatus[]
  loading?: boolean
  hasMore?: boolean
  error?: string | null
  emptyTitle?: string
  emptyText?: string
}>()

const emit = defineEmits<{
  loadMore: []
  retry: []
  reblog: [status: MastodonStatus]
  favourite: [status: MastodonStatus]
  bookmark: [status: MastodonStatus]
}>()

const sentinel = ref<HTMLElement>()

let observer: IntersectionObserver | null = null

function loadIfVisible() {
  if (props.loading || props.error || props.hasMore === false || props.statuses.length === 0 || !sentinel.value) return
  const rect = sentinel.value.getBoundingClientRect()
  if (rect.top <= window.innerHeight + 200 && rect.bottom >= -200) emit('loadMore')
}

// A completed request can leave the sentinel in view without another intersection transition.
watch([() => props.loading, () => props.statuses.length, () => props.hasMore], loadIfVisible, { flush: 'post' })
onMounted(() => {
  observer = new IntersectionObserver(() => loadIfVisible(), { rootMargin: '200px' })
  if (sentinel.value) observer.observe(sentinel.value)
})

onUnmounted(() => {
  observer?.disconnect()
})
</script>

<template>
  <div class="timeline-feed">
    <PostCard
      v-for="status in statuses"
      :key="status.id"
      :status="status"
      @reblog="emit('reblog', $event)"
      @favourite="emit('favourite', $event)"
      @bookmark="emit('bookmark', $event)"
    />

    <div ref="sentinel" class="timeline-sentinel" />

    <div v-if="loading" class="timeline-loading">
      <t-loading />
    </div>

    <div v-if="error && statuses.length" class="timeline-inline" role="alert">
      <span>没能继续加载：{{ error }}</span>
      <button type="button" class="btn btn--secondary btn--sm" @click="emit('loadMore')">重试</button>
    </div>

    <div v-else-if="error && !loading" class="empty-state" role="alert">
      <div class="empty-state__icon">
        <t-icon name="error-circle" :size="48" />
      </div>
      <h2 class="empty-state__title">加载失败</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="emit('retry')">重试</button>
    </div>

    <div v-else-if="!loading && statuses.length === 0" class="empty-state">
      <div class="empty-state__icon">
        <t-icon name="chat" :size="48" />
      </div>
      <h2 class="empty-state__title">{{ emptyTitle ?? '这里还没有内容' }}</h2>
      <p class="empty-state__text">{{ emptyText ?? '关注其他用户后，他们的帖子会出现在这里。' }}</p>
    </div>

    <div v-else-if="!loading && hasMore && statuses.length" class="timeline-inline">
      <button type="button" class="btn btn--secondary btn--sm" @click="emit('loadMore')">加载更多</button>
    </div>
  </div>
</template>

<style scoped>
.timeline-feed {
  min-height: 12.5rem;
}

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
