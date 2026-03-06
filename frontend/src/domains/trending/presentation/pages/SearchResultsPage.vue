<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import type { MastodonStatus, MastodonAccount, MastodonTag, SearchResult } from '@/shared/types/mastodon'
import client from '@/shared/api/client'
import PostCard from '@/shared/components/post/PostCard.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'

function getTagPostCount(tag: MastodonTag): string {
  const first = tag.history?.[0]
  if (first) {
    return first.uses + ' posts'
  }
  return ''
}

const route = useRoute()
const activeTab = ref('posts')
const loading = ref(false)

const statuses = ref<MastodonStatus[]>([])
const accounts = ref<MastodonAccount[]>([])
const hashtags = ref<MastodonTag[]>([])

async function search(query: string) {
  if (!query) return
  loading.value = true
  try {
    const { data } = await client.get<SearchResult>('/api/v2/search', {
      params: { q: query, limit: 20 },
    })
    statuses.value = data.statuses || []
    accounts.value = data.accounts || []
    hashtags.value = data.hashtags || []
  } catch {
    statuses.value = []
    accounts.value = []
    hashtags.value = []
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  const q = route.query.q as string
  if (q) search(q)
})

watch(() => route.query.q, (q) => {
  if (q) search(q as string)
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
        @click="activeTab = tab"
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
        <PostCard v-for="s in statuses" :key="s.id" :status="s" />
        <div v-if="statuses.length === 0" class="search-empty">No posts found</div>
      </template>

      <!-- People tab -->
      <template v-if="activeTab === 'people'">
        <div v-for="user in accounts" :key="user.id" class="user-item">
          <UserAvatar :account="user" :size="48" />
          <div class="user-item__info">
            <div class="user-item__name">{{ user.display_name }}</div>
            <div class="user-item__username">@{{ user.acct }}</div>
            <div class="user-item__bio" v-html="user.note" />
          </div>
          <t-button theme="default" shape="round" size="small" class="follow-btn">Follow</t-button>
        </div>
        <div v-if="accounts.length === 0" class="search-empty">No people found</div>
      </template>

      <!-- Tags tab -->
      <template v-if="activeTab === 'tags'">
        <div v-for="tag in hashtags" :key="tag.name" class="tag-item">
          <div class="tag-item__name">#{{ tag.name }}</div>
          <div class="tag-item__count" v-if="getTagPostCount(tag)">
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
  min-height: 100vh;
}

.search-empty {
  text-align: center;
  padding: 48px;
  color: var(--text-secondary);
}

.user-item {
  display: flex;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border-color);
  cursor: pointer;
  transition: background-color var(--transition-fast);
}

.user-item:hover {
  background: var(--bg-hover);
}

.user-item__info {
  flex: 1;
  min-width: 0;
}

.user-item__name {
  font-weight: 700;
}

.user-item__username {
  color: var(--text-secondary);
  font-size: var(--font-sm);
}

.user-item__bio {
  margin-top: 4px;
  font-size: var(--font-sm);
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.follow-btn {
  align-self: center;
  background: var(--text-primary) !important;
  color: var(--bg-primary) !important;
  font-weight: 700 !important;
  border: none !important;
}

.tag-item {
  padding: 12px 16px;
  border-bottom: 1px solid var(--border-color);
  cursor: pointer;
  transition: background-color var(--transition-fast);
}

.tag-item:hover {
  background: var(--bg-hover);
}

.tag-item__name {
  font-weight: 700;
  font-size: var(--font-md);
  color: var(--text-primary);
}

.tag-item__count {
  font-size: var(--font-xs);
  color: var(--text-secondary);
}
</style>
