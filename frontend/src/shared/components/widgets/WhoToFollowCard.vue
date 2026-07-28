<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { suggestionApi } from '@/domains/user/infrastructure/suggestionApi'
import { userApi } from '@/domains/user/infrastructure/userApi'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'

const suggestions = ref<MastodonAccount[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const following = ref<Set<string>>(new Set())
let cancelled = false

onMounted(async () => {
  try {
    loading.value = true
    const data = await suggestionApi.list({ limit: 3 })
    if (!cancelled) {
      suggestions.value = data.map((item) => item.account)
    }
  } catch (e) {
    if (!cancelled) {
      console.error('Failed to load suggestions:', e)
      error.value = 'Failed to load suggestions'
    }
  } finally {
    if (!cancelled) {
      loading.value = false
    }
  }
})

onUnmounted(() => {
  cancelled = true
})

async function handleFollow(user: MastodonAccount) {
  try {
    await userApi.follow(user.id)
    following.value.add(user.id)
  } catch (e) {
    console.error('Failed to follow user:', e)
    error.value = 'Failed to follow user'
  }
}
</script>

<template>
  <div class="follow-card" v-if="loading">
    <h2 class="follow-card__title">Who to follow</h2>
    <div class="follow-card__loading">
      <t-loading size="small" />
    </div>
  </div>
  <div class="follow-card" v-else-if="error && !suggestions.length">
    <h2 class="follow-card__title">Who to follow</h2>
    <div class="follow-card__error">
      <p>{{ error }}</p>
      <t-button size="small" @click="() => { error = null; loading = true; }">Retry</t-button>
    </div>
  </div>
  <div class="follow-card" v-else-if="suggestions.length">
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

.follow-card__loading {
  display: flex;
  justify-content: center;
  padding: var(--space-6) 0;
}

.follow-card__error {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-4);
  color: var(--text-secondary);
}
</style>
