<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { call, getToken, setToken, SetupApiError, type Inspection, type Job } from './api'
import { LOCALES, applyLocale, type Locale } from './i18n'

const { t, te, locale } = useI18n()

type Step = 'language' | 'token' | 'mode' | 'source' | 'domain' | 'database' | 'redis' | 'admin' | 'review'
const step = ref<Step>('language')
const mode = ref<'fresh' | 'misskey'>('fresh')
const busy = ref(false)
const error = ref('')
const okMessage = ref('')
const envFile = ref('/app/config/.env')
const token = ref(getToken())
const domain = ref('')
const inspection = ref<Inspection | null>(null)
const job = ref<Job | null>(null)
const phase = ref<'wizard' | 'progress' | 'restarting'>('wizard')
let poll: ReturnType<typeof setTimeout> | undefined

const db = reactive({ type: 'postgres', host: '127.0.0.1', port: 5432, name: 'community', user: 'community', password: '', sslmode: 'disable', path: '' })
const redis = reactive({ address: '127.0.0.1:6379', password: '', db: 0 })
const admin = reactive({ username: '', email: '', password: '' })
const source = reactive({
  host: '127.0.0.1', port: 5432, name: 'misskey', user: 'misskey', password: '', sslmode: 'disable', id_method: '',
  media_source: 'local' as 'local' | 's3', files_path: '/misskey/files', media_mode: 'reference' as 'reference' | 'copy',
  object_storage: { endpoint: '', bucket: '', region: '', access_key: '', secret_key: '', public_url: '', path_style: true },
})
const tested = reactive({ database: false, redis: false, source: false })

const steps = computed<Step[]>(() => mode.value === 'misskey'
  ? ['language', 'token', 'mode', 'source', 'domain', 'database', 'redis', 'admin', 'review']
  : ['language', 'token', 'mode', 'domain', 'database', 'redis', 'admin', 'review'])
const stepIndex = computed(() => steps.value.indexOf(step.value))
const domainLocked = computed(() => mode.value === 'misskey' && !!inspection.value?.detected_base_url)

function errorText(e: unknown): string {
  const code = e instanceof SetupApiError ? e.code : 'unknown'
  return te(`errors.${code}`) ? t(`errors.${code}`) : t('errors.unknown')
}
function errorDetail(e: unknown): string {
  return e instanceof SetupApiError && e.detail ? e.detail : ''
}
async function guarded(fn: () => Promise<void>) {
  busy.value = true
  error.value = ''
  okMessage.value = ''
  try {
    await fn()
  } catch (e) {
    const detail = errorDetail(e)
    error.value = errorText(e) + (detail ? ` (${detail})` : '')
  } finally {
    busy.value = false
  }
}

function pickLocale(code: Locale) { applyLocale(code) }

function go(delta: number) {
  error.value = ''
  okMessage.value = ''
  const next = steps.value[stepIndex.value + delta]
  if (next) step.value = next
}

function databaseBody() {
  return db.type === 'sqlite' ? { type: 'sqlite', path: db.path } : { ...db, port: Number(db.port) }
}
function misskeyBody() {
  return { ...source, port: Number(source.port), object_storage: { ...source.object_storage } }
}

const verifyToken = () => guarded(async () => {
  setToken(token.value.trim())
  await call('POST', 'verify')
  const status = await call<{ job?: { status: string } }>('GET', 'status')
  if (status.job) {
    phase.value = 'progress'
    startPolling()
    return
  }
  go(1)
})
const testDatabase = () => guarded(async () => {
  await call('POST', 'test/database', databaseBody())
  tested.database = true
  okMessage.value = t('common.ok')
})
const testRedis = () => guarded(async () => {
  await call('POST', 'test/redis', { ...redis, db: Number(redis.db) })
  tested.redis = true
  okMessage.value = t('common.ok')
})
const inspectSource = () => guarded(async () => {
  const res = await call<{ inspection: Inspection }>('POST', 'test/misskey', misskeyBody())
  inspection.value = res.inspection
  source.id_method = res.inspection.id_method
  if (res.inspection.detected_base_url) domain.value = res.inspection.detected_base_url
  if (res.inspection.uses_object_storage && res.inspection.object_storage) {
    source.media_source = 's3'
    Object.assign(source.object_storage, res.inspection.object_storage, { secret_key: '' })
  }
  tested.source = true
})
const testStorage = () => guarded(async () => {
  await call('POST', 'test/object-storage', { misskey: misskeyBody() })
  okMessage.value = t('common.ok')
})
const install = () => guarded(async () => {
  const body: Record<string, unknown> = {
    locale: locale.value, mode: mode.value, domain: domain.value.trim(),
    database: databaseBody(), redis: { ...redis, db: Number(redis.db) }, admin: { ...admin },
  }
  if (mode.value === 'misskey') body.misskey = misskeyBody()
  job.value = await call<Job>('POST', 'install', body)
  phase.value = 'progress'
  startPolling()
})
const retry = () => guarded(async () => {
  job.value = await call<Job>('POST', 'job/retry')
  startPolling()
})

