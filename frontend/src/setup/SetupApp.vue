<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { call, getToken, setToken, SetupApiError, type Inspection, type Job } from './api'
import { LOCALES, applyLocale, type Locale } from './i18n'
import SecretField from './SecretField.vue'
import { normalizeSetupDomain } from './validation'

const { t, te, locale } = useI18n()

type Step = 'language' | 'token' | 'mode' | 'source' | 'domain' | 'database' | 'redis' | 'admin' | 'review'
const step = ref<Step>('language')
const mode = ref<'fresh' | 'misskey' | 'mastodon'>('fresh')
const busy = ref(false)
const error = ref('')
const invalidField = ref('')
const okMessage = ref('')
const envFile = ref('/app/config/.env')
const token = ref(getToken())
const domain = ref('')
const inspection = ref<Inspection | null>(null)
const job = ref<Job | null>(null)
const phase = ref<'wizard' | 'progress' | 'restarting'>('wizard')
let poll: ReturnType<typeof setTimeout> | undefined
let active = true
let pollVersion = 0
const lifetime = new AbortController()
const form = ref<HTMLFormElement>()
const content = ref<HTMLElement>()
const pending = ref('')
const primaryBusy = computed(() => busy.value && ['verify', 'database', 'redis', 'install'].includes(pending.value))
const progressError = ref('')
const verified = ref(false)
const returningToReview = ref(false)
const visitedSteps = new Set<Step>()
watch(step, value => visitedSteps.add(value), { flush: 'sync' })
function request<T = Record<string, unknown>>(method: 'GET' | 'POST', path: string, body?: unknown) {
  return call<T>(method, path, body, lifetime.signal)
}

const db = reactive({ type: 'postgres', host: '127.0.0.1', port: 5432, name: 'community', user: 'community', password: '', sslmode: 'disable', path: '' })
const builtinDatabase = ref<{ type: 'postgres' | 'mysql' } | null>(null)
const builtinLabel = computed(() => `${t('db.builtin')} · ${builtinDatabase.value?.type === 'mysql' ? 'MySQL' : 'PostgreSQL'}`)
const redis = reactive({ address: '127.0.0.1:6379', password: '', db: 0 })
const admin = reactive({ username: '', email: '', password: '' })
const source = reactive({
  host: '127.0.0.1', port: 5432, name: 'misskey', user: 'misskey', password: '', sslmode: 'disable', id_method: '',
  media_source: 'local' as 'local' | 's3', files_path: '/misskey/files', media_mode: 'reference' as 'reference' | 'copy',
  object_storage: { endpoint: '', bucket: '', region: '', access_key: '', secret_key: '', public_url: '', path_style: true },
})
// Mastodon has no ID method and keeps its storage settings outside the database.
const mastodonSource = reactive({
  host: '127.0.0.1', port: 5432, name: 'mastodon_production', user: 'mastodon', password: '', sslmode: 'disable', id_method: '',
  media_source: 'local' as 'local' | 's3', files_path: '/mastodon/public/system', media_mode: 'reference' as 'reference' | 'copy',
  object_storage: { endpoint: '', bucket: '', region: '', access_key: '', secret_key: '', public_url: '', path_style: true },
})
const src = computed(() => mode.value === 'mastodon' ? mastodonSource : source)
const migrating = computed(() => mode.value !== 'fresh')
const tested = reactive({ database: false, redis: false, source: false })
watch(db, () => { tested.database = false; okMessage.value = '' }, { flush: 'sync' })
watch(redis, () => { tested.redis = false; okMessage.value = '' }, { flush: 'sync' })
watch(() => [mode.value, src.value.host, src.value.port, src.value.name, src.value.user, src.value.password, src.value.sslmode], () => {
  tested.source = false
  inspection.value = null
  domain.value = ''
  src.value.id_method = ''
  okMessage.value = ''
}, { flush: 'sync' })
watch(token, () => { verified.value = false })
watch(() => [src.value.media_source, src.value.media_mode, src.value.object_storage], () => { okMessage.value = '' }, { deep: true })
watch([step, phase], async () => {
  await nextTick()
  const heading = content.value?.querySelector<HTMLElement>('h2')
  heading?.focus({ preventScroll: true })
  content.value?.scrollIntoView?.({ block: 'start', behavior: 'instant' })
})
const completedStages = computed(() => job.value?.stages.filter(item => ['done', 'skipped'].includes(item.status)).length || 0)
const primaryLabel = computed(() => {
  if (primaryBusy.value) return t(pending.value === 'verify' ? 'common.verifying' : pending.value === 'install' ? 'common.starting' : 'common.testing')
  if (step.value === 'token') return t('token.verify')
  if ((step.value === 'database' && !tested.database) || (step.value === 'redis' && !tested.redis)) return t('common.testContinue')
  if (step.value === 'review') return t(migrating.value ? 'review.migrate' : 'review.install')
  if (returningToReview.value) return t('common.reviewBack')
  return t('common.next')
})
const primaryDisabled = computed(() => busy.value || step.value === 'token' && !token.value.trim() || step.value === 'source' && !tested.source)

