<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import type { MastodonList } from '@/shared/types/mastodon'
import { listApi } from '../../infrastructure/listApi'
import { validateListTitle, MAX_LIST_TITLE_LENGTH } from '../../domain/list'
import { sessionEpoch } from '@/shared/api/sessionScope'

const lists = ref<MastodonList[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const newTitle = ref('')
const createError = ref<string | null>(null)
const creating = ref(false)
let loadVersion = 0
let createVersion = 0
let disposed = false

async function load() {
  const version = ++loadVersion, epoch = sessionEpoch.value
  const current = () => version === loadVersion && epoch === sessionEpoch.value
  loading.value = true
  error.value = null
  try {
    const result = await listApi.getLists()
    if (current()) lists.value = result
  } catch (e) {
    if (current()) error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    if (current()) loading.value = false
  }
}

async function create() {
  if (creating.value || loading.value) return
  createError.value = validateListTitle(newTitle.value)
  if (createError.value) return
  const version = ++createVersion, epoch = sessionEpoch.value
  const current = () => version === createVersion && epoch === sessionEpoch.value
  creating.value = true
  try {
    const list = await listApi.createList(newTitle.value.trim())
    if (!current()) return
    lists.value = [...lists.value, list]
    error.value = null
    newTitle.value = ''
    MessagePlugin.success('列表已创建')
  } catch (e) {
    if (current()) createError.value = e instanceof Error ? e.message : '创建失败'
  } finally {
    if (current()) creating.value = false
  }
}

watch(sessionEpoch, () => {
  ++loadVersion; ++createVersion
  lists.value = []; newTitle.value = ''; error.value = null; createError.value = null
  creating.value = false; loading.value = true
  const epoch = sessionEpoch.value
  void nextTick(() => { if (!disposed && epoch === sessionEpoch.value) void load() })
}, { flush: 'sync' })
onBeforeUnmount(() => { disposed = true; ++loadVersion; ++createVersion })
onMounted(load)
</script>

<template>
  <div class="lists-page">
    <div class="page-header">
      <h1 class="page-header__title">列表</h1>
    </div>

    <form class="lists-create" @submit.prevent="create">
      <label for="new-list-title" class="sr-only">新列表名称</label>
      <input
        id="new-list-title"
        v-model="newTitle"
        class="lists-create__input"
        type="text"
        placeholder="新列表名称"
        :maxlength="MAX_LIST_TITLE_LENGTH"
      />
      <button type="submit" class="btn btn--primary btn--sm" :disabled="creating || loading || !newTitle.trim()" :aria-busy="creating">{{ creating ? '创建中…' : '创建' }}</button>
    </form>
    <p v-if="createError" class="lists-error" role="alert">{{ createError }}</p>
    <p v-else class="lists-note">点开列表查看成员的帖子。管理成员可以把正在关注的人加进去，他们已经发过的公开帖也会出现。</p>

    <div v-if="loading" class="page-loading"><t-loading /></div>

    <div v-else-if="error" class="empty-state" role="alert">
      <div class="empty-state__icon"><t-icon name="error-circle" :size="48" /></div>
      <h2 class="empty-state__title">加载失败</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="load">重试</button>
    </div>

    <div v-else-if="lists.length === 0" class="empty-state">
      <div class="empty-state__icon"><t-icon name="view-list" :size="48" /></div>
      <h2 class="empty-state__title">还没有列表</h2>
      <p class="empty-state__text">创建列表，把关注的人分组，查看只属于他们的时间线。</p>
    </div>

    <ul v-else class="lists-list">
      <li v-for="l in lists" :key="l.id" class="lists-row">
        <router-link :to="`/lists/${l.id}`" class="lists-item">
          <t-icon name="view-list" :size="20" aria-hidden="true" />
          <span class="lists-item__title">{{ l.title }}</span>
          <span class="lists-item__hint">时间线</span>
          <t-icon name="chevron-right" :size="18" aria-hidden="true" />
        </router-link>
        <router-link :to="{ path: `/lists/${l.id}`, query: { manage: '1' } }" class="btn btn--secondary btn--sm">管理成员</router-link>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.lists-page {
  min-height: 100vh;
  min-height: 100dvh;
}

.lists-create {
  display: flex;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
}

.lists-create__input {
  flex: 1;
  min-width: 0;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-separator, var(--border-color));
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: inherit;
  font: inherit;
}

.lists-error {
  padding: 0 var(--space-4) var(--space-2);
  color: var(--color-danger, var(--accent-red, #e0245e));
  font-size: var(--font-sm);
}

.lists-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.lists-note {
  margin: 0;
  padding: 0 var(--space-4) var(--space-3);
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  line-height: var(--leading-footnote);
}

.lists-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding-right: var(--space-3);
  border-bottom: var(--hairline, 1px) solid var(--color-separator, var(--border-color));
}

.lists-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  flex: 1;
  min-width: 0;
  padding: var(--space-3) var(--space-4);
  color: inherit;
  text-decoration: none;
}

.lists-item__title {
  flex: 1;
  min-width: 0;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.lists-item__hint {
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
}
</style>