function startPolling() {
  clearTimeout(poll)
  const tick = async () => {
    try {
      job.value = await call<Job>('GET', 'job')
      if (job.value.status === 'completed') return waitForRestart()
      if (job.value.status === 'failed') return
    } catch (e) {
      // After the restart the installed server answers 404 for every /setup endpoint.
      if (e instanceof SetupApiError && e.status === 404 && job.value?.status !== 'failed') return waitForRestart()
    }
    poll = setTimeout(tick, 1500)
  }
  tick()
}
function waitForRestart() {
  phase.value = 'restarting'
  const check = async () => {
    try {
      const res = await fetch('/api/v1/instance', { cache: 'no-store' })
      if (res.ok) {
        location.href = '/login'
        return
      }
    } catch { /* still restarting */ }
    poll = setTimeout(check, 1500)
  }
  check()
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
    const status = await call<{ env_file: string; defaults: Record<string, string> }>('GET', 'status')
    envFile.value = status.env_file
    const d = status.defaults || {}
    db.host = d.database_host || db.host
    db.name = d.database_name || db.name
    db.user = d.database_user || db.user
    db.path = d.sqlite_path || db.path
    redis.address = d.redis_address || redis.address
  } catch (e) {
    if (e instanceof SetupApiError && e.status === 404) location.href = '/login'
  }
})
onBeforeUnmount(() => clearTimeout(poll))
</script>