const steps = computed<Step[]>(() => migrating.value
  ? ['language', 'token', 'mode', 'source', 'domain', 'database', 'redis', 'admin', 'review']
  : ['language', 'token', 'mode', 'domain', 'database', 'redis', 'admin', 'review'])
const stepIndex = computed(() => steps.value.indexOf(step.value))
const domainLocked = computed(() => migrating.value && !!inspection.value?.detected_base_url)

function errorText(e: unknown): string {
  const code = e instanceof SetupApiError ? e.code : 'unknown'
  return te(`errors.${code}`) ? t(`errors.${code}`) : t('errors.unknown')
}
function errorDetail(e: unknown): string {
  return e instanceof SetupApiError && e.detail ? e.detail : ''
}
async function guarded(action: string, fn: () => Promise<void>) {
  if (busy.value || !active) return
  busy.value = true
  pending.value = action
  error.value = ''
  okMessage.value = ''
  try {
    await fn()
  } catch (e) {
    if (!active) return
    const detail = errorDetail(e)
    error.value = errorText(e) + (detail ? ` (${detail})` : '')
    await nextTick()
    if (active) content.value?.querySelector('.notice--error')?.scrollIntoView?.({ block: 'nearest' })
  } finally {
    busy.value = false
    pending.value = ''
  }
}

function pickLocale(code: Locale) { applyLocale(code) }
function stepLabel(s: Step) { return s === 'source' && mode.value === 'mastodon' ? t('mastodon.step') : t(`steps.${s}`) }

function go(delta: number) {
  error.value = ''
  okMessage.value = ''
  if (delta < 0) returningToReview.value = false
  const next = steps.value[stepIndex.value + delta]
  if (next) step.value = delta > 0 && next === 'token' && verified.value ? 'mode' : next
}
function advance() {
  if (returningToReview.value) { returningToReview.value = false; step.value = 'review' }
  else go(1)
}

function databaseBody() {
  if (db.type === 'builtin') return { type: 'builtin' }
  return db.type === 'sqlite' ? { type: 'sqlite', path: db.path } : { ...db, port: Number(db.port) }
}
function misskeyBody() {
  return { ...source, port: Number(source.port), object_storage: { ...source.object_storage } }
}
function mastodonBody() {
  const { host, port, name, user, password, sslmode, media_source, files_path, media_mode, object_storage } = mastodonSource
  return { host, port: Number(port), name, user, password, sslmode, media_source, files_path, media_mode, object_storage: { ...object_storage } }
}
function modeLabel() { return t(`mode.${mode.value}`) }
function pickMode() {
  // A different source invalidates the inspection and the detected domain.
  inspection.value = null
  tested.source = false
  domain.value = ''
}

const verifyToken = () => guarded('verify', async () => {
  setToken(token.value.trim())
  await request('POST', 'verify')
  if (!active) return
  verified.value = true
  const status = await request<{ job?: { status: string } }>('GET', 'status')
  if (!active) return
  if (status.job) {
    phase.value = 'progress'
    startPolling()
    return
  }
  go(1)
})
const testDatabase = () => guarded('database', async () => {
  await request('POST', 'test/database', databaseBody())
  if (!active) return
  tested.database = true
  advance()
})
const testRedis = () => guarded('redis', async () => {
  await request('POST', 'test/redis', { ...redis, db: Number(redis.db) })
  if (!active) return
  tested.redis = true
  advance()
})
const inspectSource = () => guarded('source', async () => {
  if (mode.value === 'mastodon') {
    const res = await request<{ inspection: Inspection }>('POST', 'test/mastodon', mastodonBody())
    if (!active) return
    inspection.value = res.inspection
    if (res.inspection.detected_base_url) domain.value = res.inspection.detected_base_url
    tested.source = true
    return
  }
  const res = await request<{ inspection: Inspection }>('POST', 'test/misskey', misskeyBody())
  if (!active) return
  inspection.value = res.inspection
  source.id_method = res.inspection.id_method
  if (res.inspection.detected_base_url) domain.value = res.inspection.detected_base_url
  if (res.inspection.uses_object_storage && res.inspection.object_storage) {
    source.media_source = 's3'
    Object.assign(source.object_storage, res.inspection.object_storage, { secret_key: '' })
  }
  tested.source = true
})
const testStorage = () => guarded('storage', async () => {
  // Mastodon's secret is never stored in its database: test with the typed key only.
  const body = mode.value === 'mastodon' ? { misskey: { object_storage: { ...mastodonSource.object_storage } } } : { misskey: misskeyBody() }
  await request('POST', 'test/object-storage', body)
  okMessage.value = t('common.ok')
})
const install = () => guarded('install', async () => {
  const body: Record<string, unknown> = {
    locale: locale.value, mode: mode.value, domain: domain.value.trim(),
    database: databaseBody(), redis: { ...redis, db: Number(redis.db) }, admin: { ...admin },
  }
  if (mode.value === 'misskey') body.misskey = misskeyBody()
  if (mode.value === 'mastodon') body.mastodon = mastodonBody()
  job.value = await request<Job>('POST', 'install', body)
  if (!active) return
  phase.value = 'progress'
  startPolling()
})
const retry = () => guarded('retry', async () => {
  job.value = await request<Job>('POST', 'job/retry')
  startPolling()
})

