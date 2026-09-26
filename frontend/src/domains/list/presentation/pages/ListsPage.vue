<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import type { MastodonList } from '@/shared/types/mastodon'
import { listApi } from '../../infrastructure/listApi'
import { validateListTitle, MAX_LIST_TITLE_LENGTH } from '../../domain/list'

const lists = ref<MastodonList[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const newTitle = ref('')
const createError = ref<string | null>(null)
const creating = ref(false)

async function load() {
  loading.value = true
  error.value = null
  try {
    lists.value = await listApi.getLists()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

async function create() {
  createError.value = validateListTitle(newTitle.value)
  if (createError.value) return
  creating.value = true
  try {
    const list = await listApi.createList(newTitle.value.trim())
    lists.value = [...lists.value, list]
    newTitle.value = ''
    MessagePlugin.success('列表已创建')
  } catch (e) {
    createError.value = e instanceof Error ? e.message : '创建失败'
  } finally {
    creating.value = false
  }
}

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
      <button type="submit" class="btn btn--primary btn--sm" :disabled="creating || !newTitle.trim()">创建</button>
    </form>
    <p v-if="createError" class="lists-error" role="alert">{{ createError }}</p>

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
      <li v-for="l in lists" :key="l.id">
        <router-link :to="`/lists/${l.id}`" class="lists-item">
          <t-icon name="view-list" :size="20" aria-hidden="true" />
          <span class="lists-item__title">{{ l.title }}</span>
          <t-icon name="chevron-right" :size="18" aria-hidden="true" />
        </router-link>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.lists-page {
  min-height: 100vh;
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

.lists-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  color: inherit;
  text-decoration: none;
  border-bottom: var(--hairline, 1px) solid var(--color-separator, var(--border-color));
}

.lists-item__title {
  flex: 1;
  font-weight: 600;
}
</style>
