<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import { useSettingsScope } from '../composables/useSettingsScope'
import { apiErrorMessage } from '../composables/apiErrorMessage'
import SegmentedControl from '@/shared/components/common/SegmentedControl.vue'
import { moderationApi, type KeywordAttribute, type MastodonFilter } from '../../infrastructure/moderationApi'
import { diffKeywords, parseLines } from '../../domain/lines'

import { t } from '@/shared/i18n'

const MAX_KEYWORD_LENGTH = 200
const ALL_CONTEXTS = ['home', 'notifications', 'public', 'thread', 'account']

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
const editing = ref<MastodonFilter | null>(null)
const titleInput = ref<HTMLInputElement | null>(null)
const draft = reactive({
  title: '',
  keywords: '',
  wholeWord: false,
  action: 'warn' as 'warn' | 'hide',
  context: [...ALL_CONTEXTS] as string[],
})

function resetDraft() {
  editing.value = null
  draft.title = ''
  draft.keywords = ''
  draft.wholeWord = false
  draft.action = 'warn'
  draft.context = [...ALL_CONTEXTS]
  error.value = null
}

const scope = useSettingsScope(() => { filters.value = []; loading.value = false; saving.value = false; removing.value = ''; loadError.value = ''; resetDraft() })

const keywordList = computed(() => parseLines(draft.keywords))
const keywordsHint = computed(() => t('settings.Blocks.keywordsHint', [keywordList.value.length]))
const busy = computed(() => saving.value || loading.value || !!removing.value || !!loadError.value)

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
    if (current()) loadError.value = apiErrorMessage(e, t('settings.FilterSettings.s20'))
  } finally {
    if (current()) loading.value = false
  }
}

function startEdit(filter: MastodonFilter) {
  editing.value = filter
  draft.title = filter.title
  draft.keywords = (filter.keywords ?? []).map((k) => k.keyword).join('\n')
  draft.wholeWord = (filter.keywords ?? []).some((k) => k.whole_word)
  draft.action = filter.filter_action === 'hide' ? 'hide' : 'warn'
  draft.context = [...filter.context]
  error.value = null
  void nextTick(() => {
    titleInput.value?.focus({ preventScroll: true })
    titleInput.value?.closest('form')?.querySelector('.filter-editor')?.scrollIntoView?.({ block: 'start', behavior: 'smooth' })
  })
}

/** keywords_attributes turning the filter being edited into the textarea's list. */
function keywordChanges(filter: MastodonFilter, next: string[]): KeywordAttribute[] {
  const existing = filter.keywords ?? []
  const { add, remove } = diffKeywords(existing.map((k) => k.keyword), next)
  const removed = new Set(remove)
  const attrs: KeywordAttribute[] = []
  for (const keyword of existing) {
    if (!keyword.id) continue
    if (removed.has(keyword.keyword)) attrs.push({ id: keyword.id, _destroy: true })
    else if (keyword.whole_word !== draft.wholeWord) attrs.push({ id: keyword.id, whole_word: draft.wholeWord })
  }
  for (const keyword of add) attrs.push({ keyword, whole_word: draft.wholeWord })
  return attrs
}