function startPolling() {
  if (!active) return
  clearTimeout(poll)
  const version = ++pollVersion
  progressError.value = ''
  const tick = async () => {
    try {
      const result = await request<Job>('GET', 'job')
      if (!active || version !== pollVersion) return
      job.value = result
      progressError.value = ''
      if (result.status === 'completed') return waitForRestart()
      if (result.status === 'failed') return
    } catch (e) {
      if (!active || version !== pollVersion) return
      // An absent job is different from setup routes disappearing after restart.
      if (e instanceof SetupApiError && e.status === 404 && e.code === 'not_found' && job.value?.status !== 'failed') return waitForRestart()
      progressError.value = errorText(e)
      // Authentication errors need the token entry instead of silent polling.
      if (e instanceof SetupApiError && [401, 403].includes(e.status)) { verified.value = false; return }
    }
    if (active && version === pollVersion) poll = setTimeout(tick, progressError.value ? 4000 : 1500)
  }
  void tick()
}
function waitForRestart() {
  if (!active) return
  clearTimeout(poll)
  phase.value = 'restarting'
  const version = ++pollVersion
  const check = async () => {
    try {
      const res = await fetch('/api/v1/instance', { cache: 'no-store', signal: lifetime.signal })
      if (!active || version !== pollVersion) return
      if (res.ok) { location.href = '/login'; return }
    } catch { /* still restarting */ }
    if (active && version === pollVersion) poll = setTimeout(check, 1500)
  }
  void check()
}
function reverify() { phase.value = 'wizard'; step.value = 'token'; progressError.value = '' }
function editStep(target: Step) {
  if (busy.value) return
  returningToReview.value = step.value === 'review' && !['mode', 'source'].includes(target)
  error.value = ''; okMessage.value = ''; step.value = target
}
function fieldError(code: string, field: string) {
  invalidField.value = field
  error.value = t(`errors.${code}`)
  const input = form.value?.querySelector<HTMLInputElement>(`[data-field="${field}"]`)
  input?.focus()
}
function submitStep() {
  if (busy.value || !active || primaryDisabled.value) return
  error.value = ''
  invalidField.value = ''
  if (!form.value?.reportValidity()) return
  if (step.value === 'token') return void verifyToken()
  if (step.value === 'database' && !tested.database) return void testDatabase()
  if (step.value === 'redis' && !tested.redis) return void testRedis()
  if (step.value === 'domain') {
    const normalized = normalizeSetupDomain(domain.value)
    if (!normalized) return fieldError('invalid_domain', 'domain')
    domain.value = normalized
  }
  if (step.value === 'admin') {
    admin.username = admin.username.trim(); admin.email = admin.email.trim()
    if (!/^[a-zA-Z0-9_]{1,30}$/.test(admin.username) || /^_ap_|_ap$/i.test(admin.username)) return fieldError('invalid_username', 'username')
    if (!/^[^\s@]+@[^\s@]+$/.test(admin.email)) return fieldError('invalid_email', 'email')
    if (new TextEncoder().encode(admin.password).length < 8) return fieldError('weak_password', 'password')
  }
  if (step.value === 'review') {
    if (!tested.database) return editStep('database')
    if (!tested.redis) return editStep('redis')
    if (migrating.value && !tested.source) return editStep('source')
    return void install()
  }
  advance()
}