<template>
  <main class="setup">
    <header class="setup__header">
      <img src="/logo.svg" alt="" class="setup__logo" width="40" height="40">
      <div>
        <h1>{{ t('title') }}</h1>
        <p class="muted">{{ t('subtitle') }}</p>
      </div>
    </header>

    <section v-if="phase === 'wizard'" class="card">
      <ol class="steps" :aria-label="t('title')">
        <li v-for="(s, i) in steps" :key="s" :class="{ active: s === step, done: i < stepIndex }">
          <span class="steps__dot">{{ i + 1 }}</span><span class="steps__label">{{ t(`steps.${s}`) }}</span>
        </li>
      </ol>

      <form class="form" @submit.prevent>
        <template v-if="step === 'language'">
          <h2>{{ t('language.title') }}</h2>
          <p class="muted">{{ t('language.hint') }}</p>
          <div class="choices choices--grid" role="radiogroup">
            <label v-for="l in LOCALES" :key="l.code" class="choice" :class="{ selected: locale === l.code }">
              <input type="radio" name="locale" :value="l.code" :checked="locale === l.code" @change="pickLocale(l.code)">
              <span lang="">{{ l.name }}</span>
            </label>
          </div>
        </template>

        <template v-else-if="step === 'token'">
          <h2>{{ t('token.title') }}</h2>
          <p class="muted">{{ t('token.hint') }}</p>
          <label class="field"><span>{{ t('token.label') }}</span>
            <input v-model="token" autocomplete="one-time-code" autocapitalize="off" spellcheck="false" required @keyup.enter="verifyToken">
          </label>
        </template>

        <template v-else-if="step === 'mode'">
          <h2>{{ t('mode.title') }}</h2>
          <div class="choices" role="radiogroup">
            <label class="choice choice--wide" :class="{ selected: mode === 'fresh' }">
              <input v-model="mode" type="radio" value="fresh"><strong>{{ t('mode.fresh') }}</strong><small>{{ t('mode.freshHint') }}</small>
            </label>
            <label class="choice choice--wide" :class="{ selected: mode === 'misskey' }">
              <input v-model="mode" type="radio" value="misskey"><strong>{{ t('mode.misskey') }}</strong><small>{{ t('mode.misskeyHint') }}</small>
            </label>
          </div>
        </template>

        <template v-else-if="step === 'source'">
          <h2>{{ t('source.title') }}</h2>
          <p class="muted">{{ t('source.hint') }}</p>
          <div class="grid">
            <label class="field field--wide"><span>{{ t('source.host') }}</span><input v-model="source.host" autocapitalize="off" spellcheck="false"></label>
            <label class="field"><span>{{ t('source.port') }}</span><input v-model.number="source.port" inputmode="numeric"></label>
            <label class="field"><span>{{ t('source.name') }}</span><input v-model="source.name" autocapitalize="off"></label>
            <label class="field"><span>{{ t('source.user') }}</span><input v-model="source.user" autocapitalize="off"></label>
            <label class="field"><span>{{ t('source.password') }}</span><input v-model="source.password" type="password" autocomplete="off"></label>
            <label class="field"><span>{{ t('source.sslmode') }}</span>
              <select v-model="source.sslmode"><option>disable</option><option>prefer</option><option>require</option><option>verify-full</option></select>
            </label>
          </div>
          <button type="button" class="btn" :disabled="busy" @click="inspectSource">{{ busy ? t('common.testing') : t('source.inspect') }}</button>
          <p v-if="inspection" class="notice notice--ok">{{ t('source.found', { users: inspection.counts.users ?? 0, notes: inspection.counts.notes ?? 0, files: inspection.counts.files ?? inspection.counts.drive_files ?? 0 }) }}</p>

          <template v-if="tested.source">
            <h3>{{ t('source.mediaTitle') }}</h3>
            <div class="choices">
              <label class="choice" :class="{ selected: source.media_source === 'local' }"><input v-model="source.media_source" type="radio" value="local">{{ t('source.mediaLocal') }}</label>
              <label class="choice" :class="{ selected: source.media_source === 's3' }"><input v-model="source.media_source" type="radio" value="s3">{{ t('source.mediaS3') }}</label>
            </div>
            <label v-if="source.media_source === 'local'" class="field"><span>{{ t('source.filesPath') }}</span>
              <input v-model="source.files_path" autocapitalize="off" spellcheck="false"><small>{{ t('source.filesHint') }}</small>
            </label>
            <template v-else>
              <p v-if="inspection?.object_storage" class="muted">{{ t('source.prefilled') }}</p>
              <div class="choices">
                <label class="choice choice--wide" :class="{ selected: source.media_mode === 'reference' }"><input v-model="source.media_mode" type="radio" value="reference"><strong>{{ t('source.modeReference') }}</strong><small>{{ t('source.modeReferenceHint') }}</small></label>
                <label class="choice choice--wide" :class="{ selected: source.media_mode === 'copy' }"><input v-model="source.media_mode" type="radio" value="copy"><strong>{{ t('source.modeCopy') }}</strong><small>{{ t('source.modeCopyHint') }}</small></label>
              </div>
              <div class="grid">
                <label class="field field--wide"><span>{{ t('source.endpoint') }}</span><input v-model="source.object_storage.endpoint" autocapitalize="off" spellcheck="false"></label>
                <label class="field"><span>{{ t('source.bucket') }}</span><input v-model="source.object_storage.bucket" autocapitalize="off"></label>
                <label class="field"><span>{{ t('source.region') }}</span><input v-model="source.object_storage.region" autocapitalize="off"></label>
                <label class="field"><span>{{ t('source.accessKey') }}</span><input v-model="source.object_storage.access_key" autocapitalize="off" spellcheck="false"></label>
                <label class="field"><span>{{ t('source.secretKey') }}</span><input v-model="source.object_storage.secret_key" type="password" autocomplete="off"><small v-if="inspection?.has_object_secret">{{ t('source.secretKept') }}</small></label>
                <label class="field field--wide"><span>{{ t('source.publicUrl') }}</span><input v-model="source.object_storage.public_url" autocapitalize="off" spellcheck="false"></label>
                <label class="check"><input v-model="source.object_storage.path_style" type="checkbox">{{ t('source.pathStyle') }}</label>
              </div>
              <button type="button" class="btn" :disabled="busy" @click="testStorage">{{ busy ? t('common.testing') : t('source.testStorage') }}</button>
            </template>
          </template>
        </template>

        <template v-else-if="step === 'domain'">
          <h2>{{ t('domain.title') }}</h2>
          <p class="muted">{{ t('domain.hint') }}</p>
          <label class="field"><span>{{ t('domain.label') }}</span>
            <input v-model="domain" type="url" inputmode="url" autocapitalize="off" spellcheck="false" placeholder="https://social.example.com" :readonly="domainLocked" :aria-readonly="domainLocked" required>
          </label>
          <p v-if="domainLocked" class="notice"><strong>{{ t('domain.detected') }}</strong> — {{ t('domain.detectedHint') }}</p>
          <p v-else-if="mode === 'misskey'" class="notice notice--warn">{{ t('domain.notDetected') }}</p>
          <p class="notice notice--warn">{{ t('domain.permanent') }}</p>
        </template>

        <template v-else-if="step === 'database'">
          <h2>{{ t('db.title') }}</h2>
          <p class="muted">{{ t('db.emptyHint') }}</p>
          <label class="field"><span>{{ t('db.type') }}</span>
            <select v-model="db.type" @change="tested.database = false; db.port = db.type === 'mysql' ? 3306 : 5432">
              <option value="postgres">PostgreSQL</option><option value="mysql">MySQL / MariaDB</option><option value="sqlite">SQLite</option>
            </select>
          </label>
          <template v-if="db.type === 'sqlite'">
            <label class="field"><span>{{ t('db.path') }}</span><input v-model="db.path" autocapitalize="off" spellcheck="false"><small>{{ t('db.sqliteHint') }}</small></label>
          </template>
          <div v-else class="grid">
            <label class="field field--wide"><span>{{ t('db.host') }}</span><input v-model="db.host" autocapitalize="off" spellcheck="false"></label>
            <label class="field"><span>{{ t('db.port') }}</span><input v-model.number="db.port" inputmode="numeric"></label>
            <label class="field"><span>{{ t('db.name') }}</span><input v-model="db.name" autocapitalize="off"></label>
            <label class="field"><span>{{ t('db.user') }}</span><input v-model="db.user" autocapitalize="off"></label>
            <label class="field"><span>{{ t('db.password') }}</span><input v-model="db.password" type="password" autocomplete="off"></label>
            <label v-if="db.type === 'postgres'" class="field"><span>{{ t('db.sslmode') }}</span>
              <select v-model="db.sslmode"><option>disable</option><option>prefer</option><option>require</option><option>verify-full</option></select>
            </label>
          </div>
          <button type="button" class="btn" :disabled="busy" @click="testDatabase">{{ busy ? t('common.testing') : t('common.test') }}</button>
        </template>

        <template v-else-if="step === 'redis'">
          <h2>{{ t('redis.title') }}</h2>
          <div class="grid">
            <label class="field field--wide"><span>{{ t('redis.address') }}</span><input v-model="redis.address" autocapitalize="off" spellcheck="false"></label>
            <label class="field"><span>{{ t('redis.password') }}</span><input v-model="redis.password" type="password" autocomplete="off"></label>
            <label class="field"><span>{{ t('redis.db') }}</span><input v-model.number="redis.db" inputmode="numeric"></label>
          </div>
          <button type="button" class="btn" :disabled="busy" @click="testRedis">{{ busy ? t('common.testing') : t('common.test') }}</button>
        </template>

        <template v-else-if="step === 'admin'">
          <h2>{{ t('admin.title') }}</h2>
          <p class="muted">{{ mode === 'misskey' ? t('admin.hintMisskey') : t('admin.hintFresh') }}</p>
          <label class="field"><span>{{ t('admin.username') }}</span><input v-model="admin.username" autocomplete="username" autocapitalize="off" spellcheck="false"></label>
          <label class="field"><span>{{ t('admin.email') }}</span><input v-model="admin.email" type="email" autocomplete="email"></label>
          <label class="field"><span>{{ t('admin.password') }}</span><input v-model="admin.password" type="password" autocomplete="new-password"><small>{{ t('admin.passwordHint') }}</small></label>
        </template>

        <template v-else-if="step === 'review'">
          <h2>{{ t('review.title') }}</h2>
          <dl class="summary">
            <dt>{{ t('steps.language') }}</dt><dd>{{ LOCALES.find(l => l.code === locale)?.name }}</dd>
            <dt>{{ t('steps.mode') }}</dt><dd>{{ mode === 'misskey' ? t('mode.misskey') : t('mode.fresh') }}</dd>
            <dt>{{ t('steps.domain') }}</dt><dd>{{ domain }}</dd>
            <dt>{{ t('steps.database') }}</dt><dd>{{ db.type === 'sqlite' ? db.path : `${db.type}://${db.user}@${db.host}:${db.port}/${db.name}` }}</dd>
            <dt>{{ t('steps.redis') }}</dt><dd>{{ redis.address }}</dd>
            <dt>{{ t('steps.admin') }}</dt><dd>{{ admin.username }} &lt;{{ admin.email }}&gt;</dd>
          </dl>
          <p class="muted">{{ t('review.hint', { path: envFile }) }}</p>
        </template>

        <p v-if="error" class="notice notice--error" role="alert">{{ error }}</p>
        <p v-if="okMessage" class="notice notice--ok" role="status">{{ okMessage }}</p>

        <div class="actions">
          <button v-if="stepIndex > 0 && step !== 'token'" type="button" class="btn btn--ghost" :disabled="busy" @click="go(-1)">{{ t('common.back') }}</button>
          <span class="spacer" />
          <button v-if="step === 'token'" type="button" class="btn btn--primary" :disabled="busy || !token.trim()" @click="verifyToken">{{ t('token.verify') }}</button>
          <button v-else-if="step === 'review'" type="button" class="btn btn--primary" :disabled="busy" @click="install">{{ mode === 'misskey' ? t('review.migrate') : t('review.install') }}</button>
          <button v-else type="button" class="btn btn--primary" :disabled="busy || (step === 'source' && !tested.source) || (step === 'domain' && !domain.trim()) || (step === 'database' && !tested.database) || (step === 'redis' && !tested.redis)" @click="go(1)">{{ t('common.next') }}</button>
        </div>
      </form>
    </section>

    <section v-else class="card">
      <h2>{{ t('progress.title') }}</h2>
      <p v-if="phase === 'restarting'" class="notice notice--ok" role="status">{{ t('progress.completed') }}</p>
      <p v-else-if="job?.status === 'failed'" class="notice notice--error" role="alert">{{ t('progress.failed') }}</p>
      <p v-else class="muted" role="status">{{ t('progress.running') }}</p>
      <ul class="stages">
        <li v-for="s in job?.stages || []" :key="s.name" :class="`stage stage--${s.status}`">
          <div class="stage__head">
            <span class="stage__icon" aria-hidden="true" />
            <span class="stage__name">{{ stageText(s.name) }}</span>
            <span class="stage__status">{{ te(`status.${s.status}`) ? t(`status.${s.status}`) : s.status }}</span>
          </div>
          <div v-if="progressOf(s.counts)" class="stage__progress">
            <progress :value="progressOf(s.counts)!.percent" max="100" />
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
        <button type="button" class="btn btn--primary" :disabled="busy" @click="retry">{{ t('progress.retry') }}</button>
      </div>
      <details v-if="job?.log?.length" class="log" open>
        <summary>{{ t('progress.log') }}</summary>
        <pre><template v-for="(l, i) in job.log.slice(-200)" :key="i"><span :class="`log--${l.level}`">{{ new Date(l.time).toLocaleTimeString(locale) }} {{ l.message }}</span>
