<script setup lang="ts">
import { isAxiosError } from 'axios'
import { computed, onMounted, reactive, ref } from 'vue'
import { moderationSettingsApi as api, invalidRegexLine, splitLines, type ModerationSettings, type ModerationUpdate, type UgcVisibility } from '../../infrastructure/moderationSettingsApi'
import { AdminAlert, AdminButton, AdminCheck, AdminField, AdminPageHeader } from '../components/ui'

import { t } from '@/shared/i18n'
import { translateApiError } from '@/shared/i18n/apiErrors'
type ListKey = 'preserved_usernames' | 'sensitive_words' | 'prohibited_words' | 'prohibited_words_for_name_of_user' | 'hidden_tags' | 'silenced_hosts' | 'media_silenced_hosts' | 'blocked_hosts'
interface ListSection { key: ListKey; title: string; icon: string; hint: string[]; words?: boolean; placeholder: string }

const LISTS: ListSection[] = [
  { key: 'preserved_usernames', title: t('admin.ModerationSettingsAdminPage.s29'), icon: '🔒', placeholder: 'admin\nsupport', hint: [t('admin.ModerationSettingsAdminPage.s30')] },
  { key: 'sensitive_words', title: t('admin.ModerationSettingsAdminPage.s31'), icon: '⚠️', words: true, placeholder: 'word\nword1 word2\n/re(gex)?/i', hint: [t('admin.ModerationSettingsAdminPage.s32'), t('admin.ModerationSettingsAdminPage.s33')] },
  { key: 'prohibited_words', title: t('admin.ModerationSettingsAdminPage.s34'), icon: '⛔', words: true, placeholder: 'word\nword1 word2\n/re(gex)?/i', hint: [t('admin.ModerationSettingsAdminPage.s35'), t('admin.ModerationSettingsAdminPage.s33')] },
  { key: 'prohibited_words_for_name_of_user', title: t('admin.ModerationSettingsAdminPage.s36'), icon: '🚫', words: true, placeholder: 'word\n/re(gex)?/', hint: [t('admin.ModerationSettingsAdminPage.s37'), t('admin.ModerationSettingsAdminPage.s38')] },
  { key: 'hidden_tags', title: t('admin.ModerationSettingsAdminPage.s39'), icon: '#', placeholder: 'tag', hint: [t('admin.ModerationSettingsAdminPage.s40')] },
  { key: 'silenced_hosts', title: t('admin.ModerationSettingsAdminPage.s41'), icon: '🔇', placeholder: 'example.com', hint: [t('admin.ModerationSettingsAdminPage.s42')] },
  { key: 'media_silenced_hosts', title: t('admin.ModerationSettingsAdminPage.s43'), icon: '🖼️', placeholder: 'example.com', hint: [t('admin.ModerationSettingsAdminPage.s44')] },
  { key: 'blocked_hosts', title: t('admin.ModerationSettingsAdminPage.s45'), icon: '🛑', placeholder: 'example.com', hint: [t('admin.ModerationSettingsAdminPage.s46')] },
]
const UGC_OPTIONS: { value: UgcVisibility; label: string }[] = [
  { value: 'all', label: t('admin.ModerationSettingsAdminPage.s47') },
  { value: 'local', label: t('admin.ModerationSettingsAdminPage.s48') },
  { value: 'none', label: t('admin.ModerationSettingsAdminPage.s49') },
]

const data = ref<ModerationSettings>()
const drafts = reactive<Record<ListKey, string>>({ preserved_usernames: '', sensitive_words: '', prohibited_words: '', prohibited_words_for_name_of_user: '', hidden_tags: '', silenced_hosts: '', media_silenced_hosts: '', blocked_hosts: '' })
const fieldErrors = reactive<Partial<Record<ListKey, string>>>({})
const loading = ref(false), busy = ref<string>(''), error = ref(''), notice = ref('')

const locked = computed(() => new Set(data.value?.environment_locked || []))
function install(s: ModerationSettings) {
  data.value = s
  for (const l of LISTS) drafts[l.key] = (s[l.key] || []).join('\n')
}
function dirty(key: ListKey) { return data.value ? drafts[key] !== (data.value[key] || []).join('\n') : false }
function count(key: ListKey) { return data.value?.[key]?.length || 0 }
function fmt(v?: string) { return v ? new Date(v).toLocaleString() : '' }

