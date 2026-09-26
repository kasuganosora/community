<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { suggestionApi } from '@/domains/user/infrastructure/suggestionApi'
import { userApi } from '@/domains/user/infrastructure/userApi'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { MessagePlugin } from 'tdesign-vue-next'

const suggestions = ref<MastodonAccount[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const following = ref<Set<string>>(new Set())
const followErrorId = ref<string | null>(null)
// Track in-flight follow requests to prevent duplicate API calls when the
// user rapidly double-clicks before the following Set updates (button only
// becomes disabled after the request succeeds).
const processingFollow = ref<Set<string>>(new Set())
let cancelled = false

async function loadSuggestions() {
  try {
    loading.value = true
    error.value = null
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
}

onMounted(() => {
  loadSuggestions()
})

onUnmounted(() => {
  cancelled = true
})

async function handleFollow(user: MastodonAccount) {
  // Prevent duplicate follow requests from rapid double-clicks
  if (following.value.has(user.id) || processingFollow.value.has(user.id)) return
  try {
    followErrorId.value = null
    const newProcessing = new Set(processingFollow.value)
    newProcessing.add(user.id)
    processingFollow.value = newProcessing
    await userApi.follow(user.id)
    // Trigger reactivity by creating a new Set
    const newFollowing = new Set(following.value)
    newFollowing.add(user.id)
    following.value = newFollowing
  } catch (e) {
    console.error('Failed to follow user:', e)
    followErrorId.value = user.id
    MessagePlugin.error('关注失败，请稍后重试')
  } finally {
    const cleared = new Set(processingFollow.value)
    cleared.delete(user.id)
    processingFollow.value = cleared
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
      <t-button size="small" @click="loadSuggestions">Retry</t-button>
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
        :disabled="following.has(user.id) || processingFollow.has(user.id)"
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
/* Inset grouped card: one soft fill, no border, no shadow. */
.follow-card {
  background: var(--color-bg-grouped);
  border-radius: var(--radius-lg);
  overflow: hidden;
}

.follow-card__title {
  padding: var(--space-3) var(--space-4) var(--space-1);
  font-size: var(--text-title-3);
  line-height: var(--leading-title-3);
  letter-spacing: var(--tracking-title-3);
  font-weight: var(--weight-bold);
  color: var(--color-label);
}

.follow-card__more {
  padding: var(--space-1) var(--space-4) var(--space-3);
}

.follow-card__more a {
  display: inline-flex;
  align-items: center;
  min-height: var(--hit-target);
  color: var(--color-accent-text);
  font-size: var(--text-subhead);
  font-weight: var(--weight-medium);
}

.follow-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-4);
  -webkit-tap-highlight-color: transparent;
  transition: background-color var(--duration-release) var(--ease-out);
}

@media (hover: hover) and (pointer: fine) {
  .follow-item:hover {
    background: var(--color-hover);
  }
}

.follow-item__link {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  flex: 1;
  min-width: 0;
  color: inherit;
  text-decoration: none !important;
}

.follow-item__info {
  flex: 1;
  min-width: 0;
}

.follow-item__name {
  font-size: var(--text-subhead);
  font-weight: var(--weight-semibold);
  color: var(--color-label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.follow-item__username {
  font-size: var(--text-footnote);
  color: var(--color-label-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Label-coloured capsule: the only filled control in the rail. */
.follow-btn {
  flex-shrink: 0;
  background: var(--color-label) !important;
  color: var(--color-bg) !important;
  font-weight: var(--weight-semibold) !important;
  border: none !important;
}

.follow-btn.t-is-disabled {
  background: var(--color-fill) !important;
  color: var(--color-label-secondary) !important;
  opacity: 1;
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
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
}
</style>
