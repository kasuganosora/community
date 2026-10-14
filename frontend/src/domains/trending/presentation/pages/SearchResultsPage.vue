<script setup lang="ts">
import { ref, watch, computed, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { MastodonStatus, MastodonAccount, MastodonTag, SearchResult } from '@/shared/types/mastodon'
import client from '@/shared/api/client'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { useRelationships } from '@/domains/user/presentation/composables/useRelationships'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { MessagePlugin } from 'tdesign-vue-next'
import PostCard from '@/shared/components/post/PostCard.vue'
import AccountRow from '@/shared/components/common/AccountRow.vue'
import TabBar from '@/shared/components/common/TabBar.vue'
import SearchBox from '@/shared/components/widgets/SearchBox.vue'
import GroupSearchResults from '../components/GroupSearchResults.vue'
import { isGroupReference } from '../../domain/groupSearch'

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
type TabType = 'posts' | 'people' | 'tags' | 'groups'
const TABS: { value: TabType; label: string }[] = [
  { value: 'posts', label: '帖子' },
  { value: 'people', label: '用户' },
  { value: 'tags', label: '话题' },
  { value: 'groups', label: '群组' },
]
const activeTab = ref<TabType>(getQueryParam(route.query.type) === 'groups' || isGroupReference(getQueryParam(route.query.q)) ? 'groups' : 'posts')
const groupRefreshKey = ref(0)
const loading = ref(false)
const error = ref<string | null>(null)
const searched = ref(false)
const query = ref(getQueryParam(route.query.q))
const groupVia = computed(() => Array.isArray(route.query.via) ? route.query.via.filter((value): value is string => typeof value === 'string') : typeof route.query.via === 'string' ? [route.query.via] : [])

const statuses = ref<MastodonStatus[]>([])
const accounts = ref<MastodonAccount[]>([])
const hashtags = ref<MastodonTag[]>([])

const counts = computed<Record<Exclude<TabType, 'groups'>, number>>(() => ({
  posts: statuses.value.length,
  people: accounts.value.length,
  tags: hashtags.value.length,
}))

const tabOptions = computed(() => TABS.map((tab) => ({
  value: tab.value,
  label: searched.value && tab.value !== 'groups' ? `${tab.label} ${counts.value[tab.value]}` : tab.label,
})))

const SEARCH_LIMIT = 20

// Version guard — prevents stale search results from a previous
// query from overwriting newer results when the user types fast.
let searchVersion = 0
let searchController: AbortController | null = null

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
  searchController?.abort()
  searchController = new AbortController()
  error.value = null
  if (!trimmedQuery || activeTab.value === 'groups') {
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
      params: { q: trimmedQuery, limit: SEARCH_LIMIT, resolve: authStore.isAuthenticated },
      signal: searchController.signal,
    })
    if (myVersion !== searchVersion) return
    statuses.value = data.statuses || []
    accounts.value = data.accounts || []
    hashtags.value = data.hashtags || []
    searched.value = true
    // Land on the first tab that has results.
    if (counts.value[activeTab.value] === 0) {
      activeTab.value = TABS.find(t => t.value !== 'groups' && counts.value[t.value] > 0)?.value ?? activeTab.value
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
  if (!q && activeTab.value !== 'groups') return
  const type = activeTab.value === 'groups' || isGroupReference(q) ? 'groups' : undefined
  if (q === getQueryParam(route.query.q) && type === route.query.type) {
    if (activeTab.value === 'groups') groupRefreshKey.value++
    else void search(q)
  } else void router.push({ name: 'search', query: { q: q || undefined, type } }).catch(() => {})
}

watch(() => [route.query.q, route.query.type], () => {
  query.value = getQueryParam(route.query.q)
  if (getQueryParam(route.query.type) === 'groups' || isGroupReference(query.value)) activeTab.value = 'groups'
  else if (activeTab.value === 'groups') activeTab.value = 'posts'
  void search(query.value)
}, { immediate: true })

watch(activeTab, (tab, previous) => {
  if (tab === 'groups') {
    searchVersion++
    searchController?.abort()
    loading.value = false
    void router.replace({ query: { ...route.query, type: 'groups' } }).catch(() => {})
  } else if (previous === 'groups') {
    void router.replace({ query: { ...route.query, type: undefined } }).catch(() => {})
  }
})
onBeforeUnmount(() => { searchVersion++; searchController?.abort() })

</script>

<template>
  <div class="search-page">
    <div class="page-header">
      <h1 class="page-header__title">搜索</h1>
    </div>

    <SearchBox v-model="query" class="search-form" embedded :navigate="false" :allow-empty="activeTab === 'groups'" :placeholder="activeTab === 'groups' ? '搜索群名或粘贴群地址' : '搜索帖子、用户、话题或群组'" @search="submit" />

    <TabBar v-model="activeTab" :options="tabOptions" label="搜索结果" />

    <GroupSearchResults v-if="activeTab === 'groups'" :query="getQueryParam(route.query.q)" :via="groupVia" :preview-ref="getQueryParam(route.query.preview) || undefined" :refresh-key="groupRefreshKey" />

    <div v-else-if="loading" class="page-loading">
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

    <div v-else-if="!searched" class="search-empty">输入关键词搜索帖子、用户或 #话题，也可以切换到群组搜索。</div>

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
  display: flex;
  gap: var(--space-2);
  align-items: center;
  padding: var(--space-2) var(--gutter);
}
.search-form :deep(.t-input__wrap) { min-width: 0; flex: 1; }
.search-form > button { flex-shrink: 0; min-height: 2.75rem; }

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
