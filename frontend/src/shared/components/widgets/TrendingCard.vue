<script setup lang="ts">
import { computed, onMounted } from 'vue'
import type { MastodonTag } from '@/shared/types/mastodon'
import { formatCount } from '@/shared/utils/format'
import { useCachedRequest } from '@/shared/composables/useCachedRequest'

const { data: trends, execute } = useCachedRequest<MastodonTag[]>('/api/v1/trends/tags', {
  params: { limit: 5 },
  ttl: 5 * 60 * 1000, // Cache for 5 minutes
})

// Execute on mount — catch to prevent unhandled promise rejection
onMounted(() => {
  execute().catch(() => {})
})

// Use computed to cache results instead of calling function in template
const trendStats = computed(() => {
  const stats: Record<string, string> = {}
  if (!trends.value) return stats
  
  for (const tag of trends.value) {
    const first = tag.history?.[0]
    if (first) {
      stats[tag.name] = formatCount(parseInt(first.uses, 10)) + ' posts'
    }
  }
  return stats
})
</script>

<template>
  <div class="trending-card" v-if="trends?.length">
    <h2 class="trending-card__title">What's happening</h2>
    <router-link
      v-for="tag in trends"
      :key="tag.name"
      :to="`/explore?q=${encodeURIComponent('#' + tag.name)}`"
      class="trending-item"
      role="button"
    >
      <div class="trending-item__category">Trending</div>
      <div class="trending-item__name">#{{ tag.name }}</div>
      <div class="trending-item__count" v-if="trendStats[tag.name]">
        {{ trendStats[tag.name] }}
      </div>
    </router-link>
    <div class="trending-card__more">
      <router-link to="/explore">Show more</router-link>
    </div>
  </div>
</template>

<style scoped>
.trending-card {
  background: var(--bg-secondary);
  border-radius: var(--radius-lg);
  overflow: hidden;
}

.trending-card__title {
  font-size: var(--font-xl);
  font-weight: 800;
  padding: var(--space-3) var(--space-4);
  color: var(--text-primary);
}

.trending-item {
  display: block;
  padding: var(--space-3) var(--space-4);
  cursor: pointer;
  transition: background-color var(--transition-fast);
  text-decoration: none;
}

.trending-item:hover {
  background: var(--bg-hover);
}

.trending-item__category {
  font-size: var(--font-xs);
  color: var(--text-secondary);
}

.trending-item__name {
  font-weight: 700;
  font-size: var(--font-md);
  color: var(--text-primary);
  margin: var(--space-1) 0;
}

.trending-item__count {
  font-size: var(--font-xs);
  color: var(--text-secondary);
}

.trending-card__more {
  padding: var(--space-4);
}

.trending-card__more a {
  color: var(--accent-blue);
  font-size: var(--font-md);
}
</style>