async function load() {
  loading.value = true; error.value = ''
  try {
    install(await api.get())
  } catch { error.value = t('admin.ModerationSettingsAdminPage.s50') }
  finally { loading.value = false }
}
async function save(update: ModerationUpdate, tag: string, ok = t('admin.ModerationSettingsAdminPage.s51')) {
  busy.value = tag; error.value = ''; notice.value = ''
  try { install(await api.save(update)); notice.value = ok; return true }
  catch (cause) {
    const status = isAxiosError(cause) ? cause.response?.status : undefined
    const detail = isAxiosError(cause) ? cause.response?.data?.error : undefined
    error.value = status === 409 ? t('admin.ModerationSettingsAdminPage.s52') : status === 403 ? t('admin.ModerationSettingsAdminPage.s53') : t('admin.ModerationSettingsAdminPage.s54', [detail ? `：${translateApiError(detail) ?? detail}` : ''])
    return false
  } finally { busy.value = '' }
}
async function toggleRegistration(value: boolean) {
  if (!data.value) return
  if (value && !window.confirm(t('admin.ModerationSettingsAdminPage.s55'))) return
  await save({ enable_registration: value }, 'registration')
}
async function saveList(l: ListSection) {
  const lines = splitLines(drafts[l.key])
  delete fieldErrors[l.key]
  if (l.words) {
    const bad = invalidRegexLine(lines)
    if (bad) { fieldErrors[l.key] = t('admin.ModerationSettingsAdminPage.s56', [bad]); return }
  }
  await save({ [l.key]: lines } as ModerationUpdate, l.key, t('admin.ModerationSettingsAdminPage.s57', [l.title]))
}

onMounted(load)
</script>

<template>
  <div class="admin-page moderation-settings">
    <AdminPageHeader :title="$t('admin.ModerationSettingsAdminPage.s1')" :description="$t('admin.ModerationSettingsAdminPage.s2')" />
    <p v-if="loading" class="admin-empty" role="status">{{ $t('admin.ModerationSettingsAdminPage.s3') }}</p>
    <AdminAlert v-if="error" tone="danger">{{ error }}<template v-if="!data" #action><AdminButton size="sm" :disabled="loading" @click="load">{{ $t('admin.ModerationSettingsAdminPage.s4') }}</AdminButton></template></AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>

    <template v-if="data">
      <section class="admin-card admin-form mod-basic" :aria-label="$t('admin.ModerationSettingsAdminPage.s5')">
        <AdminCheck variant="switch" :model-value="data.enable_registration" :label="$t('admin.ModerationSettingsAdminPage.s6')"
          :hint="data.auto_close_registration ? $t('admin.ModerationSettingsAdminPage.s7', [data.moderator_inactivity_limit_days]) : $t('admin.ModerationSettingsAdminPage.s8')"
          :disabled="!!busy || locked.has('enable_registration')" @update:model-value="toggleRegistration"
/>
        <div class="mod-sub" data-testid="auto-close-registration">
          <AdminCheck variant="switch" :model-value="data.auto_close_registration" :label="$t('admin.ModerationSettingsAdminPage.s9')"
            :hint="(data.auto_close_registration ? $t('admin.ModerationSettingsAdminPage.s10', [data.moderator_inactivity_limit_days]) : $t('admin.ModerationSettingsAdminPage.s11')) + (data.moderators_last_active_at ? $t('admin.ModerationSettingsAdminPage.s12', [fmt(data.moderators_last_active_at)]) : $t('admin.ModerationSettingsAdminPage.s13'))"
            :disabled="!!busy" @update:model-value="v => save({ auto_close_registration: v }, 'auto')"
/>
        </div>
        <p v-if="data.enable_registration" class="mod-warn">{{ $t('admin.ModerationSettingsAdminPage.s14') }}</p>
        <p v-if="data.registration_auto_closed_at && !data.enable_registration" class="mod-warn">{{ $t('admin.ModerationSettingsAdminPage.s15', [fmt(data.registration_auto_closed_at)]) }}</p>
        <AdminCheck variant="switch" :model-value="data.email_required_for_signup" :label="$t('admin.ModerationSettingsAdminPage.s16')"
          :disabled="!!busy || locked.has('email_required_for_signup')" @update:model-value="v => save({ email_required_for_signup: v }, 'email')"
