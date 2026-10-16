<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import { useSettingsScope } from '../composables/useSettingsScope'
import SegmentedControl from '@/shared/components/common/SegmentedControl.vue'
import { moderationApi, type MastodonFilter } from '../../infrastructure/moderationApi'

import { t } from '@/shared/i18n'
const contexts = [
  { id: 'home', label: t('settings.FilterSettings.s13') },
  { id: 'notifications', label: t('settings.FilterSettings.s14') },
  { id: 'public', label: t('settings.FilterSettings.s15') },
  { id: 'thread', label: t('settings.FilterSettings.s16') },
  { id: 'account', label: t('settings.FilterSettings.s17') },
]

const actionOptions = [
  { value: 'warn' as const, label: t('settings.FilterSettings.s18') },
  { value: 'hide' as const, label: t('settings.FilterSettings.s19') },
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
    if (current()) loadError.value = e instanceof Error ? e.message : t('settings.FilterSettings.s20')
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
    error.value = t('settings.FilterSettings.s21')
    return
  }
  if (draft.context.length === 0) {
    error.value = t('settings.FilterSettings.s22')
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
    MessagePlugin.success(t('settings.FilterSettings.s23'))
  } catch (e) {
    if (current()) error.value = e instanceof Error ? e.message : t('settings.FilterSettings.s24')
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
    MessagePlugin.success(t('settings.FilterSettings.s25'))
  } catch (e) {
    if (current()) MessagePlugin.error(e instanceof Error ? e.message : t('settings.FilterSettings.s26'))
  } finally {
    if (current()) removing.value = ''
  }
}

function summary(filter: MastodonFilter) {
  const words = (filter.keywords ?? []).map((item) => item.keyword).filter(Boolean).join('、')
  const action = filter.filter_action === 'hide' ? t('settings.FilterSettings.s27') : t('settings.FilterSettings.s28')
  return words ? `${action} · ${words}` : action
}

onMounted(load)
</script>

<template>
  <form class="settings-form" @submit.prevent="createFilter">
    <p v-if="loading" class="settings-field__hint" role="status">{{ $t('settings.FilterSettings.s1') }}</p>
    <div v-else-if="loadError" class="settings-error" role="alert">{{ loadError }} <button type="button" class="btn btn--ghost btn--sm" @click="load">{{ $t('settings.FilterSettings.s2') }}</button></div>
    <ul v-else-if="filters.length" class="filter-list">
      <li v-for="filter in filters" :key="filter.id" class="filter-list__item">
        <span>
          <strong>{{ filter.title }}</strong>
          <span class="settings-field__hint">{{ summary(filter) }}</span>
        </span>
        <button type="button" class="btn btn--ghost btn--sm" :disabled="!!removing || saving" :aria-busy="removing === filter.id" @click="remove(filter)">{{ removing === filter.id ? $t('settings.FilterSettings.s3') : $t('settings.FilterSettings.s4') }}</button>
      </li>
    </ul>
    <p v-else class="settings-field__hint">{{ $t('settings.FilterSettings.s5') }}</p>

    <div class="settings-field">
      <label for="filter-title" class="settings-field__label">{{ $t('settings.FilterSettings.s6') }}</label>
      <input id="filter-title" v-model="draft.title" class="settings-input" type="text" maxlength="64" autocomplete="off" />
    </div>
    <div class="settings-field">
      <label for="filter-keyword" class="settings-field__label">{{ $t('settings.FilterSettings.s7') }}</label>
      <input id="filter-keyword" v-model="draft.keyword" class="settings-input" type="text" maxlength="80" autocomplete="off" />
    </div>
    <label class="filter-check">
      <input v-model="draft.wholeWord" type="checkbox" />
      <span>{{ $t('settings.FilterSettings.s8') }}</span>
    </label>
    <SegmentedControl v-model="draft.action" :options="actionOptions" :label="$t('settings.FilterSettings.s9')" />
    <div class="filter-contexts" role="group" :aria-label="$t('settings.FilterSettings.s10')">
      <label v-for="item in contexts" :key="item.id" class="filter-check">
        <input type="checkbox" :checked="draft.context.includes(item.id)" @change="toggleContext(item.id)" />
        <span>{{ item.label }}</span>
      </label>
    </div>
    <p v-if="error" class="settings-error" role="alert">{{ error }}</p>
    <div class="settings-actions">
      <button type="submit" class="btn btn--primary btn--sm" :disabled="saving || loading || !!removing || !!loadError">{{ saving ? $t('settings.FilterSettings.s11') : $t('settings.FilterSettings.s12') }}</button>
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
