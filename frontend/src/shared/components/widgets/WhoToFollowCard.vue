<script setup lang="ts">
import { displayAccount } from '@/shared/utils/accountDisplay'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { discoveryInvalidation, invalidateDiscovery } from '@/domains/discovery/presentation/composables/discoveryState'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { suggestionApi } from '@/domains/user/infrastructure/suggestionApi'
import { useRelationships } from '@/domains/user/presentation/composables/useRelationships'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { followButtonLabel } from '@/domains/user/domain/relationship'
import { SUGGESTIONS_LIMIT } from '@/shared/utils/constants'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { MessagePlugin } from 'tdesign-vue-next'

const authStore = useAuthStore()
const suggestions = ref<MastodonAccount[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const relationships = useRelationships()
let cancelled = false
let version = 0

async function loadSuggestions() {
  const mine = ++version, epoch = sessionEpoch.value
  suggestions.value = []
  try {
    loading.value = true
    error.value = null
    // The server checks eligibility; relationship hydration also keeps follow controls accurate.
    const data = await suggestionApi.list({ limit: 20 })
    const candidates = data.map(item => item.account).filter(a => a.id !== authStore.currentUser?.id)
    if (mine !== version || epoch !== sessionEpoch.value || cancelled) return
    await relationships.load(candidates.map(a => a.id))
    if (!cancelled && mine === version && epoch === sessionEpoch.value) {
      suggestions.value = candidates
        .filter(a => relationships.state(a.id) === 'none' || relationships.state(a.id) === 'unknown')
        .slice(0, SUGGESTIONS_LIMIT)
    }
  } catch (e) {
    if (!cancelled && mine === version && epoch === sessionEpoch.value) {
      console.error('Failed to load suggestions:', e)
      error.value = e instanceof Error ? e.message : '加载失败'
    }
  } finally {
    if (!cancelled && mine === version && epoch === sessionEpoch.value) {
      loading.value = false
    }
  }
}

watch([sessionEpoch, discoveryInvalidation], () => { ++version; suggestions.value = []; void loadSuggestions() }, { flush: 'sync' })
onMounted(() => {
  loadSuggestions()
})

onUnmounted(() => {
  cancelled = true
})

async function removeSuggestion(user: MastodonAccount) {
  const epoch = sessionEpoch.value
  try {
    await suggestionApi.remove(user.id)
    if (epoch !== sessionEpoch.value) return
    suggestions.value = suggestions.value.filter(item => item.id !== user.id)
    invalidateDiscovery()
    MessagePlugin.success('已移除，可在发现设置中撤销')
  } catch (e) { MessagePlugin.error(e instanceof Error ? e.message : '移除失败，请重试') }
}

async function handleFollow(user: MastodonAccount) {
  try {
    await relationships.toggleFollow(user.id)
  } catch (e) {
    console.error('Failed to toggle follow:', e)
    MessagePlugin.error(e instanceof Error ? e.message : '操作失败，请稍后重试')
  }
}
</script>

<template>
  <div class="follow-card" v-if="loading">
    <h2 class="follow-card__title">推荐关注</h2>
    <div class="follow-card__loading">
      <t-loading size="small" />
    </div>
  </div>
  <div class="follow-card" v-else-if="error && !suggestions.length">
    <h2 class="follow-card__title">推荐关注</h2>
    <div class="follow-card__error">
      <p>{{ error }}</p>
      <t-button size="small" @click="loadSuggestions">重试</t-button>
    </div>
  </div>
  <div class="follow-card" v-else-if="suggestions.length">
    <h2 class="follow-card__title">推荐关注</h2>
    <div
      v-for="user in suggestions"
      :key="user.id"
      class="follow-item"
    >
      <router-link :to="`/@${user.acct}`" class="follow-item__link">
        <UserAvatar :account="user" :size="40" class="follow-item__avatar" />
        <div class="follow-item__info">
          <div class="follow-item__name"><EmojiText :text="user.display_name || user.username" :emojis="user.emojis" /></div>
          <div class="follow-item__username">@{{ displayAccount(user.acct) }}</div>
        </div>
      </router-link>
      <t-button
        theme="default"
        shape="round"
        size="small"
        class="follow-btn"
        :loading="relationships.pending.has(user.id)"
        @click="handleFollow(user)"
      >
        {{ followButtonLabel(relationships.state(user.id) === 'unknown' ? 'none' : relationships.state(user.id), relationships.rels.get(user.id)?.followed_by) }}
      </t-button>
      <button type="button" class="follow-item__remove" :aria-label="`移除 ${user.display_name || user.username} 的关注建议`" @click="removeSuggestion(user)"><t-icon name="close" :size="14" /></button>
    </div>
    <div class="follow-card__more">
      <router-link to="/explore">发现更多</router-link>
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
.follow-item__remove { display: grid; place-items: center; flex: none; width: var(--hit-target); min-height: var(--hit-target); color: var(--color-label-secondary); border-radius: var(--radius-full); }
.follow-item__remove:active:not(:focus-visible) { background: var(--color-pressed); }
</style>
