<script setup lang="ts">
import { ref, watch, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { MastodonStatus, MastodonAccount, MastodonTag, SearchResult } from '@/shared/types/mastodon'
import client from '@/shared/api/client'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { useRelationships } from '@/domains/user/presentation/composables/useRelationships'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { MessagePlugin } from 'tdesign-vue-next'
import PostCard from '@/shared/components/post/PostCard.vue'
import AccountRow from '@/shared/components/common/AccountRow.vue'

// Type-safe route params helper
function getQueryParam(q: unknown): string {
  if (typeof q === 'string') return q
  if (Array.isArray(q) && typeof q[0] === 'string') return q[0]
  return ''
}

function getTagPostCount(tag: MastodonTag): string {
  const uses = tag.history?.reduce((n, h) => n + Number(h.uses || 0), 0) ?? 0
  return uses ? `${uses} 条帖子` : ''
}

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
type TabType = 'posts' | 'people' | 'tags'
const TABS: { value: TabType; label: string }[] = [
  { value: 'posts', label: '帖子' },
  { value: 'people', label: '用户' },
  { value: 'tags', label: '话题' },
]
const activeTab = ref<TabType>('posts')
const loading = ref(false)
const error = ref<string | null>(null)
const searched = ref(false)
const query = ref(getQueryParam(route.query.q))

const statuses = ref<MastodonStatus[]>([])
const accounts = ref<MastodonAccount[]>([])
const hashtags = ref<MastodonTag[]>([])

const counts = computed<Record<TabType, number>>(() => ({
  posts: statuses.value.length,
  people: accounts.value.length,
  tags: hashtags.value.length,
}))

const SEARCH_LIMIT = 20

// Version guard — prevents stale search results from a previous
// query from overwriting newer results when the user types fast.
let searchVersion = 0

// Reuse the shared action handlers — optimistic update on the local array.
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

const relationships = useRelationships()

async function toggleFollow(account: MastodonAccount) {
  try {
    await relationships.toggleFollow(account.id)
  } catch (e) {
    MessagePlugin.error(e instanceof Error ? e.message : '操作失败')
  }
}

async function search(q: string) {
  const trimmedQuery = q.trim()
  const myVersion = ++searchVersion
  error.value = null
  if (!trimmedQuery) {
    statuses.value = []
    accounts.value = []
    hashtags.value = []
    searched.value = false
    loading.value = false
    return
  }
  loading.value = true
  try {
    const { data } = await client.get<SearchResult>('/api/v2/search', {
      params: { q: trimmedQuery, limit: SEARCH_LIMIT },
    })
    if (myVersion !== searchVersion) return
    statuses.value = data.statuses || []
    accounts.value = data.accounts || []
    hashtags.value = data.hashtags || []
    searched.value = true
    // Land on the first tab that has results.
    if (counts.value[activeTab.value] === 0) {
      activeTab.value = TABS.find(t => counts.value[t.value] > 0)?.value ?? activeTab.value
    }
    relationships.load(accounts.value.filter(a => a.id !== authStore.currentUser?.id).map(a => a.id))
  } catch (e) {
    if (myVersion !== searchVersion) return
    statuses.value = []
    accounts.value = []
    hashtags.value = []
    error.value = e instanceof Error ? e.message : '搜索失败'
  } finally {
    if (myVersion === searchVersion) {
      loading.value = false
    }
  }
}

function submit() {
  const q = query.value.trim()
  if (!q) return
  if (q === getQueryParam(route.query.q)) search(q)
  else router.push({ name: 'search', query: { q } }).catch(() => {})
}

onMounted(() => search(getQueryParam(route.query.q)))

watch(() => route.query.q, (q) => {
  query.value = getQueryParam(q)
  search(query.value)
})

</script>

<template>
  <div class="search-page">
    <div class="page-header">
      <h1 class="page-header__title">搜索</h1>
    </div>

    <form class="search-form" role="search" @submit.prevent="submit">
      <t-input
        v-model="query"
        placeholder="搜索帖子、用户或话题"
        aria-label="搜索"
        clearable
        @enter="submit"
      >
        <template #prefixIcon>
          <t-icon name="search" />
        </template>
      </t-input>
    </form>

    <div class="tab-bar" role="tablist" aria-label="搜索结果">
      <button
        v-for="tab in TABS"
        :key="tab.value"
        type="button"
        role="tab"
        class="tab-bar__item"
        :class="{ 'tab-bar__item--active': activeTab === tab.value }"
        :aria-selected="activeTab === tab.value"
        @click="activeTab = tab.value"
      >
        <span class="tab-bar__text">{{ tab.label }}<template v-if="searched"> {{ counts[tab.value] }}</template></span>
        <div v-if="activeTab === tab.value" class="tab-bar__indicator" />
      </button>
    </div>

    <div v-if="loading" class="page-loading">
      <t-loading />
    </div>

    <div v-else-if="error" class="empty-state" role="alert">
      <div class="empty-state__icon">
        <t-icon name="error-circle" :size="48" />
      </div>
      <h2 class="empty-state__title">搜索失败</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="search(query)">重试</button>
    </div>

    <div v-else-if="!searched" class="search-empty">输入关键词搜索帖子、用户或 #话题</div>

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
        <div v-if="statuses.length === 0" class="search-empty">没有找到相关帖子</div>
      </template>

      <!-- People tab -->
      <template v-if="activeTab === 'people'">
        <AccountRow
          v-for="user in accounts"
          :key="user.id"
          :account="user"
          :follow-state="user.id === authStore.currentUser?.id ? undefined : relationships.state(user.id)"
          :followed-by="relationships.rels.get(user.id)?.followed_by"
          :busy="relationships.pending.has(user.id)"
          @toggle-follow="toggleFollow"
        />
        <div v-if="accounts.length === 0" class="search-empty">没有找到相关用户</div>
      </template>

      <!-- Tags tab -->
      <template v-if="activeTab === 'tags'">
        <router-link
          v-for="tag in hashtags"
          :key="tag.name"
          class="tag-item"
          :to="`/tags/${encodeURIComponent(tag.name.replace(/^#/, ''))}`"
                  >
          <div class="tag-item__name">#{{ tag.name.replace(/^#/, '') }}</div>
          <div class="tag-item__count" v-if="getTagPostCount(tag)">
            {{ getTagPostCount(tag) }}
          </div>
        </router-link>
        <div v-if="hashtags.length === 0" class="search-empty">没有找到相关话题</div>
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

.search-form {
  padding: var(--space-2) var(--gutter);
}

.tag-item {
  display: block;
  position: relative;
  padding: var(--space-3) var(--gutter);
  color: inherit;
  text-decoration: none;
  -webkit-tap-highlight-color: transparent;
  transition: background-color var(--duration-release) var(--ease-out);
  cursor: pointer;
}

.tag-item::after {
  content: '';
  position: absolute;
  left: var(--gutter);
  right: 0;
  bottom: 0;
  height: var(--hairline);
  background: var(--color-separator);
}

@media (hover: hover) and (pointer: fine) {
  .tag-item:hover {
    background: var(--color-hover);
  }
}

.tag-item:active {
  background: var(--color-pressed);
  transition-duration: 0s;
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
