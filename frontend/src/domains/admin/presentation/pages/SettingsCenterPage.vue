<script setup lang="ts">
function lockHint(field: { locked_by?: string; deployment_key?: string; key: string }): string {
  const name = field.deployment_key || field.key
  return field.locked_by === 'process' ? i18nT('admin.SettingsCenterPage.processLockedHint', [name]) : i18nT('admin.SettingsCenterPage.envFileLockedHint', [name])
}
import { useI18n as useLockI18n } from 'vue-i18n'
const { t: i18nT } = useLockI18n()

import { isAxiosError } from 'axios'
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { settingsAdminApi as api, type SettingsSection } from '../../infrastructure/settingsAdminApi'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { tokenService } from '@/shared/api/client'
import { loadBranding } from '@/shared/composables/useBranding'
import { AdminAlert, AdminButton, AdminCheck, AdminField, AdminPageHeader } from '../components/ui'
import SettingsAdminPage from './SettingsAdminPage.vue'

import { t } from '@/shared/i18n'
const props = defineProps<{ section: string }>()
const data = ref<SettingsSection>()
const draft = reactive<Record<string, string | number | boolean>>({})
const clearSecrets = reactive<Record<string, boolean>>({})
const fieldErrors = reactive<Record<string, string>>({})
const loading = ref(false)
const busy = ref(false)
const testing = ref(false)
const error = ref('')
const notice = ref('')
const testNotice = ref('')
let generation = 0

