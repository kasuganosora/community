<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useSettingsScope } from '../composables/useSettingsScope'
import SegmentedControl from '@/shared/components/common/SegmentedControl.vue'
import { moderationApi, type MastodonFilter } from '../../infrastructure/moderationApi'

const contexts = [
  { id: 'home', label: '首页' },
  { id: 'notifications', label: '通知' },
  { id: 'public', label: '公共时间线' },
  { id: 'thread', label: '帖子详情' },
  { id: 'account', label: '个人主页' },
]

const actionOptions = [
  { value: 'warn' as const, label: '折叠警告' },
  { value: 'hide' as const, label: '直接隐藏' },
]

const filters = ref<MastodonFilter[]>([])
const loading = ref(true)
const saving = ref(false)
const error = ref<string | null>(null)
const loadError = ref('')
const removing = ref('')
const scope = useSettingsScope(() => { filters.value = []; loading.value = false; saving.value = false; removing.value = ''; error.value = null; loadError.value = ''; draft.title = ''; draft.keyword = '' })
const draft = reactive({
  title: '',
  keyword: '',
  wholeWord: false,
  action: 'warn' as 'warn' | 'hide',
  context: ['home', 'notifications', 'public', 'thread', 'account'] as string[],
})

function toggleContext(id: string) {
  draft.context = draft.context.includes(id)
    ? draft.context.filter((item) => item !== id)
    : [...draft.context, id]
}

async function load() {
  const current = scope()
  loading.value = true
  loadError.value = ''
  try {
    const result = await moderationApi.listFilters()
    if (current()) filters.value = result
  } catch (e) {
    if (current()) loadError.value = e instanceof Error ? e.message : '过滤器加载失败'
  } finally {
    if (current()) loading.value = false
  }
}

async function createFilter() {
  if (saving.value || loading.value || removing.value || loadError.value) return
  const current = scope()
  error.value = null
  const title = draft.title.trim()
  const keyword = draft.keyword.trim()
  if (!title || !keyword) {
    error.value = '标题和关键词都要填'
    return
  }
  if (draft.context.length === 0) {
    error.value = '至少选一个生效范围'
    return
  }
  saving.value = true
  try {
    const created = await moderationApi.createFilter({
      title,
      context: [...draft.context],
      filter_action: draft.action,
      keywords: [{ keyword, whole_word: draft.wholeWord }],
    })
    if (!current()) return
    filters.value = [created, ...filters.value.filter((item) => item.id !== created.id)]
    draft.title = ''
    draft.keyword = ''
    MessagePlugin.success('过滤器已添加')
  } catch (e) {
    if (current()) error.value = e instanceof Error ? e.message : '添加失败'
  } finally {
    if (current()) saving.value = false
  }
}

async function remove(filter: MastodonFilter) {
  if (removing.value || saving.value || loading.value) return
  const current = scope()
  removing.value = filter.id
  try {
    await moderationApi.deleteFilter(filter.id)
    if (!current()) return
    filters.value = filters.value.filter((item) => item.id !== filter.id)
    MessagePlugin.success('已删除')
  } catch (e) {
    if (current()) MessagePlugin.error(e instanceof Error ? e.message : '删除失败')
  } finally {
    if (current()) removing.value = ''
  }
}

function summary(filter: MastodonFilter) {
  const words = (filter.keywords ?? []).map((item) => item.keyword).filter(Boolean).join('、')
  const action = filter.filter_action === 'hide' ? '隐藏' : '警告'
  return words ? `${action} · ${words}` : action
}

onMounted(load)
</script>

<template>
  <form class="settings-form" @submit.prevent="createFilter">
    <p v-if="loading" class="settings-field__hint" role="status">正在加载…</p>
    <div v-else-if="loadError" class="settings-error" role="alert">{{ loadError }} <button type="button" class="btn btn--ghost btn--sm" @click="load">重试</button></div>
    <ul v-else-if="filters.length" class="filter-list">
      <li v-for="filter in filters" :key="filter.id" class="filter-list__item">
        <span>
          <strong>{{ filter.title }}</strong>
          <span class="settings-field__hint">{{ summary(filter) }}</span>
        </span>
        <button type="button" class="btn btn--ghost btn--sm" :disabled="!!removing || saving" :aria-busy="removing === filter.id" @click="remove(filter)">{{ removing === filter.id ? '删除中…' : '删除' }}</button>
      </li>
    </ul>
    <p v-else class="settings-field__hint">还没有过滤器。命中的帖子会先折叠，你可以再点开。</p>

    <div class="settings-field">
      <label for="filter-title" class="settings-field__label">标题</label>
      <input id="filter-title" v-model="draft.title" class="settings-input" type="text" maxlength="64" autocomplete="off" />
    </div>
    <div class="settings-field">
      <label for="filter-keyword" class="settings-field__label">关键词</label>
      <input id="filter-keyword" v-model="draft.keyword" class="settings-input" type="text" maxlength="80" autocomplete="off" />
    </div>
    <label class="filter-check">
      <input v-model="draft.wholeWord" type="checkbox" />
      <span>只匹配完整的词。中文句子里建议关掉，否则嵌在句子中不会命中。</span>
    </label>
    <SegmentedControl v-model="draft.action" :options="actionOptions" label="命中后" />
    <div class="filter-contexts" role="group" aria-label="生效范围">
      <label v-for="item in contexts" :key="item.id" class="filter-check">
        <input type="checkbox" :checked="draft.context.includes(item.id)" @change="toggleContext(item.id)" />
        <span>{{ item.label }}</span>
      </label>
    </div>
    <p v-if="error" class="settings-error" role="alert">{{ error }}</p>
    <div class="settings-actions">
      <button type="submit" class="btn btn--primary btn--sm" :disabled="saving || loading || !!removing || !!loadError">{{ saving ? '添加中…' : '添加过滤器' }}</button>
    </div>
  </form>
</template>

<style scoped>
.settings-form { display: flex; flex-direction: column; gap: var(--space-3); }
.settings-field { display: flex; flex-direction: column; gap: var(--space-1); }
.settings-field__label { font-size: var(--text-footnote, var(--font-sm)); font-weight: 600; }
.settings-field__hint { display: block; font-size: var(--text-footnote, var(--font-xs)); color: var(--color-label-secondary, var(--text-secondary)); }
.settings-input {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-separator, var(--border-color));
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: inherit;
  font: inherit;
}
.settings-actions { display: flex; justify-content: flex-end; }
.settings-error { color: var(--color-danger, #e0245e); font-size: var(--font-sm); }
.filter-list { display: flex; flex-direction: column; gap: var(--space-2); list-style: none; }
.filter-list__item { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
.filter-check { display: flex; align-items: flex-start; gap: var(--space-2); font-size: var(--text-footnote); color: var(--color-label-secondary); }
.filter-contexts { display: flex; flex-wrap: wrap; gap: var(--space-2) var(--space-3); }
</style>
