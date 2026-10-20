<script setup lang="ts">
/**
 * External services → LLM: providers (API keys are write-only and only shown
 * masked), which services use them (currently translation), translation mode
 * and cost limits, connection test and a daily usage summary.
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { isAxiosError } from 'axios'
import { AdminAlert, AdminBadge, AdminButton, AdminCheck, AdminEmpty, AdminField } from './ui'
import { llmAdminApi as api, type LlmProviderType, type LlmSettings, type LlmTestResult, type LlmTranslationSettings, type LlmUsage } from '../../infrastructure/llmAdminApi'
import { t } from '@/shared/i18n'
import { translateApiError } from '@/shared/i18n/apiErrors'

interface Draft {
  ref: string
  id: string
  name: string
  type: LlmProviderType
  base_url: string
  model: string
  api_key: string
  clear_api_key: boolean
  api_key_set: boolean
  api_key_mask: string
  timeout_seconds: number | ''
  max_tokens: number | ''
  temperature: number | ''
  input_price_per_1m: number | ''
  output_price_per_1m: number | ''
  currency: string
}

const loaded = ref<LlmSettings>()
const drafts = ref<Draft[]>([])
const editing = ref<string | null>(null)
const translationService = ref('')
const tr = reactive<LlmTranslationSettings>({ enabled: false, mode: 'traditional', user_daily_limit: 50, daily_limit: 1000, user_per_minute: 10 })
const usage = ref<LlmUsage>()
const loading = ref(true), busy = ref(false), dirty = ref(false)
const error = ref(''), notice = ref('')
const testing = ref<string | null>(null)
const tests = reactive<Record<string, LlmTestResult>>({})
let seq = 0

const TYPE_LABELS: Record<LlmProviderType, string> = { openai: 'OpenAI', openai_compatible: 'OpenAI-compatible', anthropic: 'Anthropic', google: 'Google Gemini', grok: 'xAI Grok' }

function num(v: number | undefined | null): number | '' { return typeof v === 'number' && v !== 0 ? v : '' }
function install(s: LlmSettings) {
  loaded.value = s
  drafts.value = s.providers.map(p => ({
    ref: p.id, id: p.id, name: p.name, type: p.type, base_url: p.base_url || '', model: p.model, api_key: '', clear_api_key: false,
    api_key_set: p.api_key_set, api_key_mask: p.api_key_mask || '', timeout_seconds: num(p.timeout_seconds), max_tokens: num(p.max_tokens),
    temperature: typeof p.temperature === 'number' ? p.temperature : '', input_price_per_1m: num(p.input_price_per_1m), output_price_per_1m: num(p.output_price_per_1m), currency: p.currency || '',
  }))
  translationService.value = s.services.translation || ''
  Object.assign(tr, s.translation)
  dirty.value = false
  editing.value = null
}
async function loadUsage() {
  try { usage.value = await api.usage(30) } catch { usage.value = undefined }
}
async function load() {
  loading.value = true; error.value = ''
  try { install(await api.get()); void loadUsage() } catch { error.value = t('admin.Llm.loadError') } finally { loading.value = false }
}
function touch() { dirty.value = true; notice.value = '' }
function addProvider() {
  const ref = `new-${++seq}`
  drafts.value.push({ ref, id: '', name: '', type: 'openai', base_url: '', model: '', api_key: '', clear_api_key: false, api_key_set: false, api_key_mask: '', timeout_seconds: '', max_tokens: '', temperature: '', input_price_per_1m: '', output_price_per_1m: '', currency: 'USD' })
  editing.value = ref
  touch()
}
function removeProvider(d: Draft) {
  if (!window.confirm(t('admin.Llm.removeConfirm', [d.name || d.model]))) return
  drafts.value = drafts.value.filter(x => x !== d)
  if (translationService.value === d.ref) translationService.value = ''
  touch()
}
const providerOptions = computed(() => drafts.value.map(d => ({ value: d.ref, label: d.name || d.model || t('admin.Llm.unnamed') })))
const errorField = ref('')

function errorText(code?: string): string {
  return (code && translateApiError(code)) || t('admin.Llm.saveError')
}
async function save() {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''; errorField.value = ''
  const services: Record<string, string> = {}
  const providers = drafts.value.map((d, i) => {
    if (translationService.value === d.ref) services.translation = d.id || `#${i}`
    const out: Record<string, unknown> = {
      id: d.id, name: d.name.trim(), type: d.type, base_url: d.base_url.trim(), model: d.model.trim(),
      timeout_seconds: d.timeout_seconds || 0, max_tokens: d.max_tokens || 0, temperature: d.temperature === '' ? null : d.temperature,
      input_price_per_1m: d.input_price_per_1m || 0, output_price_per_1m: d.output_price_per_1m || 0, currency: d.currency.trim(),
    }
    if (d.clear_api_key) out.clear_api_key = true
    else if (d.api_key.trim()) out.api_key = d.api_key
    return out
  })
  try {
    install(await api.save({ providers: providers as never, services, translation: { ...tr } }))
    notice.value = t('admin.Llm.saved')
  } catch (cause) {
    const data = isAxiosError(cause) ? cause.response?.data as { error_code?: string; field?: string } | undefined : undefined
    error.value = cause && isAxiosError(cause) && cause.response?.status === 403 ? t('admin.Llm.forbidden') : errorText(data?.error_code)
    errorField.value = data?.field || ''
  } finally {
    busy.value = false
  }
}
async function test(d: Draft) {
  if (!d.id || testing.value) return
  testing.value = d.id
  delete tests[d.id]
  try { tests[d.id] = await api.test(d.id) } catch { tests[d.id] = { ok: false, code: 'llm_request_failed', latency_ms: 0, model: d.model } } finally { testing.value = null; void loadUsage() }
}
function testText(r: LlmTestResult): string {
  if (r.ok) return t('admin.Llm.testOk', [r.latency_ms])
  const key = r.code ? `admin.Llm.testError.${r.code.replace(/^llm_/, '').replace(/_([a-z])/g, (_, c: string) => c.toUpperCase())}` : ''
  const known = ['authFailed', 'rateLimited', 'timeout', 'unreachable', 'invalidRequest', 'contentPolicy', 'requestFailed']
  return known.some(k => key.endsWith(`.${k}`)) ? t(key) : t('admin.Llm.testError.requestFailed')
}
function formatCost(cost: Record<string, number>): string {
  const entries = Object.entries(cost)
  if (!entries.length) return '—'
  return entries.map(([cur, v]) => `${v.toFixed(4)} ${cur || ''}`.trim()).join(' · ')
}
const fmt = (n: number) => n.toLocaleString()
onMounted(load)
</script>

<template>
  <section class="admin-card llm-card" :aria-label="$t('admin.Llm.title')">
    <div class="llm-card__heading">
      <h2>{{ $t('admin.Llm.title') }}</h2>
      <p>{{ $t('admin.Llm.description') }}</p>
    </div>
    <p v-if="loading" class="admin-empty" role="status">{{ $t('admin.Llm.loading') }}</p>
    <AdminAlert v-if="error" tone="danger">{{ error }}<template v-if="!loaded" #action><AdminButton size="sm" @click="load">{{ $t('admin.Llm.retry') }}</AdminButton></template></AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>

    <form v-if="loaded" class="admin-form" @submit.prevent="save">
      <h3 class="llm-card__sub">{{ $t('admin.Llm.providers') }}</h3>
      <AdminEmpty v-if="!drafts.length">{{ $t('admin.Llm.noProviders') }}</AdminEmpty>
      <ul class="llm-card__list">
        <li v-for="d in drafts" :key="d.ref" class="llm-provider" :data-testid="`llm-provider-${d.ref}`">
          <div class="llm-provider__row">
            <div class="llm-provider__main">
              <strong>{{ d.name || $t('admin.Llm.unnamed') }}</strong>
              <span class="llm-provider__meta">
                <AdminBadge tone="info">{{ TYPE_LABELS[d.type] }}</AdminBadge>
                <code>{{ d.model || '—' }}</code>
                <AdminBadge v-if="d.clear_api_key" tone="warning">{{ $t('admin.Llm.keyWillClear') }}</AdminBadge>
                <AdminBadge v-else-if="d.api_key" tone="warning">{{ $t('admin.Llm.keyPending') }}</AdminBadge>
                <AdminBadge v-else-if="d.api_key_set" tone="success">{{ $t('admin.Llm.keySet', [d.api_key_mask]) }}</AdminBadge>
                <AdminBadge v-else tone="neutral">{{ $t('admin.Llm.keyMissing') }}</AdminBadge>
                <AdminBadge v-if="translationService === d.ref" tone="info">{{ $t('admin.Llm.usedByTranslation') }}</AdminBadge>
              </span>
            </div>
            <div class="llm-provider__actions">
              <AdminButton size="sm" :disabled="!d.id || dirty || !!testing || busy" :loading="testing === d.id" @click="test(d)">{{ $t('admin.Llm.test') }}</AdminButton>
              <AdminButton size="sm" :aria-expanded="editing === d.ref" @click="editing = editing === d.ref ? null : d.ref">{{ editing === d.ref ? $t('admin.Llm.collapse') : $t('admin.Llm.edit') }}</AdminButton>
              <AdminButton size="sm" variant="danger" :disabled="busy" @click="removeProvider(d)">{{ $t('admin.Llm.remove') }}</AdminButton>
            </div>
          </div>
          <p v-if="d.id && tests[d.id]" class="llm-provider__test" :class="tests[d.id]!.ok ? 'is-ok' : 'is-error'" role="status">{{ testText(tests[d.id]!) }}</p>
          <div v-if="editing === d.ref" class="llm-provider__form">
            <AdminField :label="$t('admin.Llm.name')" :error="errorField.endsWith('.name') ? ' ' : undefined"><template #default="{ id }"><input :id="id" v-model="d.name" maxlength="64" required autocomplete="off" :placeholder="$t('admin.Llm.namePlaceholder')" @input="touch" /></template></AdminField>
            <AdminField :label="$t('admin.Llm.type')"><template #default="{ id }"><select :id="id" v-model="d.type" @change="touch"><option v-for="tp in loaded.provider_types" :key="tp" :value="tp">{{ TYPE_LABELS[tp] || tp }}</option></select></template></AdminField>
            <AdminField :label="$t('admin.Llm.baseUrl')" :hint="d.type === 'openai_compatible' ? $t('admin.Llm.baseUrlRequired') : $t('admin.Llm.baseUrlHint')"><template #default="{ id, describedBy }"><input :id="id" v-model="d.base_url" type="url" inputmode="url" autocapitalize="off" spellcheck="false" maxlength="2048" :aria-describedby="describedBy" placeholder="https://api.example.com/v1" @input="touch" /></template></AdminField>
            <AdminField :label="$t('admin.Llm.model')"><template #default="{ id }"><input :id="id" v-model="d.model" maxlength="128" required autocapitalize="off" spellcheck="false" placeholder="example-model" @input="touch" /></template></AdminField>
            <AdminField :label="$t('admin.Llm.apiKey')" :hint="d.api_key_set ? $t('admin.Llm.apiKeyKeep') : $t('admin.Llm.apiKeyHint')">
              <template #default="{ id, describedBy }">
                <input :id="id" v-model="d.api_key" type="password" autocomplete="new-password" spellcheck="false" maxlength="4096" :aria-describedby="describedBy" :disabled="d.clear_api_key" :placeholder="d.api_key_set ? d.api_key_mask : $t('admin.Llm.apiKeyPlaceholder')" @input="touch" />
                <AdminCheck v-if="d.api_key_set" v-model="d.clear_api_key" :label="$t('admin.Llm.clearKey')" @update:model-value="touch" />
              </template>
            </AdminField>
            <details class="llm-provider__advanced">
              <summary>{{ $t('admin.Llm.advanced') }}</summary>
              <div class="llm-provider__grid">
                <AdminField :label="$t('admin.Llm.timeout')"><template #default="{ id }"><input :id="id" v-model.number="d.timeout_seconds" type="number" min="1" max="600" inputmode="numeric" placeholder="60" @input="touch" /></template></AdminField>
                <AdminField :label="$t('admin.Llm.maxTokens')"><template #default="{ id }"><input :id="id" v-model.number="d.max_tokens" type="number" min="1" max="200000" inputmode="numeric" :placeholder="$t('admin.Llm.auto')" @input="touch" /></template></AdminField>
                <AdminField :label="$t('admin.Llm.temperature')"><template #default="{ id }"><input :id="id" v-model.number="d.temperature" type="number" min="0" max="2" step="0.1" inputmode="decimal" :placeholder="$t('admin.Llm.auto')" @input="touch" /></template></AdminField>
                <AdminField :label="$t('admin.Llm.inputPrice')"><template #default="{ id }"><input :id="id" v-model.number="d.input_price_per_1m" type="number" min="0" step="0.0001" inputmode="decimal" placeholder="0" @input="touch" /></template></AdminField>
                <AdminField :label="$t('admin.Llm.outputPrice')"><template #default="{ id }"><input :id="id" v-model.number="d.output_price_per_1m" type="number" min="0" step="0.0001" inputmode="decimal" placeholder="0" @input="touch" /></template></AdminField>
                <AdminField :label="$t('admin.Llm.currency')"><template #default="{ id }"><input :id="id" v-model="d.currency" maxlength="8" autocapitalize="characters" placeholder="USD" @input="touch" /></template></AdminField>
              </div>
            </details>
          </div>
        </li>
      </ul>
      <div><AdminButton :disabled="busy || drafts.length >= 20" @click="addProvider">{{ $t('admin.Llm.add') }}</AdminButton></div>

      <h3 class="llm-card__sub">{{ $t('admin.Llm.services') }}</h3>
      <AdminField :label="$t('admin.Llm.serviceTranslation')" :hint="$t('admin.Llm.serviceTranslationHint')">
        <template #default="{ id, describedBy }">
          <select :id="id" v-model="translationService" :aria-describedby="describedBy" @change="touch">
            <option value="">{{ $t('admin.Llm.serviceNone') }}</option>
            <option v-for="o in providerOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
          </select>
        </template>
      </AdminField>

      <h3 class="llm-card__sub">{{ $t('admin.Llm.translation') }}</h3>
      <AdminCheck v-model="tr.enabled" variant="switch" :label="$t('admin.Llm.translationEnabled')" :hint="$t('admin.Llm.translationEnabledHint')" @update:model-value="touch" />
      <AdminField :label="$t('admin.Llm.mode')" :hint="tr.mode === 'llm' ? $t('admin.Llm.modeLlmHint') : $t('admin.Llm.modeTraditionalHint', [loaded.translation.traditional_provider || '—'])">
        <template #default="{ id, describedBy }">
          <select :id="id" v-model="tr.mode" :aria-describedby="describedBy" @change="touch">
            <option value="traditional">{{ $t('admin.Llm.modeTraditional') }}</option>
            <option value="llm">{{ $t('admin.Llm.modeLlm') }}</option>
          </select>
        </template>
      </AdminField>
      <AdminAlert v-if="tr.mode === 'llm' && !translationService" tone="warning">{{ $t('admin.Llm.modeNeedsProvider') }}</AdminAlert>
      <div class="llm-provider__grid">
        <AdminField :label="$t('admin.Llm.userDailyLimit')" :hint="$t('admin.Llm.zeroUnlimited')"><template #default="{ id, describedBy }"><input :id="id" v-model.number="tr.user_daily_limit" type="number" min="0" max="1000000" inputmode="numeric" required :aria-describedby="describedBy" @input="touch" /></template></AdminField>
        <AdminField :label="$t('admin.Llm.dailyLimit')" :hint="$t('admin.Llm.zeroUnlimited')"><template #default="{ id, describedBy }"><input :id="id" v-model.number="tr.daily_limit" type="number" min="0" max="1000000" inputmode="numeric" required :aria-describedby="describedBy" @input="touch" /></template></AdminField>
        <AdminField :label="$t('admin.Llm.userPerMinute')" :hint="$t('admin.Llm.zeroUnlimited')"><template #default="{ id, describedBy }"><input :id="id" v-model.number="tr.user_per_minute" type="number" min="0" max="1000000" inputmode="numeric" required :aria-describedby="describedBy" @input="touch" /></template></AdminField>
      </div>
      <p class="llm-card__note">{{ $t('admin.Llm.cacheNote') }}</p>

      <div class="admin-form-actions">
        <AdminButton type="submit" variant="primary" :loading="busy" :disabled="!dirty">{{ busy ? $t('admin.Llm.saving') : $t('admin.Llm.save') }}</AdminButton>
        <AdminButton :disabled="busy" @click="load">{{ $t('admin.Llm.reset') }}</AdminButton>
      </div>
      <p v-if="dirty" class="llm-card__note" role="status">{{ $t('admin.Llm.testNeedsSave') }}</p>
    </form>

    <div v-if="loaded" class="llm-card__usage">
      <h3 class="llm-card__sub">{{ $t('admin.Llm.usage') }}</h3>
      <p class="llm-card__note">{{ $t('admin.Llm.usageToday', [usage?.today.translation_calls ?? 0, tr.daily_limit || '∞']) }}</p>
      <AdminEmpty v-if="!usage?.days.length">{{ $t('admin.Llm.usageEmpty') }}</AdminEmpty>
      <div v-else class="admin-table-wrap" role="region" :aria-label="$t('admin.Llm.usage')" tabindex="0">
        <table class="admin-table">
          <thead><tr><th>{{ $t('admin.Llm.day') }}</th><th>{{ $t('admin.Llm.calls') }}</th><th>{{ $t('admin.Llm.errors') }}</th><th>{{ $t('admin.Llm.inputTokens') }}</th><th>{{ $t('admin.Llm.outputTokens') }}</th><th>{{ $t('admin.Llm.cost') }}</th></tr></thead>
          <tbody><tr v-for="d in usage.days" :key="d.day"><td>{{ d.day }}</td><td>{{ fmt(d.calls) }}</td><td>{{ fmt(d.errors) }}</td><td>{{ fmt(d.input_tokens) }}</td><td>{{ fmt(d.output_tokens) }}</td><td>{{ formatCost(d.cost) }}</td></tr></tbody>
        </table>
      </div>
    </div>
  </section>
</template>

<style scoped>
.llm-card__heading h2 { margin: 0; }
.llm-card__heading p, .llm-card__note { margin: 6px 0 0; color: var(--color-label-secondary); font-size: 13px; line-height: 1.6; }
.llm-card__sub { margin: 18px 0 4px; font-size: 15px; }
.llm-card__list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 10px; }
.llm-provider { border: 1px solid var(--color-separator); border-radius: 10px; padding: 10px 12px; min-width: 0; }
.llm-provider__row { display: flex; gap: 10px; align-items: center; justify-content: space-between; flex-wrap: wrap; }
.llm-provider__main { display: flex; flex-direction: column; gap: 6px; min-width: 0; }
.llm-provider__meta { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; font-size: 12px; }
.llm-provider__meta code { overflow-wrap: anywhere; }
.llm-provider__actions { display: flex; gap: 6px; flex-wrap: wrap; }
.llm-provider__form { display: flex; flex-direction: column; gap: 12px; margin-top: 12px; }
.llm-provider__grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 12px; }
.llm-provider__advanced summary { cursor: pointer; color: var(--admin-accent); font-size: 13px; min-height: 32px; align-content: center; }
.llm-provider__test { margin: 8px 0 0; font-size: 13px; }
.llm-provider__test.is-ok { color: var(--color-success, #1a7f37); }
.llm-provider__test.is-error { color: var(--color-danger, #d33); }
.llm-card__usage { margin-top: 8px; }
@media (max-width: 760px) {
  .llm-provider__actions { width: 100%; }
}
</style>