</template></pre>
      </details>
    </section>
  </main>
</template>

<style>
:root { color-scheme: light dark; --bg: #f5f6f8; --card: #fff; --text: #1d2129; --muted: #5c6370; --border: #dcdfe6; --primary: #3a6ff7; --ok: #1f8a4c; --warn: #a15c00; --err: #c62828; }
@media (prefers-color-scheme: dark) { :root { --bg: #111317; --card: #1b1e24; --text: #e6e8eb; --muted: #9aa1ab; --border: #30343c; --primary: #6b93ff; --ok: #4cc27f; --warn: #e3a54a; --err: #ff6b6b; } }
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--text); font: 15px/1.5 system-ui, -apple-system, "Segoe UI", "Noto Sans", "PingFang SC", "Hiragino Sans", sans-serif; }
.setup { max-width: 760px; margin: 0 auto; padding: 24px 16px calc(32px + env(safe-area-inset-bottom)); }
.setup__header { display: flex; gap: 12px; align-items: center; margin-bottom: 16px; }
.setup__header h1 { margin: 0; font-size: 22px; }
.setup__header p { margin: 0; }
.muted { color: var(--muted); }
.card { background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 20px; }
h2 { margin: 0 0 6px; font-size: 19px; }
h3 { margin: 20px 0 8px; font-size: 16px; }
.steps { display: flex; gap: 6px; list-style: none; padding: 0; margin: 0 0 20px; overflow-x: auto; scrollbar-width: none; }
.steps li { display: flex; align-items: center; gap: 6px; flex: 0 0 auto; color: var(--muted); font-size: 13px; padding-right: 6px; }
.steps__dot { width: 22px; height: 22px; border-radius: 50%; border: 1px solid var(--border); display: grid; place-items: center; font-size: 12px; }
.steps li.done .steps__dot { background: var(--ok); border-color: var(--ok); color: #fff; }
.steps li.active { color: var(--text); font-weight: 600; }
.steps li.active .steps__dot { background: var(--primary); border-color: var(--primary); color: #fff; }
@media (max-width: 600px) { .steps li:not(.active) .steps__label { display: none; } }
.form { display: flex; flex-direction: column; gap: 12px; }
.grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.field--wide { grid-column: 1 / -1; }
@media (max-width: 600px) { .grid { grid-template-columns: 1fr; } }
.field { display: flex; flex-direction: column; gap: 4px; }
.field > span { font-weight: 600; font-size: 14px; }
.field small, .choice small { color: var(--muted); }
input, select { font: inherit; color: inherit; background: transparent; border: 1px solid var(--border); border-radius: 8px; padding: 10px 12px; min-height: 44px; width: 100%; }
input[readonly] { background: color-mix(in srgb, var(--border) 35%, transparent); }
input:focus, select:focus { outline: 2px solid var(--primary); outline-offset: -1px; }
.check { display: flex; gap: 8px; align-items: center; }
.check input { width: auto; min-height: 0; }
.choices { display: flex; flex-direction: column; gap: 8px; }
.choices--grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); }
.choice { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 10px; border: 1px solid var(--border); border-radius: 10px; padding: 12px 14px; cursor: pointer; min-height: 48px; }
.choice input { width: auto; min-height: 0; margin: 0; }
.choice--wide small { flex-basis: 100%; padding-left: 24px; }
.choice.selected { border-color: var(--primary); box-shadow: 0 0 0 1px var(--primary) inset; }
.btn { font: inherit; border: 1px solid var(--border); background: transparent; color: var(--text); border-radius: 8px; padding: 10px 16px; min-height: 44px; cursor: pointer; align-self: flex-start; }
.btn--primary { background: var(--primary); border-color: var(--primary); color: #fff; font-weight: 600; }
.btn--ghost { border-color: transparent; }
.btn:disabled { opacity: .55; cursor: not-allowed; }
.actions { display: flex; gap: 8px; align-items: center; margin-top: 8px; position: sticky; bottom: 0; background: var(--card); padding: 8px 0 0; }
.spacer { flex: 1; }
.notice { margin: 0; padding: 10px 12px; border-radius: 8px; border: 1px solid var(--border); }
.notice--ok { border-color: var(--ok); color: var(--ok); }
.notice--warn { border-color: var(--warn); color: var(--warn); }
.notice--error { border-color: var(--err); color: var(--err); }
.summary { display: grid; grid-template-columns: max-content 1fr; gap: 6px 16px; margin: 8px 0; }
.summary dt { color: var(--muted); }
.summary dd { margin: 0; overflow-wrap: anywhere; }
.stages { list-style: none; padding: 0; margin: 16px 0; display: flex; flex-direction: column; gap: 8px; }
.stage { border: 1px solid var(--border); border-radius: 10px; padding: 10px 12px; }
.stage__head { display: flex; align-items: center; gap: 10px; }
.stage__name { flex: 1; font-weight: 600; }
.stage__status { color: var(--muted); font-size: 13px; }
.stage__icon { width: 10px; height: 10px; border-radius: 50%; background: var(--border); flex: none; }
.stage--running .stage__icon { background: var(--primary); animation: pulse 1s infinite alternate; }
.stage--done .stage__icon { background: var(--ok); }
.stage--skipped .stage__icon { background: var(--muted); }
.stage--failed { border-color: var(--err); }
.stage--failed .stage__icon { background: var(--err); }
@keyframes pulse { to { opacity: .3; } }
.stage__progress { display: flex; flex-direction: column; gap: 2px; margin-top: 8px; }
.stage__progress progress { width: 100%; height: 8px; accent-color: var(--primary); }
.stage__counts { list-style: none; display: flex; flex-wrap: wrap; gap: 4px 14px; padding: 0; margin: 6px 0 0 20px; color: var(--muted); font-size: 13px; }
.stage__counts b { color: var(--text); }
.stage__error { color: var(--err); margin: 6px 0 0 20px; font-size: 14px; }
.log pre { max-height: 280px; overflow: auto; background: color-mix(in srgb, var(--border) 30%, transparent); padding: 10px; border-radius: 8px; font-size: 12px; white-space: pre-wrap; }
.log--error { color: var(--err); }
.log--warn { color: var(--warn); }
</style>
