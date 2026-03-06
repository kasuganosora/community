<script setup lang="ts">
import { ref, onMounted } from 'vue'
import type { MastodonTag } from '@/shared/types/mastodon'
import client from '@/shared/api/client'
import { formatCount } from '@/shared/utils/format'

const trends = ref<MastodonTag[]>([])

onMounted(async () => {
  try {
    const { data } = await client.get<MastodonTag[]>('/api/v1/trends/tags', { params: { limit: 5 } })
    trends.value = data
  } catch {
    // Silently fail - card will be hidden if empty
  }
})

function getTagPosts(tag: MastodonTag): string {
  const first = tag.history?.[0]
  if (first) {
    return formatCount(parseInt(first.uses)) + ' posts'
  }
  return ''
}
</script>

<template>
  <div class="trending-card" v-if="trends.length">
    <h2 class="trending-card__title">What's happening</h2>
    <div
      v-for="tag in trends"
      :key="tag.name"
      class="trending-item"
    >
      <div class="trending-item__category">Trending</div>
      <div class="trending-item__name">#{{ tag.name }}</div>
      <div class="trending-item__count" v-if="getTagPosts(tag)">
        {{ getTagPosts(tag) }}
      </div>
    </div>
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
  padding: 12px 16px;
  color: var(--text-primary);
}

.trending-item {
  padding: 12px 16px;
  cursor: pointer;
  transition: background-color var(--transition-fast);
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
  margin: 2px 0;
}

.trending-item__count {
  font-size: var(--font-xs);
  color: var(--text-secondary);
}

.trending-card__more {
  padding: 16px;
}

.trending-card__more a {
  color: var(--accent-blue);
  font-size: var(--font-md);
}
</style>