async function submit() {
  if (busy.value) return
  const current = scope()
  error.value = null
  const title = draft.title.trim()
  const keywords = keywordList.value
  if (!title || keywords.length === 0) {
    error.value = t('settings.FilterSettings.s21')
    return
  }
  const tooLong = keywords.find((k) => [...k].length > MAX_KEYWORD_LENGTH)
  if (tooLong) {
    error.value = t('settings.Blocks.errors.FILTER_KEYWORD_TOO_LONG')
    return
  }
  if (draft.context.length === 0) {
    error.value = t('settings.FilterSettings.s22')
    return
  }
  saving.value = true
  const target = editing.value
  try {
    const base = { title, context: [...draft.context], filter_action: draft.action }
    const saved = target
      ? await moderationApi.updateFilter(target.id, { ...base, keywords_attributes: keywordChanges(target, keywords) })
      : await moderationApi.createFilter({ ...base, keywords_attributes: keywords.map((keyword) => ({ keyword, whole_word: draft.wholeWord })) })
    if (!current()) return
    filters.value = target
      ? filters.value.map((item) => (item.id === saved.id ? saved : item))
      : [saved, ...filters.value.filter((item) => item.id !== saved.id)]
    resetDraft()
    MessagePlugin.success(target ? t('settings.Blocks.filterSaved') : t('settings.FilterSettings.s23'))
  } catch (e) {
    if (current()) error.value = apiErrorMessage(e, t('settings.FilterSettings.s24'))
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
    if (editing.value?.id === filter.id) resetDraft()
    MessagePlugin.success(t('settings.FilterSettings.s25'))
  } catch (e) {
    if (current()) MessagePlugin.error(apiErrorMessage(e, t('settings.FilterSettings.s26')))
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
  <form class="settings-form" data-testid="filter-settings" @submit.prevent="submit">
    <p v-if="loading" class="settings-field__hint" role="status">{{ $t('settings.FilterSettings.s1') }}</p>
    <div v-else-if="loadError" class="settings-error" role="alert">{{ loadError }} <button type="button" class="btn btn--ghost btn--sm" @click="load">{{ $t('settings.FilterSettings.s2') }}</button></div>
    <ul v-else-if="filters.length" class="filter-list">
      <li v-for="filter in filters" :key="filter.id" class="filter-list__item" :class="{ 'filter-list__item--editing': editing?.id === filter.id }" data-testid="filter-item">
        <span class="filter-list__text">
          <strong>{{ filter.title }}</strong>
          <span class="settings-field__hint">{{ summary(filter) }}</span>
        </span>
        <span class="filter-list__actions">
          <button type="button" class="btn btn--ghost btn--sm" :disabled="!!removing || saving" :aria-pressed="editing?.id === filter.id" @click="startEdit(filter)">{{ $t('settings.Blocks.edit') }}</button>
          <button type="button" class="btn btn--ghost btn--sm filter-list__remove" :disabled="!!removing || saving" :aria-busy="removing === filter.id" @click="remove(filter)">{{ removing === filter.id ? $t('settings.FilterSettings.s3') : $t('settings.FilterSettings.s4') }}</button>
        </span>
      </li>
    </ul>
    <p v-else class="settings-field__hint">{{ $t('settings.FilterSettings.s5') }}</p>

    <div class="filter-editor">
      <p class="filter-editor__title">{{ editing ? $t('settings.Blocks.editingFilter', [editing.title]) : $t('settings.Blocks.newFilter') }}</p>
      <div class="settings-field">
        <label for="filter-title" class="settings-field__label">{{ $t('settings.FilterSettings.s6') }}</label>
        <input id="filter-title" ref="titleInput" v-model="draft.title" class="settings-input" type="text" maxlength="64" autocomplete="off" enterkeyhint="next" />
      </div>
      <div class="settings-field">
        <label for="filter-keywords" class="settings-field__label">{{ $t('settings.FilterSettings.s7') }}</label>
        <textarea
          id="filter-keywords"
          v-model="draft.keywords"
          class="settings-input settings-textarea"
          rows="5"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          aria-describedby="filter-keywords-hint"
          :placeholder="$t('settings.Blocks.keywordsPlaceholder')"
        />
        <span id="filter-keywords-hint" class="settings-field__hint">{{ keywordsHint }}</span>
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
    </div>
    <p v-if="error" class="settings-error" role="alert">{{ error }}</p>
    <div class="settings-actions">
      <button v-if="editing" type="button" class="btn btn--ghost btn--sm" :disabled="saving" @click="resetDraft">{{ $t('settings.Blocks.cancel') }}</button>
      <button type="submit" class="btn btn--primary btn--sm" :disabled="busy">
        {{ saving ? $t('settings.FilterSettings.s11') : editing ? $t('settings.Blocks.saveFilter') : $t('settings.FilterSettings.s12') }}
      </button>
    </div>
  </form>
</template>

<style scoped>
.settings-form { display: flex; flex-direction: column; gap: var(--space-3); }
.filter-editor { display: flex; flex-direction: column; gap: 1.25rem; min-width: 0; }
.filter-editor__title { font-weight: 600; font-size: .9375rem; color: var(--color-label); overflow-wrap: anywhere; }
.settings-textarea { min-height: 7.5rem; resize: vertical; line-height: 1.5; }
.filter-list { display: flex; flex-direction: column; list-style: none; }
.filter-list__item { display: flex; align-items: center; justify-content: space-between; }
.filter-list__item--editing strong { color: var(--color-accent-text); }
.filter-list__text { display: flex; flex-direction: column; gap: .125rem; }
.filter-list__actions { display: flex; flex: none; gap: .25rem; }
.filter-list__actions .btn { min-height: 44px; padding: .5rem .75rem; }
.filter-list__remove { color: var(--color-danger); }
</style>
