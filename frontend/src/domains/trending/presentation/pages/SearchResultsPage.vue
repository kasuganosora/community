<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import type { MastodonStatus, MastodonAccount, MastodonTag, SearchResult } from '@/shared/types/mastodon'
import client from '@/shared/api/client'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import PostCard from '@/shared/components/post/PostCard.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import RichText from '@/shared/components/common/RichText.vue'

// Type-safe route params helper
function getQueryParam(q: unknown): string {
  if (typeof q === 'string') return q
  if (Array.isArray(q) && typeof q[0] === 'string') return q[0]
  return ''
}

function getTagPostCount(tag: MastodonTag): string {
  const first = tag.history?.[0]
  if (first) {
    return first.uses + ' posts'
  }
  return ''
}

const route = useRoute()
type TabType = 'posts' | 'people' | 'tags'
const activeTab = ref<TabType>('posts')
const loading = ref(false)

const statuses = ref<MastodonStatus[]>([])
const accounts = ref<MastodonAccount[]>([])
const hashtags = ref<MastodonTag[]>([])
const followingMap = ref(new Map<string, boolean>())

const SEARCH_LIMIT = 20

// Version guard — prevents stale search results from a previous
// query from overwriting newer results when the user types fast.
let searchVersion = 0

// Reuse the shared action handlers — optimistic update on the local array.
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

// Track which follow actions are in-flight to prevent duplicate toggles.
const pendingFollows = ref(new Set<string>())

async function toggleFollow(account: MastodonAccount) {
  if (pendingFollows.value.has(account.id)) return
  pendingFollows.value.add(account.id)

  const wasFollowing = followingMap.value.get(account.id) ?? false
  // Optimistic update for immediate UI feedback
  followingMap.value.set(account.id, !wasFollowing)
  try {
    const rel = wasFollowing
      ? await userApi.unfollow(account.id)
      : await userApi.follow(account.id)
    followingMap.value.set(account.id, rel.following)
  } catch (e) {
    // Revert optimistic update on failure
    followingMap.value.set(account.id, wasFollowing)
    console.error('Failed to toggle follow:', e)
  } finally {
    pendingFollows.value.delete(account.id)
  }
}

async function search(query: string) {
  const trimmedQuery = query.trim()
  if (!trimmedQuery) return
  const myVersion = ++searchVersion
  loading.value = true
  try {
    const { data } = await client.get<SearchResult>('/api/v2/search', {
      params: { q: trimmedQuery, limit: SEARCH_LIMIT },
    })
    if (myVersion !== searchVersion) return
    statuses.value = data.statuses || []
    accounts.value = data.accounts || []
    hashtags.value = data.hashtags || []
  } catch {
    if (myVersion !== searchVersion) return
    statuses.value = []
    accounts.value = []
    hashtags.value = []
  } finally {
    if (myVersion === searchVersion) {
      loading.value = false
    }
  }
}

onMounted(() => {
  const q = getQueryParam(route.query.q)
  if (q) search(q)
})

watch(() => route.query.q, (q) => {
  const query = getQueryParam(q)
  if (query) search(query)
})

</script>

<template>
  <div class="search-page">
    <div class="page-header">
      <h1 class="page-header__title">Search: {{ route.query.q }}</h1>
    </div>

    <div class="tab-bar">
      <button
        v-for="tab in ['posts', 'people', 'tags']"
        :key="tab"
        class="tab-bar__item"
        :class="{ 'tab-bar__item--active': activeTab === tab }"
        @click="activeTab = tab as TabType"
      >
        <span class="tab-bar__text">{{ tab.charAt(0).toUpperCase() + tab.slice(1) }}</span>
        <div v-if="activeTab === tab" class="tab-bar__indicator" />
      </button>
    </div>

    <div v-if="loading" class="page-loading">
      <t-loading />
    </div>

    <template v-else>
      <!-- Posts tab -->
      <template v-if="activeTab === 'posts'">
        <PostCard
          v-for="s in statuses"
          :key="s.id"
          :status="s"
          @reblog="toggleReblog"
          @favourite="toggleFavourite"
          @bookmark="toggleBookmark"
        />
        <div v-if="statuses.length === 0" class="search-empty">No posts found</div>
      </template>

      <!-- People tab -->
      <template v-if="activeTab === 'people'">
        <div v-for="user in accounts" :key="user.id" class="user-item">
          <UserAvatar :account="user" :size="48" />
          <div class="user-item__info">
            <div class="user-item__name">{{ user.display_name }}</div>
            <div class="user-item__username">@{{ user.acct }}</div>
            <RichText class="user-item__bio" :html="user.note" />
          </div>
          <t-button theme="default" shape="round" size="small" class="follow-btn"
            @click.stop="toggleFollow(user)"
          >
            {{ followingMap.get(user.id) ? 'Following' : 'Follow' }}
          </t-button>
        </div>
        <div v-if="accounts.length === 0" class="search-empty">No people found</div>
      </template>

      <!-- Tags tab -->
      <template v-if="activeTab === 'tags'">
        <div v-for="tag in hashtags" :key="tag.name" class="tag-item">
          <div class="tag-item__name">#{{ tag.name }}</div>
          <div class="tag-item__count" v-if="getTagPostCount(tag)" :key="tag.name">
            {{ getTagPostCount(tag) }}
          </div>
        </div>
        <div v-if="hashtags.length === 0" class="search-empty">No tags found</div>
      </template>
    </template>
  </div>
</template>

<style scoped>
.search-page {
  min-height: 100dvh;
}

.search-empty {
  text-align: center;
  padding: var(--space-12) var(--space-8);
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
}

.user-item,
.tag-item {
  position: relative;
  -webkit-tap-highlight-color: transparent;
  transition: background-color var(--duration-release) var(--ease-out);
  cursor: pointer;
}

.user-item::after,
.tag-item::after {
  content: '';
  position: absolute;
  right: 0;
  bottom: 0;
  height: var(--hairline);
  background: var(--color-separator);
}

.user-item::after {
  left: calc(var(--gutter) + 3rem + var(--space-3));
}

.tag-item::after {
  left: var(--gutter);
}

@media (hover: hover) and (pointer: fine) {
  .user-item:hover,
  .tag-item:hover {
    background: var(--color-hover);
  }
}

.user-item:active,
.tag-item:active {
  background: var(--color-pressed);
  transition-duration: 0s;
}

.user-item {
  display: flex;
  gap: var(--space-3);
  padding: var(--space-3) var(--gutter);
}

.user-item__info {
  flex: 1;
  min-width: 0;
}

.user-item__name {
  font-size: var(--text-body);
  font-weight: var(--weight-semibold);
  color: var(--color-label);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-item__username {
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-item__bio {
  margin-top: var(--space-1);
  font-size: var(--text-subhead);
  line-height: var(--leading-subhead);
  color: var(--color-label);
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.follow-btn {
  align-self: center;
  flex-shrink: 0;
  background: var(--color-label) !important;
  color: var(--color-bg) !important;
  font-weight: var(--weight-semibold) !important;
  border: none !important;
}

.tag-item {
  padding: var(--space-3) var(--gutter);
}

.tag-item__name {
  font-size: var(--text-body);
  font-weight: var(--weight-semibold);
  color: var(--color-label);
}

.tag-item__count {
  font-size: var(--text-footnote);
  color: var(--color-label-secondary);
  font-variant-numeric: tabular-nums;
}
</style>