function stageText(name: string) { return te(`stages.${name}`) ? t(`stages.${name}`) : name }
function formatBytes(n: number) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++ }
  return `${n.toFixed(i ? 1 : 0)} ${units[i]}`
}
function countEntries(counts?: Record<string, number>) {
  if (!counts) return []
  const out: { label: string; value: string }[] = []
  for (const [key, value] of Object.entries(counts)) {
    if (key === 'bytes_done' || key === 'bytes_total' || key === 'files_total') continue
    let label = key
    for (const prefix of ['skipped', 'archived']) {
      if (key.startsWith(prefix + '_')) {
        const base = key.slice(prefix.length + 1)
        label = `${te(`counts.${base}`) ? t(`counts.${base}`) : base} (${t(`counts.prefix_${prefix}`)})`
      }
    }
    if (label === key && te(`counts.${key}`)) label = t(`counts.${key}`)
    out.push({ label, value: value.toLocaleString(locale.value) })
  }
  return out
}
function progressOf(counts?: Record<string, number>) {
  if (!counts?.files_total) return null
  const files = (counts.files_done || 0) + (counts.files_skipped || 0) + (counts.files_missing || 0)
  return {
    percent: Math.min(100, Math.round((files / counts.files_total) * 100)),
    files: t('progress.bytes', { done: files.toLocaleString(locale.value), total: counts.files_total.toLocaleString(locale.value) }),
    bytes: counts.bytes_total ? t('progress.bytes', { done: formatBytes(counts.bytes_done || 0), total: formatBytes(counts.bytes_total) }) : '',
  }
}
function stageError(name: string, err?: { error_code: string; detail?: string }) {
  if (!err) return ''
  const text = te(`errors.${err.error_code}`) ? t(`errors.${err.error_code}`) : t('errors.stage_failed')
  return `${stageText(name)}: ${text}${err.detail ? ` (${err.detail})` : ''}`
}

onMounted(async () => {
  try {
    const status = await request<{ env_file: string; defaults: Record<string, string>; builtin_database?: { type: 'postgres' | 'mysql' }; job?: { status: string } }>('GET', 'status')
    if (!active) return
    envFile.value = status.env_file
    const d = status.defaults || {}
    builtinDatabase.value = status.builtin_database || null
    // A slow status response must not overwrite a connection being edited
    // or invalidate a connection test already in flight.
    if (!visitedSteps.has('database')) {
      db.host = d.database_host || db.host
      db.name = d.database_name || db.name
      db.user = d.database_user || db.user
      db.path = d.sqlite_path || db.path
      if (builtinDatabase.value) db.type = 'builtin'
    }
    if (!visitedSteps.has('redis')) redis.address = d.redis_address || redis.address
    if (status.job && step.value === 'language') {
      step.value = 'token'
      // A saved token is verified again; public status alone never grants access.
      if (token.value.trim()) await verifyToken()
    }
  } catch (e) {
    if (active && e instanceof SetupApiError && e.status === 404) location.href = '/login'
  }
})
onBeforeUnmount(() => { active = false; pollVersion++; clearTimeout(poll); lifetime.abort() })
</script>

