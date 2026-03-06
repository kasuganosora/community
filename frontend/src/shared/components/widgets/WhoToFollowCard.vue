<script setup lang="ts">
import { ref, onMounted } from 'vue'
import type { MastodonAccount } from '@/shared/types/mastodon'
import client from '@/shared/api/client'

const suggestions = ref<MastodonAccount[]>([])

onMounted(async () => {
  try {
    const { data } = await client.get('/api/v1/suggestions', { params: { limit: 3 } })
    suggestions.value = data.map((item: any) => item.account || item)
  } catch {
    // Silently fail - card will be hidden if empty
  }
})
</script>

<template>
  <div class="follow-card" v-if="suggestions.length">
    <h2 class="follow-card__title">Who to follow</h2>
    <div
      v-for="user in suggestions"
      :key="user.id"
      class="follow-item"
    >
      <t-avatar :size="40" :image="user.avatar || undefined" class="follow-item__avatar">
        {{ user.display_name?.charAt(0) || 'U' }}
      </t-avatar>
      <div class="follow-item__info">
        <div class="follow-item__name">{{ user.display_name }}</div>
        <div class="follow-item__username">@{{ user.acct }}</div>
      </div>
      <t-button theme="default" shape="round" size="small" class="follow-btn">
        Follow
      </t-button>
    </div>
    <div class="follow-card__more">
      <router-link to="/explore">Show more</router-link>
    </div>
  </div>
</template>

<style scoped>
.follow-card {
  background: var(--bg-secondary);
  border-radius: var(--radius-lg);
  overflow: hidden;
}

.follow-card__title {
  font-size: var(--font-xl);
  font-weight: 800;
  padding: 12px 16px;
  color: var(--text-primary);
}

.follow-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  cursor: pointer;
  transition: background-color var(--transition-fast);
}

.follow-item:hover {
  background: var(--bg-hover);
}

.follow-item__info {
  flex: 1;
  min-width: 0;
}

.follow-item__name {
  font-weight: 700;
  font-size: var(--font-md);
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.follow-item__username {
  font-size: var(--font-sm);
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.follow-btn {
  background: var(--text-primary) !important;
  color: var(--bg-primary) !important;
  font-weight: 700 !important;
  border: none !important;
}

.follow-card__more {
  padding: 16px;
}

.follow-card__more a {
  color: var(--accent-blue);
  font-size: var(--font-md);
}
</style>
