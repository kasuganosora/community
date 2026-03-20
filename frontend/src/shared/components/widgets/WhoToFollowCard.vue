<script setup lang="ts">
import { ref, onMounted } from 'vue'
import type { MastodonAccount } from '@/shared/types/mastodon'
import client from '@/shared/api/client'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'

interface SuggestionItem {
  account: MastodonAccount
}

const suggestions = ref<MastodonAccount[]>([])
const error = ref<string | null>(null)
const following = ref<Set<string>>(new Set())

onMounted(async () => {
  try {
    const { data } = await client.get<SuggestionItem[]>('/api/v1/suggestions', { params: { limit: 3 } })
    suggestions.value = data.map((item) => item.account)
  } catch (e) {
    console.error('Failed to load suggestions:', e)
    error.value = 'Failed to load suggestions'
  }
})

async function handleFollow(user: MastodonAccount) {
  try {
    // TODO: Implement actual follow API call
    // await client.post(`/api/v1/accounts/${user.id}/follow`)
    following.value.add(user.id)
  } catch (e) {
    console.error('Failed to follow user:', e)
  }
}
</script>

<template>
  <div class="follow-card" v-if="suggestions.length">
    <h2 class="follow-card__title">Who to follow</h2>
    <div
      v-for="user in suggestions"
      :key="user.id"
      class="follow-item"
    >
      <router-link :to="`/@${user.acct}`" class="follow-item__link">
        <UserAvatar :account="user" :size="40" class="follow-item__avatar" />
        <div class="follow-item__info">
          <div class="follow-item__name">{{ user.display_name }}</div>
          <div class="follow-item__username">@{{ user.acct }}</div>
        </div>
      </router-link>
      <t-button 
        theme="default" 
        shape="round" 
        size="small" 
        class="follow-btn"
        :disabled="following.has(user.id)"
        @click="handleFollow(user)"
      >
        {{ following.has(user.id) ? 'Following' : 'Follow' }}
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
  padding: var(--space-3) var(--space-4);
  color: var(--text-primary);
}

.follow-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  transition: background-color var(--transition-fast);
}

.follow-item:hover {
  background: var(--bg-hover);
}

.follow-item__link {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  flex: 1;
  min-width: 0;
  text-decoration: none;
  color: inherit;
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
  padding: var(--space-4);
}

.follow-card__more a {
  color: var(--accent-blue);
  font-size: var(--font-md);
}
</style>