/>
        <AdminField :label="$t('admin.ModerationSettingsAdminPage.s17')" :hint="$t('admin.ModerationSettingsAdminPage.s18')">
          <template #default="{ id, describedBy }">
            <select :id="id" :aria-describedby="describedBy" :value="data.ugc_visibility_for_visitor" :disabled="!!busy"
              @change="save({ ugc_visibility_for_visitor: ($event.target as HTMLSelectElement).value as UgcVisibility }, 'ugc')"
>
              <option v-for="o in UGC_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
          </template>
        </AdminField>
      </section>

      <details v-for="l in LISTS" :key="l.key" class="admin-card mod-folder" :data-key="l.key">
        <summary><span class="mod-folder__icon" aria-hidden="true">{{ l.icon }}</span><span class="mod-folder__title">{{ l.title }}</span><span class="mod-folder__count">{{ count(l.key) }}</span></summary>
        <form class="mod-folder__body" @submit.prevent="saveList(l)">
          <AdminField :label="l.title" :error="fieldErrors[l.key]" :hint="l.hint.join(' ')">
            <template #default="{ id, describedBy }">
              <textarea :id="id" v-model="drafts[l.key]" :aria-describedby="describedBy" :aria-invalid="fieldErrors[l.key] ? true : undefined" rows="8" spellcheck="false" autocapitalize="off" autocomplete="off" :placeholder="l.placeholder" :disabled="busy === l.key" />
            </template>
          </AdminField>
          <div class="admin-form-actions">
            <AdminButton type="submit" variant="primary" :loading="busy === l.key" :disabled="!!busy || !dirty(l.key)">{{ $t('admin.ModerationSettingsAdminPage.s24') }}</AdminButton>
            <AdminButton :disabled="!!busy || !dirty(l.key)" @click="drafts[l.key] = (data[l.key] || []).join('\n')">{{ $t('admin.ModerationSettingsAdminPage.s28') }}</AdminButton>
          </div>
        </form>
      </details>
    </template>
  </div>
</template>

<style scoped>
.moderation-settings { display: flex; flex-direction: column; gap: 12px; width: 100%; }
.moderation-settings > .admin-card { max-width: none; width: 100%; box-sizing: border-box; }
.mod-basic { display: flex; flex-direction: column; gap: 14px; }
.mod-basic select { width: 100%; }
.mod-warn { margin: -6px 0 0; font-size: 12px; line-height: 1.6; color: var(--color-warning, #b7791f); }
.mod-folder { padding: 0; overflow: hidden; }
.mod-folder > summary { display: flex; align-items: center; gap: 10px; min-height: 48px; padding: 0 16px; cursor: pointer; list-style: none; user-select: none; }
.mod-folder > summary::-webkit-details-marker { display: none; }
.mod-folder > summary::after { content: ''; width: 8px; height: 8px; margin-left: 4px; border-right: 2px solid var(--color-label-tertiary); border-bottom: 2px solid var(--color-label-tertiary); transform: rotate(-45deg); transition: transform .15s; }
.mod-folder[open] > summary::after { transform: rotate(45deg); }
.mod-folder > summary:focus-visible { outline: 2px solid var(--admin-accent); outline-offset: -2px; }
.mod-folder__icon { width: 22px; text-align: center; }
.mod-folder__title { flex: 1; font-weight: 600; }
.mod-folder__count { font-size: 12px; min-width: 22px; text-align: center; padding: 1px 8px; border-radius: 999px; background: var(--color-fill); color: var(--color-label-secondary); font-variant-numeric: tabular-nums; }
.mod-folder__body { padding: 4px 16px 16px; border-top: 1px solid var(--color-separator); display: flex; flex-direction: column; gap: 10px; }
.mod-folder__body textarea { width: 100%; box-sizing: border-box; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; min-height: 140px; resize: vertical; }
.mod-sub { margin-left: 28px; padding-left: 12px; border-left: 2px solid var(--td-component-stroke, #e5e7eb); }
</style>