const lockedFields = computed(() => data.value?.fields.filter(field => field.source === 'environment') || [])
const editable = computed(() => data.value?.fields.some(field => !field.read_only))
const changes = computed(() => {
  const values: Record<string, string | number | boolean> = {}
  const clear: string[] = []
  for (const field of data.value?.fields || []) {
    if (field.read_only) continue
    if (field.type === 'secret') {
      if (clearSecrets[field.key]) clear.push(field.key)
      else if (draft[field.key]) values[field.key] = draft[field.key]!
    } else if (draft[field.key] !== field.value) values[field.key] = draft[field.key]!
  }
  return { values, clear }
})
// Client-side checks mirroring the server (JSON object / hex colour), so a
// typo is reported next to the field before the request.
const localErrors = computed(() => {
  const errors: Record<string, string> = {}
  for (const field of data.value?.fields || []) {
    const value = String(draft[field.key] ?? '').trim()
    if (field.read_only || !value) continue
    if (field.type === 'json') {
      try {
        const parsed = JSON.parse(value)
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) errors[field.key] = t('admin.SettingsCenterPage.s36')
      } catch {
        errors[field.key] = t('admin.SettingsCenterPage.s37')
      }
    } else if (field.type === 'color' && !/^#([0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})$/i.test(value)) {
      errors[field.key] = t('admin.SettingsCenterPage.s38')
    }
  }
  return errors
})
const hasLocalErrors = computed(() => Object.keys(localErrors.value).length > 0)
function colorPickerValue(value: unknown) {
  const v = String(value ?? '')
  if (/^#[0-9a-f]{6}$/i.test(v)) return v.toLowerCase()
  if (/^#[0-9a-f]{3}$/i.test(v)) return `#${v[1]}${v[1]}${v[2]}${v[2]}${v[3]}${v[3]}`.toLowerCase()
  return '#000000'
}
const dirty = computed(() => Object.keys(changes.value.values).length + changes.value.clear.length > 0)
const connectionSection = computed(() => props.section === 'email' || props.section === 'storage')
function install(section: SettingsSection) {
  data.value = section
  for (const key of Object.keys(fieldErrors)) delete fieldErrors[key]
  for (const key of Object.keys(draft)) delete draft[key]
  for (const key of Object.keys(clearSecrets)) delete clearSecrets[key]
  for (const field of section.fields) draft[field.key] = field.type === 'secret' ? '' : field.value ?? ''
}
async function load() {
  const version = ++generation
  const identity = tokenService.get()
  data.value = undefined
  for (const key of Object.keys(draft)) delete draft[key]
  for (const key of Object.keys(clearSecrets)) delete clearSecrets[key]
  error.value = ''; notice.value = ''; testNotice.value = ''
  busy.value = false; testing.value = false
  loading.value = false
  if (props.section === 'security') return
  loading.value = true
  try {
    const result = await api.get(props.section)
    if (version === generation && identity === tokenService.get()) install(result)
  } catch {
    if (version === generation) error.value = t('admin.SettingsCenterPage.s39')
  } finally {
    if (version === generation) loading.value = false
  }
}
async function save() {
  if (busy.value || !dirty.value || !data.value || hasLocalErrors.value) return
  if (changes.value.clear.length && !window.confirm(t('admin.SettingsCenterPage.s40'))) return
  const version = generation
  const identity = tokenService.get()
  const restart = data.value.fields.some(f => f.restart_required && (f.key in changes.value.values || changes.value.clear.includes(f.key)))
  busy.value = true; error.value = ''; notice.value = ''; testNotice.value = ''
  for (const key of Object.keys(fieldErrors)) delete fieldErrors[key]
  try {
    const result = await api.save(props.section, changes.value.values, changes.value.clear)
    if (version !== generation || identity !== tokenService.get()) return
    install(result)
    if (props.section === 'branding') void loadBranding(true)
    notice.value = restart ? t('admin.SettingsCenterPage.s41') : t('admin.SettingsCenterPage.s42')
  } catch (cause) {
    if (version !== generation || identity !== tokenService.get()) return
    const status = isAxiosError(cause) ? cause.response?.status : undefined
    const detail = isAxiosError(cause) ? cause.response?.data?.error : undefined
    const invalid = data.value?.fields.find(field => detail === `invalid value for ${field.key}`)
    if (invalid) fieldErrors[invalid.key] = t('admin.SettingsCenterPage.s43')
    error.value = status === 409 ? t('admin.SettingsCenterPage.s44')
      : status === 403 ? t('admin.SettingsCenterPage.s45')
      : t('admin.SettingsCenterPage.s46')
  } finally {
    if (version === generation) busy.value = false
  }
}
async function testConnection() {
  if (testing.value || busy.value || dirty.value || (props.section !== 'email' && props.section !== 'storage')) return
  const version = generation
  const identity = tokenService.get()
  testing.value = true; testNotice.value = ''; error.value = ''
  try {
    const result = await api.test(props.section)
    if (version !== generation || identity !== tokenService.get()) return
    if (result.success) testNotice.value = result.message || t('admin.SettingsCenterPage.s47')
    else error.value = result.message || t('admin.SettingsCenterPage.s48')
  } catch {
    if (version === generation) error.value = t('admin.SettingsCenterPage.s49')
  } finally {
    if (version === generation) testing.value = false
  }
}
watch(() => props.section, load, { immediate: true })
watch(sessionEpoch, () => {
  ++generation
  data.value = undefined; error.value = ''; notice.value = ''; testNotice.value = ''
  loading.value = false; busy.value = false; testing.value = false
  for (const key of Object.keys(draft)) delete draft[key]
  for (const key of Object.keys(clearSecrets)) delete clearSecrets[key]
}, { flush: 'sync' })
onUnmounted(() => { generation++; for (const key of Object.keys(draft)) delete draft[key] })
</script>
<template>
  <SettingsAdminPage v-if="section === 'security'" mode="security" />
  <div v-else class="admin-page settings-center">
    <AdminPageHeader :title="data?.title || $t('admin.SettingsCenterPage.s1')" :description="data?.description" />
    <p v-if="loading" class="admin-empty" role="status">{{ $t('admin.SettingsCenterPage.s2') }}</p>
    <AdminAlert v-if="error" tone="danger">
      {{ error }}
      <template v-if="!data" #action><AdminButton size="sm" :disabled="loading" @click="load">{{ $t('admin.SettingsCenterPage.s3') }}</AdminButton></template>
    </AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <section v-if="lockedFields.length" class="admin-card settings-center__deployment" :aria-label="$t('admin.SettingsCenterPage.s4')">
      <div><h2>{{ $t('admin.SettingsCenterPage.s5') }}</h2><p>{{ $t('admin.SettingsCenterPage.s6') }}</p></div>
      <details><summary>{{ $t('admin.SettingsCenterPage.s7') }}</summary><p>{{ $t('admin.SettingsCenterPage.s8') }}</p><ul><li v-for="field in lockedFields" :key="field.key">{{ field.label }}：<code>{{ field.deployment_key || field.key }}</code></li></ul></details>
    </section>
    <form v-if="data" class="admin-card admin-preferences" @submit.prevent="save">
      <div class="admin-preferences__heading"><h2>{{ editable ? $t('admin.SettingsCenterPage.s9') : $t('admin.SettingsCenterPage.s10') }}</h2><span>{{ editable ? $t('admin.SettingsCenterPage.s11') : $t('admin.SettingsCenterPage.s12') }}</span></div>
      <div class="admin-form admin-preferences__fields">
        <template v-for="field in data.fields" :key="field.key">
          <AdminCheck variant="switch" class="admin-preferences__toggle" v-if="field.type === 'boolean' && !field.read_only" :model-value="Boolean(draft[field.key])" @update:model-value="draft[field.key] = $event" :label="field.label" :hint="field.description" :disabled="busy || testing" />
          <AdminField v-else :label="field.label" :error="localErrors[field.key] || fieldErrors[field.key]" :hint="[field.description, field.read_only ? (field.source === 'environment' ? lockHint(field) : $t('admin.SettingsCenterPage.s14')) : field.restart_required ? $t('admin.SettingsCenterPage.s15') : $t('admin.SettingsCenterPage.s16')].filter(Boolean).join(' ')">
            <template #default="{ id, describedBy }">
              <template v-if="field.read_only">
                <div class="settings-center__readonly">
                  <textarea v-if="field.type === 'textarea'" :id="id" readonly :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" :value="String(field.value ?? '')" rows="4" />
                  <input v-else :id="id" type="text" readonly :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" :value="field.type === 'secret' ? (field.secret_set ? $t('admin.SettingsCenterPage.s17') : $t('admin.SettingsCenterPage.s18')) : typeof field.value === 'boolean' ? (field.value ? $t('admin.SettingsCenterPage.s19') : $t('admin.SettingsCenterPage.s20')) : String(field.value ?? '')" :placeholder="$t('admin.SettingsCenterPage.s21')" />
                  <span class="settings-center__source" v-if="field.source === 'environment'">{{ field.locked_by === 'process' ? $t('admin.SettingsCenterPage.processLocked') : $t('admin.SettingsCenterPage.envFileLocked') }}<code v-if="field.deployment_key"> {{ field.deployment_key }}</code></span><span class="settings-center__source" v-else>{{ $t('admin.SettingsCenterPage.s23') }}</span>
                </div>
              </template>
              <select v-else-if="field.options?.length" :id="id" :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" v-model="draft[field.key]" :disabled="busy || testing"><option v-for="option in field.options" :key="option" :value="option">{{ option }}</option></select>
              <textarea v-else-if="field.type === 'json'" :id="id" class="settings-center__code" :aria-describedby="describedBy" :aria-invalid="localErrors[field.key] || fieldErrors[field.key] ? true : undefined" :value="String(draft[field.key] ?? '')" :maxlength="field.max" :disabled="busy || testing" rows="6" spellcheck="false" autocapitalize="off" autocomplete="off" placeholder="{ }" @input="draft[field.key] = ($event.target as HTMLTextAreaElement).value" />
              <div v-else-if="field.type === 'color'" class="settings-center__color">
                <input :id="id" :aria-describedby="describedBy" :aria-invalid="localErrors[field.key] || fieldErrors[field.key] ? true : undefined" v-model="draft[field.key]" type="text" inputmode="text" autocapitalize="off" spellcheck="false" :maxlength="field.max" :placeholder="$t('admin.SettingsCenterPage.s24')" :disabled="busy || testing" />
                <input type="color" class="settings-center__swatch" :aria-label="$t('admin.SettingsCenterPage.s25', [field.label])" :value="colorPickerValue(draft[field.key])" :disabled="busy || testing" @input="draft[field.key] = ($event.target as HTMLInputElement).value" />
              </div>
              <textarea v-else-if="field.type === 'textarea'" :id="id" :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" :value="String(draft[field.key] ?? '')" :maxlength="field.max" :disabled="busy || testing" rows="5" @input="draft[field.key] = ($event.target as HTMLTextAreaElement).value" />
              <input v-else-if="field.type === 'number'" :id="id" :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" v-model.number="draft[field.key]" type="number" required :min="field.min" :max="field.max" :disabled="busy || testing" />
              <template v-else-if="field.type === 'secret'">
                <input :id="id" :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" v-model="draft[field.key]" type="password" autocomplete="new-password" :placeholder="$t('admin.SettingsCenterPage.s26')" :disabled="busy || testing || clearSecrets[field.key]" />
                <AdminCheck v-if="field.secret_set" v-model="clearSecrets[field.key]" :label="$t('admin.SettingsCenterPage.s27', [field.label])" :disabled="busy || testing" />
              </template>
              <input v-else :id="id" :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" v-model="draft[field.key]" :type="field.type" :maxlength="field.max" :disabled="busy || testing" />
            </template>
          </AdminField>
        </template>
      </div>
      <div class="admin-form-actions">
        <AdminButton v-if="editable" type="submit" variant="primary" :loading="busy" :disabled="testing || !dirty || hasLocalErrors">{{ busy ? $t('admin.SettingsCenterPage.s28') : $t('admin.SettingsCenterPage.s29') }}</AdminButton>
        <AdminButton :disabled="busy || testing || dirty" @click="load">{{ $t('admin.SettingsCenterPage.s30') }}</AdminButton>
      </div>
    </form>
    <section v-if="connectionSection && data" class="admin-card">
      <h2>{{ $t('admin.SettingsCenterPage.s31') }}</h2>
      <p>{{ props.section === 'email' ? $t('admin.SettingsCenterPage.testEmailLive') : $t('admin.SettingsCenterPage.s32') }}</p>
      <p v-if="dirty" class="settings-center__pending" role="status">{{ $t('admin.SettingsCenterPage.s33') }}</p>
      <div class="admin-form-actions"><AdminButton :loading="testing" :disabled="busy || testing || dirty" @click="testConnection">{{ testing ? $t('admin.SettingsCenterPage.s34') : $t('admin.SettingsCenterPage.s35') }}</AdminButton></div>
      <p v-if="testNotice" role="status">{{ testNotice }}</p>
    </section>
  </div>
</template>
<style scoped>
.settings-center__deployment h2 { margin: 0; font-size: 16px; }
.settings-center__readonly { position: relative; min-width: 0; }
.settings-center__readonly :is(input, textarea) { padding-right: 88px; background: var(--color-bg-grouped); color: var(--color-label-secondary); cursor: text; }
.settings-center__source { position: absolute; right: 10px; top: 11px; font-size: 11px; line-height: 18px; padding: 0 6px; border-radius: 4px; background: var(--color-fill); color: var(--color-label-secondary); pointer-events: none; }
.settings-center__deployment { border-inline-start: 3px solid var(--admin-accent); }
.settings-center__deployment p { line-height: 1.7; margin: 8px 0 0; }
.settings-center__deployment summary { cursor: pointer; font-size: 13px; color: var(--admin-accent); min-height: 32px; align-content: center; }
.settings-center__deployment ul { padding-left: 20px; font-size: 12px; line-height: 2; overflow-wrap: anywhere; }
.settings-center__code { font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace); font-size: 13px; tab-size: 2; }
.settings-center__color { display: flex; gap: 8px; align-items: stretch; min-width: 0; }
.settings-center__color input[type="text"] { flex: 1; min-width: 0; }
.settings-center__swatch { flex: none; width: 44px; min-height: 40px; padding: 2px; border-radius: 8px; border: 1px solid var(--color-separator); background: var(--color-bg-row); cursor: pointer; }
.settings-center__pending { color: var(--color-label-secondary); font-size: 13px; }
@media(max-width: 760px) {
  .settings-center__source { position: static; display: inline-block; margin-top: 6px; }
  .settings-center__readonly :is(input, textarea) { padding-right: 12px; }
}
</style>
