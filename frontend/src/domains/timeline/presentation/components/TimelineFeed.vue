<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import PostCard from '@/shared/components/post/PostCard.vue'

const props = defineProps<{
  statuses: MastodonStatus[]
  loading?: boolean
  hasMore?: boolean
}>()

const emit = defineEmits<{
  loadMore: []
  reblog: [status: MastodonStatus]
  favourite: [status: MastodonStatus]
  bookmark: [status: MastodonStatus]
}>()

const sentinel = ref<HTMLElement>()

let observer: IntersectionObserver | null = null
// Loading lock to prevent duplicate requests
let isLoadingMore = false

onMounted(() => {
  observer = new IntersectionObserver(
    (entries) => {
      if (entries[0]?.isIntersecting && !props.loading && !isLoadingMore && props.hasMore !== false) {
        isLoadingMore = true
        emit('loadMore')
        // Reset lock after a delay
        setTimeout(() => { isLoadingMore = false }, 500)
      }
    },
    { rootMargin: '200px' },
  )
  if (sentinel.value) {
    observer.observe(sentinel.value)
  }
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

    <div v-if="!loading && statuses.length === 0" class="empty-state">
      <div class="empty-state__icon">
        <t-icon name="chat" :size="48" />
      </div>
      <h2 class="empty-state__title">Nothing to see here yet</h2>
      <p class="empty-state__text">When you follow someone, their posts will show up here.</p>
    </div>
  </div>
</template>

<style scoped>
.timeline-feed {
  min-height: 200px;
}

.timeline-sentinel {
  height: 1px;
}

.timeline-loading {
  display: flex;
  justify-content: center;
  padding: var(--space-8);
}

</style>
