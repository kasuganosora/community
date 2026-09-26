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
      const uses = parseInt(first.uses, 10)
      // Guard against NaN: invalid/empty history.uses would otherwise render
      // "NaN posts" via formatCount(NaN) -> NaN.toString() === 'NaN'
      if (!isNaN(uses)) {
        stats[tag.name] = formatCount(uses) + ' 条帖子'
      }
    }
  }
  return stats
})
</script>

<template>
  <div class="trending-card" v-if="trends?.length">
    <h2 class="trending-card__title">热门话题</h2>
    <router-link
      v-for="tag in trends"
      :key="tag.name"
      :to="`/tags/${encodeURIComponent(tag.name.replace(/^#/, ''))}`"
      class="trending-item"
    >
      <div class="trending-item__category">趋势</div>
      <div class="trending-item__name">#{{ tag.name }}</div>
      <div class="trending-item__count" v-if="trendStats[tag.name]">
        {{ trendStats[tag.name] }}
      </div>
    </router-link>
    <div class="trending-card__more">
      <router-link to="/explore">发现更多</router-link>
    </div>
  </div>
</template>

<style scoped>
/* Inset grouped card: one soft fill, no border, no shadow. */
.trending-card {
  background: var(--color-bg-grouped);
  border-radius: var(--radius-lg);
  overflow: hidden;
}

.trending-card__title {
  padding: var(--space-3) var(--space-4) var(--space-1);
  font-size: var(--text-title-3);
  line-height: var(--leading-title-3);
  letter-spacing: var(--tracking-title-3);
  font-weight: var(--weight-bold);
  color: var(--color-label);
}

.trending-card__more {
  padding: var(--space-1) var(--space-4) var(--space-3);
}

.trending-card__more a {
  display: inline-flex;
  align-items: center;
  min-height: var(--hit-target);
  color: var(--color-accent-text);
  font-size: var(--text-subhead);
  font-weight: var(--weight-medium);
}

.trending-item {
  display: block;
  padding: var(--space-2) var(--space-4);
  color: inherit;
  text-decoration: none;
  -webkit-tap-highlight-color: transparent;
  transition: background-color var(--duration-release) var(--ease-out);
}

@media (hover: hover) and (pointer: fine) {
  .trending-item:hover {
    background: var(--color-hover);
    text-decoration: none;
  }
}

.trending-item:active {
  background: var(--color-pressed);
  transition-duration: 0s;
}

.trending-item__category,
.trending-item__count {
  font-size: var(--text-footnote);
  letter-spacing: var(--tracking-footnote);
  color: var(--color-label-secondary);
}

.trending-item__name {
  margin: var(--space-0) 0;
  font-size: var(--text-body);
  font-weight: var(--weight-semibold);
  color: var(--color-label);
}
</style>