<template>
  <main class="setup">
    <header class="setup__header">
      <img src="/logo.svg" alt="" class="setup__logo" width="40" height="40">
      <div>
        <h1>{{ t('title') }}</h1>
        <p class="muted">{{ t('subtitle') }}</p>
      </div>
      <select v-if="step !== 'language' || phase !== 'wizard'" class="setup__locale" :value="locale" :aria-label="t('language.label')" :disabled="busy" @change="pickLocale(($event.target as HTMLSelectElement).value as Locale)">
        <option v-for="l in LOCALES" :key="l.code" :value="l.code" :lang="l.code">{{ l.name }}</option>
      </select>
    </header>

    <section v-if="phase === 'wizard'" class="wizard">
      <aside class="wizard__nav">
      <p class="steps-compact" aria-live="polite">
        <span class="steps-compact__text">{{ t('stepOf', { n: stepIndex + 1, total: steps.length }) }} · <strong>{{ stepLabel(step) }}</strong></span>
        <span class="steps-compact__bar"><span :style="{ width: `${((stepIndex + 1) / steps.length) * 100}%` }" /></span>
      </p>
      <ol class="steps" :aria-label="t('title')">
        <li v-for="(s, i) in steps" :key="s" :class="{ active: s === step, done: i < stepIndex }" :aria-current="s === step ? 'step' : undefined">
          <span class="steps__dot" aria-hidden="true">{{ i < stepIndex ? '✓' : i + 1 }}</span><span class="steps__label">{{ stepLabel(s) }}</span>
        </li>
      </ol>
      </aside>
      <div ref="content" class="card wizard__content">
      <form ref="form" class="form" :aria-busy="busy" @submit.prevent="submitStep" @input="error = ''; invalidField = ''">
        <fieldset class="form__fields" :disabled="busy">
        <template v-if="step === 'language'">
          <h2 tabindex="-1">{{ t('language.title') }}</h2>
          <p class="muted">{{ t('language.hint') }}</p>
          <label class="field"><span>{{ t('language.label') }}</span>
            <select class="select" :value="locale" @change="pickLocale(($event.target as HTMLSelectElement).value as Locale)">
              <option v-for="l in LOCALES" :key="l.code" :value="l.code" :lang="l.code">{{ l.name }}</option>
            </select>
          </label>
        </template>

        <template v-else-if="step === 'token'">
          <h2 tabindex="-1">{{ t('token.title') }}</h2>
          <p class="muted">{{ t('token.hint') }}</p>
          <label class="field"><span>{{ t('token.label') }}</span>
            <input v-model="token" type="password" autocomplete="one-time-code" autocapitalize="off" spellcheck="false" required>
          </label>
        </template>

        <template v-else-if="step === 'mode'">
          <h2 tabindex="-1">{{ t('mode.title') }}</h2>
          <div class="choices mode-choices" role="radiogroup" :aria-label="t('mode.title')">
            <label class="choice choice--wide" :class="{ selected: mode === 'fresh' }">
              <input v-model="mode" type="radio" name="install-mode" value="fresh" @change="pickMode"><strong>{{ t('mode.fresh') }}</strong><small>{{ t('mode.freshHint') }}</small>
            </label>
            <label class="choice choice--wide" :class="{ selected: mode === 'misskey' }">
              <input v-model="mode" type="radio" name="install-mode" value="misskey" @change="pickMode"><strong>{{ t('mode.misskey') }}</strong><small>{{ t('mode.misskeyHint') }}</small>
            </label>
            <label class="choice choice--wide" :class="{ selected: mode === 'mastodon' }">
              <input v-model="mode" type="radio" name="install-mode" value="mastodon" @change="pickMode"><strong>{{ t('mode.mastodon') }}</strong><small>{{ t('mode.mastodonHint') }}</small>
            </label>
          </div>
        </template>

        <template v-else-if="step === 'source'">
          <h2 tabindex="-1">{{ mode === 'mastodon' ? t('mastodon.title') : t('source.title') }}</h2>
          <p class="muted">{{ mode === 'mastodon' ? t('mastodon.hint') : t('source.hint') }}</p>
          <div class="grid">
            <label class="field"><span>{{ t('source.host') }}</span><input v-model="src.host" required autocapitalize="off" spellcheck="false"></label>
            <label class="field"><span>{{ t('source.port') }}</span><input v-model.number="src.port" type="number" min="1" max="65535" step="1" inputmode="numeric" required></label>
            <label class="field"><span>{{ t('source.name') }}</span><input v-model="src.name" required autocapitalize="off"></label>
            <label class="field"><span>{{ t('source.user') }}</span><input v-model="src.user" required autocapitalize="off"></label>
            <SecretField v-model="src.password" :label="t('source.password')" />
            <label class="field"><span>{{ t('source.sslmode') }}</span>
              <select v-model="src.sslmode"><option>disable</option><option>prefer</option><option>require</option><option>verify-full</option></select>
            </label>
          </div>
          <button type="button" class="btn" :disabled="busy" @click="form?.reportValidity() && inspectSource()"><span v-if="pending === 'source'" class="spinner" aria-hidden="true" />{{ pending === 'source' ? t('common.testing') : t('source.inspect') }}</button>
          <p v-if="inspection && mode === 'mastodon'" class="notice notice--ok">{{ t('mastodon.found', { users: inspection.counts.local_users ?? 0, remote: inspection.counts.remote_users ?? 0, notes: inspection.counts.notes ?? 0, files: inspection.counts.files ?? 0 }) }}</p>
          <p v-else-if="inspection" class="notice notice--ok">{{ t('source.found', { users: inspection.counts.users ?? 0, notes: inspection.counts.notes ?? 0, files: inspection.counts.files ?? inspection.counts.drive_files ?? 0 }) }}</p>

          <template v-if="tested.source">
            <h3>{{ t('source.mediaTitle') }}</h3>
            <div class="choices">
              <label class="choice" :class="{ selected: src.media_source === 'local' }"><input v-model="src.media_source" type="radio" name="media-source" value="local">{{ t('source.mediaLocal') }}</label>
              <label class="choice" :class="{ selected: src.media_source === 's3' }"><input v-model="src.media_source" type="radio" name="media-source" value="s3">{{ t('source.mediaS3') }}</label>
            </div>
            <label v-if="src.media_source === 'local'" class="field"><span>{{ mode === 'mastodon' ? t('mastodon.filesPath') : t('source.filesPath') }}</span>
              <input v-model="src.files_path" required autocapitalize="off" spellcheck="false"><small>{{ mode === 'mastodon' ? t('mastodon.filesHint') : t('source.filesHint') }}</small>
            </label>
            <template v-else>
              <p v-if="mode === 'mastodon'" class="muted">{{ t('mastodon.storageHint') }}</p>
              <p v-else-if="inspection?.object_storage" class="muted">{{ t('source.prefilled') }}</p>
              <div class="choices">
                <label class="choice choice--wide" :class="{ selected: src.media_mode === 'reference' }"><input v-model="src.media_mode" type="radio" name="media-mode" value="reference"><strong>{{ t('source.modeReference') }}</strong><small>{{ t('source.modeReferenceHint') }}</small></label>
                <label class="choice choice--wide" :class="{ selected: src.media_mode === 'copy' }"><input v-model="src.media_mode" type="radio" name="media-mode" value="copy"><strong>{{ t('source.modeCopy') }}</strong><small>{{ t('source.modeCopyHint') }}</small></label>
              </div>
              <div class="grid">
                <label class="field field--wide"><span>{{ t('source.endpoint') }}</span><input v-model="src.object_storage.endpoint" autocapitalize="off" spellcheck="false"></label>
                <label class="field"><span>{{ t('source.bucket') }}</span><input v-model="src.object_storage.bucket" autocapitalize="off"></label>
                <label class="field"><span>{{ t('source.region') }}</span><input v-model="src.object_storage.region" autocapitalize="off"></label>
                <label class="field"><span>{{ t('source.accessKey') }}</span><input v-model="src.object_storage.access_key" autocapitalize="off" spellcheck="false"></label>
                <SecretField v-model="src.object_storage.secret_key" :label="t('source.secretKey')" :hint="inspection?.has_object_secret ? t('source.secretKept') : undefined" />
                <label class="field field--wide"><span>{{ t('source.publicUrl') }}</span><input v-model="src.object_storage.public_url" autocapitalize="off" spellcheck="false"></label>
                <label class="check"><input v-model="src.object_storage.path_style" type="checkbox">{{ t('source.pathStyle') }}</label>
              </div>
              <button type="button" class="btn" :disabled="busy" @click="testStorage"><span v-if="pending === 'storage'" class="spinner" aria-hidden="true" />{{ pending === 'storage' ? t('common.testing') : t('source.testStorage') }}</button>
            </template>
          </template>
        </template>

        <template v-else-if="step === 'domain'">
          <h2 tabindex="-1">{{ t('domain.title') }}</h2>
          <p class="muted">{{ t('domain.hint') }}</p>
          <label class="field"><span>{{ t('domain.label') }}</span>
            <input v-model="domain" type="text" data-field="domain" :aria-invalid="invalidField === 'domain'" :aria-describedby="invalidField === 'domain' ? 'setup-error' : undefined" inputmode="url" autocapitalize="off" spellcheck="false" placeholder="https://social.example.com" :readonly="domainLocked" :aria-readonly="domainLocked" required>
          </label>
          <p v-if="domainLocked" class="notice"><strong>{{ mode === 'mastodon' ? t('mastodon.detected') : t('domain.detected') }}</strong> — {{ t('domain.detectedHint') }}</p>
          <p v-else-if="mode === 'misskey'" class="notice notice--warn">{{ t('domain.notDetected') }}</p>
          <p v-else-if="mode === 'mastodon'" class="notice notice--warn">{{ t('mastodon.notDetected') }}</p>
          <p class="notice notice--warn">{{ t('domain.permanent') }}</p>
        </template>

        <template v-else-if="step === 'database'">
          <h2 tabindex="-1">{{ t('db.title') }}</h2>
          <p class="muted">{{ t('db.emptyHint') }}</p>
          <label class="field"><span>{{ t('db.type') }}</span>
            <select v-model="db.type" @change="db.port = db.type === 'mysql' ? 3306 : 5432">
              <option v-if="builtinDatabase" value="builtin">{{ builtinLabel }}</option>
              <option value="postgres">PostgreSQL</option><option value="mysql">MySQL / MariaDB</option><option value="sqlite">SQLite</option>
            </select>
          </label>
          <p v-if="db.type === 'builtin'" class="notice">{{ t('db.builtinHint') }}</p>
          <template v-else-if="db.type === 'sqlite'">
            <label class="field"><span>{{ t('db.path') }}</span><input v-model="db.path" required autocapitalize="off" spellcheck="false"><small>{{ t('db.sqliteHint') }}</small></label>
          </template>
          <div v-else class="grid">
            <label class="field"><span>{{ t('db.host') }}</span><input v-model="db.host" required autocapitalize="off" spellcheck="false"></label>
            <label class="field"><span>{{ t('db.port') }}</span><input v-model.number="db.port" type="number" min="1" max="65535" step="1" inputmode="numeric" required></label>
            <label class="field"><span>{{ t('db.name') }}</span><input v-model="db.name" required autocapitalize="off"></label>
            <label class="field"><span>{{ t('db.user') }}</span><input v-model="db.user" required autocapitalize="off"></label>
            <SecretField v-model="db.password" :label="t('db.password')" />
            <label v-if="db.type === 'postgres'" class="field"><span>{{ t('db.sslmode') }}</span>
              <select v-model="db.sslmode"><option>disable</option><option>prefer</option><option>require</option><option>verify-full</option></select>
            </label>
          </div>
        </template>

        <template v-else-if="step === 'redis'">
          <h2 tabindex="-1">{{ t('redis.title') }}</h2>
          <div class="grid">
            <label class="field field--wide"><span>{{ t('redis.address') }}</span><input v-model="redis.address" required autocapitalize="off" spellcheck="false"></label>
            <SecretField v-model="redis.password" :label="t('redis.password')" />
            <label class="field"><span>{{ t('redis.db') }}</span><input v-model.number="redis.db" type="number" min="0" step="1" inputmode="numeric" required></label>
          </div>
        </template>

        <template v-else-if="step === 'admin'">
          <h2 tabindex="-1">{{ t('admin.title') }}</h2>
          <p class="muted">{{ mode === 'mastodon' ? t('mastodon.adminHint') : mode === 'misskey' ? t('admin.hintMisskey') : t('admin.hintFresh') }}</p>
          <label class="field"><span>{{ t('admin.username') }}</span><input v-model="admin.username" data-field="username" :aria-invalid="invalidField === 'username'" :aria-describedby="invalidField === 'username' ? 'setup-error' : undefined" required maxlength="30" autocomplete="username" autocapitalize="off" spellcheck="false"></label>
          <label class="field"><span>{{ t('admin.email') }}</span><input v-model="admin.email" data-field="email" required type="email" autocomplete="email"></label>
          <SecretField v-model="admin.password" :label="t('admin.password')" autocomplete="new-password" required field="password" :invalid="invalidField === 'password'" :hint="t(migrating ? 'admin.passwordHintMigration' : 'admin.passwordHintFresh')" />
        </template>

        <template v-else-if="step === 'review'">
          <h2 tabindex="-1">{{ t('review.title') }}</h2>
          <dl class="summary">
            <div class="summary__row"><dt>{{ t('steps.language') }}</dt><dd>{{ LOCALES.find(l => l.code === locale)?.name }}</dd><dd><button type="button" class="btn btn--ghost btn--edit" :aria-label="`${t('common.edit')} · ${t('steps.language')}`" @click="editStep('language')">{{ t('common.edit') }}</button></dd></div>
            <div class="summary__row"><dt>{{ t('steps.mode') }}</dt><dd>{{ modeLabel() }}</dd><dd><button type="button" class="btn btn--ghost btn--edit" :aria-label="`${t('common.edit')} · ${t('steps.mode')}`" @click="editStep('mode')">{{ t('common.edit') }}</button></dd></div>
            <div class="summary__row"><dt>{{ t('steps.domain') }}</dt><dd>{{ domain }}</dd><dd><button type="button" class="btn btn--ghost btn--edit" :aria-label="`${t('common.edit')} · ${t('steps.domain')}`" @click="editStep('domain')">{{ t('common.edit') }}</button></dd></div>
            <div class="summary__row"><dt>{{ t('steps.database') }}</dt><dd>{{ db.type === 'builtin' ? builtinLabel : db.type === 'sqlite' ? db.path : `${db.type} · ${db.host}:${db.port} / ${db.name}` }}</dd><dd><button type="button" class="btn btn--ghost btn--edit" :aria-label="`${t('common.edit')} · ${t('steps.database')}`" @click="editStep('database')">{{ t('common.edit') }}</button></dd></div>
            <div class="summary__row"><dt>{{ t('steps.redis') }}</dt><dd>{{ redis.address }}</dd><dd><button type="button" class="btn btn--ghost btn--edit" :aria-label="`${t('common.edit')} · ${t('steps.redis')}`" @click="editStep('redis')">{{ t('common.edit') }}</button></dd></div>
            <div class="summary__row"><dt>{{ t('steps.admin') }}</dt><dd>{{ `${admin.username} · ${admin.email}` }}</dd><dd><button type="button" class="btn btn--ghost btn--edit" :aria-label="`${t('common.edit')} · ${t('steps.admin')}`" @click="editStep('admin')">{{ t('common.edit') }}</button></dd></div>
            <div v-if="migrating" class="summary__row"><dt>{{ stepLabel('source') }}</dt><dd>{{ src.name }} · {{ src.media_source === 'local' ? src.files_path : src.object_storage.bucket }}</dd><dd><button type="button" class="btn btn--ghost btn--edit" :aria-label="`${t('common.edit')} · ${stepLabel('source')}`" @click="editStep('source')">{{ t('common.edit') }}</button></dd></div>
          </dl>
          <p class="muted">{{ t('review.hint', { path: envFile }) }}</p>
        </template>
        </fieldset>
        <p v-if="error" id="setup-error" class="notice notice--error" role="alert">{{ error }}</p>
        <p v-if="okMessage" class="notice notice--ok" role="status">{{ okMessage }}</p>

        <div class="actions">
          <button v-if="stepIndex > 0" type="button" class="btn btn--ghost" :disabled="busy" @click="go(-1)"><span aria-hidden="true">←</span>{{ t('common.back') }}</button>
          <span class="spacer" />
          <button type="submit" class="btn btn--primary" :disabled="primaryDisabled"><span v-if="primaryBusy" class="spinner" aria-hidden="true" />{{ primaryLabel }}<span v-if="!primaryBusy" aria-hidden="true">→</span></button>
        </div>
      </form>
      </div>
    </section>

    <section v-else ref="content" class="card progress-card">
      <div class="progress-heading"><span v-if="phase === 'restarting'" class="progress-heading__symbol" aria-hidden="true">✓</span><span v-else-if="job?.status !== 'failed'" class="spinner" aria-hidden="true" /><h2 tabindex="-1">{{ t(phase === 'restarting' ? 'progress.finishedTitle' : job?.status === 'failed' ? 'progress.failedTitle' : 'progress.title') }}</h2></div>
      <p v-if="phase === 'restarting'" class="notice notice--ok" role="status">{{ t('progress.completed') }}</p>
      <p v-else-if="job?.status === 'failed'" class="notice notice--error" role="alert">{{ t('progress.failed') }}</p>
      <p v-else class="muted" role="status">{{ t('progress.running') }}</p>
      <div v-if="progressError" class="notice notice--error" role="alert">{{ progressError }}<span v-if="verified"> {{ t('progress.reconnecting') }}</span><button v-else type="button" class="btn" @click="reverify">{{ t('progress.verifyAgain') }}</button></div>
      <p v-if="job?.stages.length" class="progress-count">{{ t('progress.stageCount', { done: completedStages, total: job.stages.length }) }}</p>
      <ul class="stages">
        <li v-for="s in job?.stages || []" :key="s.name" :class="`stage stage--${s.status}`">
          <div class="stage__head">
            <span class="stage__icon" aria-hidden="true" />
            <span class="stage__name">{{ stageText(s.name) }}</span>
            <span class="stage__status">{{ te(`status.${s.status}`) ? t(`status.${s.status}`) : s.status }}</span>
          </div>
          <div v-if="progressOf(s.counts)" class="stage__progress">
            <progress :aria-label="stageText(s.name)" :value="progressOf(s.counts)!.percent" max="100" />
            <small>{{ progressOf(s.counts)!.files }} <template v-if="progressOf(s.counts)!.bytes">· {{ progressOf(s.counts)!.bytes }}</template></small>
          </div>
          <ul v-if="countEntries(s.counts).length" class="stage__counts">
            <li v-for="c in countEntries(s.counts)" :key="c.label"><b>{{ c.value }}</b> {{ c.label }}</li>
          </ul>
          <p v-if="s.error" class="stage__error">{{ stageError(s.name, s.error) }}</p>
        </li>
      </ul>
      <p v-if="error" class="notice notice--error" role="alert">{{ error }}</p>
      <div v-if="job?.status === 'failed'" class="actions">
        <span class="spacer" />
        <button type="button" class="btn btn--primary" :disabled="busy" @click="retry"><span v-if="busy" class="spinner" aria-hidden="true" />{{ busy ? t('common.starting') : t('progress.retry') }}</button>
      </div>
      <details v-if="job?.log?.length" class="log" :open="job.status === 'failed'">
        <summary>{{ t('progress.log') }}</summary>
        <pre><template v-for="(l, i) in job.log.slice(-200)" :key="i"><span :class="`log--${l.level}`">{{ new Date(l.time).toLocaleTimeString(locale) }} {{ l.message }}</span>
</template></pre>
      </details>
    </section>
  </main>
</template>

<style src="./setup.css"></style>
