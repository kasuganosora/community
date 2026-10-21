import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import SetupApp from '../SetupApp.vue'
import { applyLocale, i18n } from '../i18n'
import { normalizeSetupDomain, normalizeStorageURL } from '../validation'

const reply = (body: unknown, status = 200) => Promise.resolve(new Response(JSON.stringify(body), { status }))
const job = { id: 'job-1', status: 'running', stages: [{ name: 'schema', status: 'running' }], log: [] }
const calls: { path: string; body: Record<string, unknown>; signal?: AbortSignal | null }[] = []
const overrides = new Map<string, () => Promise<Response>>()
let wrappers: VueWrapper[] = []
const checkNames = ['web_port', 'web_meta', 'sessions', 'note_writes', 'queues']
function stoppedResult(status: 'STOPPED' | 'RUNNING') {
  return { status, checked_at: '2026-10-21T10:00:00Z', checks: checkNames.map(name => ({
    name, title: name, status: name === 'queues' ? 'skipped' : status === 'RUNNING' && name === 'sessions' ? 'fail' : 'pass',
    reasons: status === 'RUNNING' && name === 'sessions' ? ['1 other client session(s) connected to the source database', 'pid=42 user=misskey'] : ['ok'],
  })) }
}
beforeEach(() => {
  applyLocale('en')
  sessionStorage.removeItem('setup-token')
  calls.length = 0; overrides.clear()
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    const path = url.replace('/api/setup/', '')
    calls.push({ path, body: init?.body ? JSON.parse(String(init.body)) : {}, signal: init?.signal })
    const override = overrides.get(path)
    if (override) return override()
    if (path === 'status') return reply({ env_file: '/app/config/.env', defaults: { sqlite_path: '/app/data/community.db' } })
    if (path === 'install' || path === 'job' || path === 'job/retry') return reply(job)
    if (path === 'test/misskey') return reply({ inspection: { counts: { users: 2 }, id_method: 'aid', detected_base_url: 'https://old.example', uses_object_storage: false } })
    if (path === 'test/source-stopped') return reply({ result: stoppedResult('STOPPED') })
    if (path === 'test/mastodon') return reply({ inspection: { counts: { local_users: 2, remote_users: 5 }, detected_base_url: 'https://old.example' } })
    return reply({ ok: true })
  }))
})
afterEach(() => { wrappers.forEach(w => w.unmount()); wrappers = []; document.body.innerHTML = ''; vi.unstubAllGlobals(); vi.useRealTimers() })
async function setup() {
  const w = mount(SetupApp, { attachTo: document.body, global: { plugins: [i18n] } })
  wrappers.push(w); await flushPromises(); return w
}
async function submit(w: VueWrapper) { await w.get('form').trigger('submit'); await flushPromises() }
async function back(w: VueWrapper) { await w.get('.actions .btn--ghost').trigger('click'); await flushPromises() }
async function toMode(w: VueWrapper) {
  await submit(w)
  await w.get('input[autocomplete="one-time-code"]').setValue('qa-token')
  await submit(w)
}
async function toDatabase(w: VueWrapper) {
  await toMode(w); await submit(w)
  await w.get('[data-field="domain"]').setValue('social.example')
  await submit(w)
}
async function toReview(w: VueWrapper) {
  await toDatabase(w); await submit(w); await submit(w)
  await w.get('[data-field="username"]').setValue('admin')
  await w.get('[data-field="email"]').setValue('admin@example.org')
  await w.get('[data-field="password"]').setValue('qa-password')
  await submit(w)
}

describe('installer form navigation', () => {
  it('does not offer a built-in database without the server capability', async () => {
    const w = await setup(); await toDatabase(w)
    expect(w.find('option[value="builtin"]').exists()).toBe(false)
    expect(w.find('.grid input').exists()).toBe(true)
  })

  it.each(['postgres', 'mysql'])('uses the server-side %s connection without sending credentials', async (type) => {
    overrides.set('status', () => reply({ defaults: {}, builtin_database: { type } }))
    const w = await setup(); await toDatabase(w)
    expect(w.get('form select').element).toHaveProperty('value', 'builtin')
    expect(w.find('input[type="password"]').exists()).toBe(false)
    await submit(w)
    expect(calls.find(c => c.path === 'test/database')?.body).toEqual({ type: 'builtin' })
    await submit(w)
    await w.get('[data-field="username"]').setValue('admin')
    await w.get('[data-field="email"]').setValue('admin@example.org')
    await w.get('[data-field="password"]').setValue('qa-password')
    await submit(w)
    expect(w.get('.summary').text()).toContain(`Built-in database · ${type === 'mysql' ? 'MySQL' : 'PostgreSQL'}`)
    await submit(w)
    expect(calls.find(c => c.path === 'install')?.body.database).toEqual({ type: 'builtin' })
  })

  it('still allows manual connections when a bundled database is available', async () => {
    overrides.set('status', () => reply({ defaults: {}, builtin_database: { type: 'mysql' } }))
    const w = await setup(); await toDatabase(w)
    await w.get('form select').setValue('postgres')
    await w.get('.grid input').setValue('external.example')
    await submit(w)
    expect(calls.find(c => c.path === 'test/database')?.body).toMatchObject({ type: 'postgres', host: 'external.example', port: 5432 })
  })

  it('does not replace edited connection fields with late server defaults', async () => {
    let finish!: (response: Response) => void
    overrides.set('status', () => new Promise(resolve => { finish = resolve }))
    const w = await setup()
    overrides.set('status', () => reply({ defaults: {} }))
    await toDatabase(w)
    await w.get('.grid input').setValue('chosen.example')
    finish(new Response(JSON.stringify({ defaults: { database_host: 'late.example' }, builtin_database: { type: 'mysql' } })))
    await flushPromises()
    expect(w.get('.grid input').element).toHaveProperty('value', 'chosen.example')
    expect(w.get('form select').element).toHaveProperty('value', 'postgres')
    expect(w.find('option[value="builtin"]').exists()).toBe(true)
  })

  it('normalizes origins without accepting insecure remote addresses or embedded credentials', () => {
    expect(normalizeSetupDomain(' Social.Example/ ')).toBe('https://social.example')
    expect(normalizeSetupDomain('http://localhost:8080')).toBe('http://localhost:8080')
    for (const value of ['http://social.example', 'https://user:secret@social.example', 'https://social.example/path', 'https://social.example?x=1', 'https://social.example/#x', 'https://localhost\\@elsewhere.test']) expect(normalizeSetupDomain(value)).toBeNull()
  })

  it('validates the domain before moving forward and focuses its error field', async () => {
    const w = await setup(); await toMode(w); await submit(w)
    await w.get('[data-field="domain"]').setValue('http://social.example')
    await submit(w)
    expect(w.get('h2').text()).toBe('Public address')
    expect(w.get('[data-field="domain"]').attributes('aria-invalid')).toBe('true')
    expect(document.activeElement).toBe(w.get('[data-field="domain"]').element)
    await w.get('[data-field="domain"]').setValue('social.example')
    await submit(w)
    expect(w.get('h2').text()).toBe('Database')
    expect(document.activeElement).toBe(w.get('h2').element)
  })

  it('blocks duplicate connection checks and keeps the form after a failed test', async () => {
    let finish!: (response: Response) => void
    overrides.set('test/database', () => new Promise(resolve => { finish = resolve }))
    const w = await setup(); await toDatabase(w)
    await submit(w); await submit(w)
    expect(calls.filter(c => c.path === 'test/database')).toHaveLength(1)
    expect(w.get('.actions .btn--primary').text()).toContain('Testing')
    expect(w.get('fieldset').attributes('disabled')).toBeDefined()
    finish(new Response(JSON.stringify({ error_code: 'database_connect_failed' }), { status: 400 })); await flushPromises()
    expect(w.get('h2').text()).toBe('Database')
    expect(w.get('[role="alert"]').text()).toContain('database')
    expect(w.get('fieldset').attributes('disabled')).toBeUndefined()
    overrides.delete('test/database'); await submit(w)
    expect(w.get('h2').text()).toBe('Redis')
    await back(w)
    expect(w.get('.actions .btn--primary').text()).toContain('Next')
    await w.get('.grid input').setValue('another-db')
    expect(w.get('.actions .btn--primary').text()).toContain('Test and continue')
    await submit(w)
    expect(calls.filter(c => c.path === 'test/database')).toHaveLength(3)
  })

  it('retests edited Redis settings and preserves administrator drafts on a review edit', async () => {
    const w = await setup(); await toReview(w)
    expect(w.get('h2').text()).toBe('Ready to install')
    expect(w.get('.summary').text()).not.toContain('qa-password')
    await w.get('button[aria-label="Edit · Redis"]').trigger('click')
    await w.get('.grid input').setValue('redis-new:6379')
    await submit(w)
    expect(w.get('h2').text()).toBe('Ready to install')
    expect(w.get('.summary').text()).toContain('redis-new:6379')
    expect(calls.filter(c => c.path === 'test/redis')).toHaveLength(2)
    await w.get('button[aria-label="Edit · Administrator"]').trigger('click')
    expect(w.get('[data-field="username"]').element).toHaveProperty('value', 'admin')
    await w.get('[data-field="password"]').setValue('short')
    await submit(w)
    expect(w.find('[role="alert"]').exists()).toBe(true)
    expect(w.get('[data-field="password"]').attributes('aria-invalid')).toBe('true')
    await w.get('button[aria-label="Show · Password"]').trigger('click')
    expect(w.get('[data-field="password"]').attributes('type')).toBe('text')
  })

  it('invalidates a source inspection and detected domain when its connection changes', async () => {
    const w = await setup(); await toMode(w)
    await w.get('input[value="misskey"]').setValue(true); await submit(w)
    await submit(w)
    await submit(w)
    expect(w.get('[data-field="domain"]').element).toHaveProperty('value', 'https://old.example')
    await back(w); await back(w); await w.get('.grid input').setValue('other-source')
    expect(w.find('.notice--ok').exists()).toBe(false)
    expect(w.get('.actions .btn--primary').text()).toContain('Connect and inspect')
    overrides.set('test/misskey', () => reply({ error_code: 'misskey_connect_failed' }, 422))
    await submit(w)
    expect(w.get('h2').text()).toBe('Misskey database')
  })

  it('starts installation once with the reviewed values and keeps secrets out of the summary', async () => {
    let finish!: (response: Response) => void
    overrides.set('install', () => new Promise(resolve => { finish = resolve }))
    const w = await setup(); await toReview(w)
    await submit(w); await submit(w)
    const requests = calls.filter(c => c.path === 'install')
    expect(requests).toHaveLength(1)
    expect(requests[0]!.body).toMatchObject({ domain: 'https://social.example', admin: { username: 'admin', password: 'qa-password' } })
    expect(w.get('.actions .btn--primary').text()).toContain('Starting')
    finish(new Response(JSON.stringify(job), { status: 200 })); await flushPromises()
    expect(w.find('.stages').exists()).toBe(true)
  })
})

describe('installer progress recovery', () => {
  it('verifies the stored token before restoring an existing job and reports authentication errors', async () => {
    sessionStorage.setItem('setup-token', 'saved-token')
    overrides.set('status', () => reply({ defaults: {}, job: { status: 'running' } }))
    overrides.set('verify', () => reply({ error_code: 'invalid_token' }, 401))
    const w = await setup()
    expect(w.get('h2').text()).toBe('Enter the setup token')
    expect(w.get('[role="alert"]').text()).toContain('token')
    expect(calls.some(c => c.path === 'job')).toBe(false)
    overrides.delete('verify'); await submit(w)
    expect(w.find('.stages').exists()).toBe(true)
    expect(calls.findIndex(c => c.path === 'verify')).toBeLessThan(calls.findIndex(c => c.path === 'job'))
  })

  it('reports a lost connection and does not rearm polling after unmount', async () => {
    vi.useFakeTimers()
    sessionStorage.setItem('setup-token', 'saved-token')
    overrides.set('status', () => reply({ defaults: {}, job: { status: 'running' } }))
    overrides.set('job', () => Promise.reject(new TypeError('offline')))
    const w = await setup()
    expect(w.get('[role="alert"]').text()).toContain('Reconnecting')
    let finish!: (response: Response) => void
    overrides.set('job', () => new Promise(resolve => { finish = resolve }))
    await vi.advanceTimersByTimeAsync(4000)
    const count = calls.length
    w.unmount(); wrappers = []
    expect(calls.at(-1)!.signal?.aborted).toBe(true)
    finish(new Response(JSON.stringify(job), { status: 200 })); await flushPromises()
    await vi.advanceTimersByTimeAsync(6000)
    expect(calls).toHaveLength(count)
  })
})

async function toMedia(w: VueWrapper, mode: 'misskey' | 'mastodon' = 'misskey') {
  await toMode(w)
  await w.get(`input[value="${mode}"]`).setValue(true)
  await submit(w)
  expect(w.find('[data-field="files-path"]').exists()).toBe(false)
  await submit(w)
  expect(w.get('h2').text()).toBe('Media files')
}
async function finishMigrationReview(w: VueWrapper) {
  await submit(w) // media -> domain
  await submit(w) // domain -> target database
  await submit(w) // database -> Redis
  await submit(w) // Redis -> admin
  await w.get('[data-field="username"]').setValue('admin')
  await w.get('[data-field="email"]').setValue('admin@example.org')
  await w.get('[data-field="password"]').setValue('qa-password')
  await submit(w)
  expect(w.get('h2').text()).toBe('Ready to migrate')
  if (w.find('[data-testid="stop-check-run"]').exists()) { await w.get('[data-testid="stop-check-run"]').trigger('click'); await flushPromises() }
}
function storedMediaInspection(extra = {}) {
  return reply({ inspection: {
    counts: { local_users: 7, remote_users: 12, notes: 300, files: 40 }, id_method: 'aidx', detected_base_url: 'https://old.example',
    uses_object_storage: true, has_object_secret: true,
    object_storage: { endpoint: 'https://s3.example', bucket: 'old-media', region: '', access_key: 'stored-access', public_url: 'https://cdn.example/media', path_style: true }, ...extra,
  } })
}

describe('migration-specific form validation and recovery', () => {
  it('normalizes public media prefixes and storage endpoints without URL credentials', () => {
    expect(normalizeStorageURL('https://cdn.example/media/')).toBe('https://cdn.example/media')
    expect(normalizeStorageURL('minio:9000', true)).toBe('https://minio:9000')
    expect(normalizeStorageURL('http://minio:9000/', true)).toBe('http://minio:9000')
    for (const value of ['ftp://cdn.example/files', 'https://user:secret@cdn.example', 'https://cdn.example/?key=secret', 'https://cdn.example/#x']) expect(normalizeStorageURL(value)).toBeNull()
    expect(normalizeStorageURL('https://s3.example/path', true)).toBeNull()
  })

  it('blocks duplicate inspections, reports failure and advances only after success', async () => {
    let finish!: (response: Response) => void
    overrides.set('test/misskey', () => new Promise(resolve => { finish = resolve }))
    const w = await setup(); await toMode(w)
    await w.get('input[value="misskey"]').setValue(true); await submit(w)
    await submit(w); await submit(w)
    expect(calls.filter(c => c.path === 'test/misskey')).toHaveLength(1)
    expect(w.get('.actions .btn--primary').text()).toContain('Testing')
    expect(w.get('fieldset').attributes('disabled')).toBeDefined()
    finish(new Response(JSON.stringify({ error_code: 'misskey_connect_failed' }), { status: 422 })); await flushPromises()
    expect(w.get('h2').text()).toBe('Misskey database')
    overrides.set('test/misskey', () => storedMediaInspection()); await submit(w)
    expect(w.get('h2').text()).toBe('Media files')
    expect(w.text()).toContain('7 local accounts, 12 remote accounts, 300 posts and 40 media files')
    expect(calls.find(c => c.path === 'test/misskey')?.body).not.toHaveProperty('object_storage')
  })

  it.each(['misskey', 'mastodon'] as const)('requires only the public media prefix for %s references', async (mode) => {
    if (mode === 'misskey') overrides.set('test/misskey', () => storedMediaInspection())
    const w = await setup(); await toMedia(w, mode)
    await w.get('input[value="s3"]').setValue(true)
    expect(w.find('[data-field="storage-secret"]').exists()).toBe(false)
    expect(w.find('[data-field="storage-endpoint"]').exists()).toBe(false)
    await w.get('[data-field="media-url"]').setValue('')
    await submit(w)
    expect(w.get('h2').text()).toBe('Media files')
    await w.get('[data-field="media-url"]').setValue('https://cdn.example/media/')
    await finishMigrationReview(w)
    expect(w.get('.summary').text()).toContain('Reference in place · https://cdn.example/media')
    await submit(w)
    const body = calls.find(c => c.path === 'install')?.body[mode] as Record<string, unknown>
    expect(body.object_storage).toEqual({ public_url: 'https://cdn.example/media' })
    expect(calls.some(c => c.path === 'test/object-storage')).toBe(false)
  })

  it('requires a manual Misskey ID format when automatic detection fails', async () => {
    overrides.set('test/misskey', () => storedMediaInspection({ id_method: '', uses_object_storage: false }))
    const w = await setup(); await toMode(w)
    await w.get('input[value="misskey"]').setValue(true); await submit(w); await submit(w)
    await submit(w)
    expect(w.get('h2').text()).toBe('Misskey database')
    await w.get('[data-field="id-method"]').setValue('ulid'); await submit(w)
    await finishMigrationReview(w); await submit(w)
    expect(calls.find(c => c.path === 'install')?.body.misskey).toMatchObject({ id_method: 'ulid' })
  })

  it.each(['misskey', 'mastodon'] as const)('requires an explicit choice among multiple %s domains', async (mode) => {
    const inspection = { counts: { local_users: 2 }, detected_base_url: '', base_url_candidates: ['https://old.example', 'https://other.example'], ...(mode === 'misskey' ? { id_method: 'aid' } : {}) }
    overrides.set(`test/${mode}`, () => reply({ inspection }))
    const w = await setup(); await toMedia(w, mode); await submit(w)
    expect(w.get('[data-field="domain"]').element.tagName).toBe('SELECT')
    await submit(w)
    expect(w.get('h2').text()).toBe('Public address')
    await w.get('[data-field="domain"]').setValue('https://old.example')
    await submit(w)
    expect(w.get('h2').text()).toBe('Database')
  })

  it('recovers from a Mastodon inspection error and accepts an undetected LOCAL_DOMAIN', async () => {
    overrides.set('test/mastodon', () => reply({ error_code: 'mastodon_not_mastodon' }, 422))
    const w = await setup(); await toMode(w)
    await w.get('input[value="mastodon"]').setValue(true); await submit(w); await submit(w)
    expect(w.get('h2').text()).toBe('Mastodon database')
    expect(w.get('[role="alert"]').text()).toContain('Mastodon')
    overrides.set('test/mastodon', () => reply({ inspection: { counts: { local_users: 3, remote_users: 8 }, detected_base_url: '' } }))
    await submit(w)
    expect(w.get('[data-field="files-path"]').element).toHaveProperty('value', '/mastodon/public/system')
    expect(w.find('[data-field="id-method"]').exists()).toBe(false)
    expect(w.text()).toContain('3 local accounts, 8 remote accounts')
    await submit(w)
    expect(w.text()).toContain('LOCAL_DOMAIN')
    expect(w.get('[data-field="domain"]').attributes('readonly')).toBeUndefined()
    await submit(w)
    expect(w.get('h2').text()).toBe('Public address')
    await w.get('[data-field="domain"]').setValue('mastodon.example')
    await submit(w)
    expect(w.get('h2').text()).toBe('Database')
    expect(calls.filter(c => c.path === 'test/mastodon')).toHaveLength(2)
    expect(calls.some(c => c.path === 'test/misskey')).toBe(false)
  })

  it('tests copied media, stays on errors, and invalidates a successful check after edits', async () => {
    overrides.set('test/misskey', () => storedMediaInspection())
    overrides.set('test/object-storage', () => reply({ error_code: 'object_storage_failed' }, 422))
    const w = await setup(); await toMedia(w)
    await w.get('input[value="copy"]').setValue(true)
    expect(w.get('[data-field="storage-secret"]').attributes('required')).toBeUndefined()
    await submit(w)
    expect(w.get('h2').text()).toBe('Media files')
    expect(w.get('[role="alert"]').text()).toContain('object storage')
    overrides.delete('test/object-storage'); await submit(w)
    expect(w.get('h2').text()).toBe('Public address')
    await back(w)
    expect(w.get('.actions .btn--primary').text()).toContain('Next')
    await w.get('[data-field="storage-bucket"]').setValue('new-bucket')
    expect(w.get('.actions .btn--primary').text()).toContain('Test and continue')
    await submit(w)
    expect(calls.filter(c => c.path === 'test/object-storage')).toHaveLength(3)
    expect(calls.filter(c => c.path === 'test/object-storage').at(-1)?.body.misskey).toMatchObject({ object_storage: { bucket: 'new-bucket' }, host: '127.0.0.1' })
  })

  it('requires Mastodon copy credentials without requiring a public media URL', async () => {
    const w = await setup(); await toMedia(w, 'mastodon')
    await w.get('input[value="s3"]').setValue(true)
    await w.get('input[value="copy"]').setValue(true)
    expect(w.find('[data-field="media-url"]').exists()).toBe(false)
    await w.get('[data-field="storage-endpoint"]').setValue('minio:9000')
    await w.get('[data-field="storage-bucket"]').setValue('media')
    await w.get('[data-field="storage-access-key"]').setValue('qa-access')
    await submit(w)
    expect(calls.some(c => c.path === 'test/object-storage')).toBe(false)
    await w.get('[data-field="storage-secret"]').setValue('qa-secret')
    await submit(w)
    expect(w.get('h2').text()).toBe('Public address')
    const request = calls.find(c => c.path === 'test/object-storage')?.body.misskey as Record<string, unknown>
    expect(request).not.toHaveProperty('host')
    expect(request.object_storage).toMatchObject({ endpoint: 'https://minio:9000', secret_key: 'qa-secret' })
  })

  it('clears storage presets when inspecting a different database', async () => {
    overrides.set('test/misskey', () => storedMediaInspection())
    const w = await setup(); await toMedia(w)
    expect(w.get('[data-field="media-url"]').element).toHaveProperty('value', 'https://cdn.example/media')
    await back(w)
    await w.get('.grid input').setValue('another-source')
    overrides.set('test/misskey', () => storedMediaInspection({ uses_object_storage: false, object_storage: undefined }))
    await submit(w)
    expect(w.get('input[value="local"]').element).toHaveProperty('checked', true)
    await w.get('input[value="s3"]').setValue(true)
    expect(w.get('[data-field="media-url"]').element).toHaveProperty('value', '')
  })

  it.each(['misskey', 'mastodon'] as const)('returns a missing %s directory to the media step and preserves the reviewed draft', async (mode) => {
    const w = await setup(); await toMedia(w, mode); await finishMigrationReview(w)
    overrides.set('install', () => reply({ error_code: `${mode}_files_missing` }, 422))
    await submit(w)
    expect(w.get('h2').text()).toBe('Media files')
    expect(w.find('[role="alert"]').exists()).toBe(true)
    await w.get('[data-field="files-path"]').setValue('/mounted/files')
    await submit(w)
    expect(w.get('h2').text()).toBe('Ready to migrate')
    expect(w.get('.summary').text()).toContain('/mounted/files')
    expect(w.get('.summary').text()).toContain('admin@example.org')
    overrides.delete('install'); await submit(w)
    expect(calls.filter(c => c.path === 'install').at(-1)?.body).toMatchObject({ mode, [mode]: { files_path: '/mounted/files', object_storage: {} } })
    if (mode === 'mastodon') expect(calls.filter(c => c.path === 'install').at(-1)?.body.mastodon).not.toHaveProperty('id_method')
  })
})

describe('migration progress bar', () => {
  const at = (ms: number) => new Date(Date.parse('2026-10-18T12:00:00Z') + ms).toISOString()
  function migrationJob(over: Record<string, unknown> = {}) {
    return {
      id: 'job-2', status: 'running', log: [], server_time: at(0), heartbeat_at: at(-2000),
      stages: [
        { name: 'misskey_export', status: 'done' },
        { name: 'misskey_import', status: 'running', progress: { phase: 'import/posts', done: 25000, total: 100000, rate: 500, eta_seconds: 150, updated_at: at(-2000) } },
      ],
      ...over,
    }
  }

  it('shows phase, percent, speed, ETA and heartbeat for the running stage after reload', async () => {
    sessionStorage.setItem('setup-token', 'saved-token')
    overrides.set('status', () => reply({ defaults: {}, job: { status: 'running' } }))
    overrides.set('job', () => reply(migrationJob()))
    const w = await setup()
    const bar = w.get('[data-testid="stage-bar"]')
    expect(bar.text()).toContain('Importing · posts')
    expect(bar.text()).toContain('25%')
    expect(bar.text()).toContain('25,000 of 100,000')
    expect(bar.text()).toContain('500/s')
    expect(bar.text()).toContain('about 2m 30s left')
    expect(bar.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('25')
    expect(w.get('[data-testid="heartbeat"]').text()).toContain('updated 2s ago')
    expect(w.find('[data-testid="stalled"]').exists()).toBe(false)
  })

  it('warns when the heartbeat stops and marks the failed stage', async () => {
    sessionStorage.setItem('setup-token', 'saved-token')
    overrides.set('status', () => reply({ defaults: {}, job: { status: 'running' } }))
    overrides.set('job', () => reply(migrationJob({ heartbeat_at: at(-45000) })))
    const w = await setup()
    expect(w.get('[data-testid="stalled"]').text()).toContain('45s')
    overrides.set('job', () => reply(migrationJob({
      status: 'failed',
      stages: [{ name: 'misskey_import', status: 'failed', error: { error_code: 'import_failed' }, progress: { phase: 'import/posts', done: 40000, total: 100000, rate: 0, eta_seconds: -1, updated_at: at(0) } }],
    })))
    w.unmount(); wrappers = []
    const again = await setup()
    const bar = again.get('[data-testid="stage-bar"]')
    expect(bar.classes()).toContain('stage__bar--failed')
    expect(bar.text()).toContain('stopped here')
    expect(bar.text()).not.toContain('left')
  })
})

describe('source stopped check', () => {
  async function toReview() {
    const w = await setup(); await toMedia(w, 'misskey'); await submit(w)
    await submit(w); await submit(w); await submit(w)
    await w.get('[data-field="username"]').setValue('admin')
    await w.get('[data-field="email"]').setValue('admin@example.org')
    await w.get('[data-field="password"]').setValue('qa-password')
    await submit(w)
    return w
  }
  const primary = (w: VueWrapper) => w.get('.actions .btn--primary').element as HTMLButtonElement
  it('blocks the final sync until Misskey is STOPPED and lists every check', async () => {
    const w = await toReview()
    expect(w.find('[data-testid="stop-check"]').exists()).toBe(true)
    expect(primary(w).disabled).toBe(true)
    overrides.set('test/source-stopped', () => reply({ result: stoppedResult('RUNNING') }))
    await w.get('[data-testid="stop-check-run"]').trigger('click'); await flushPromises()
    expect(w.findAll('.stop-check__item').map(i => i.attributes('data-check'))).toEqual(checkNames)
    expect(w.get('[data-check="sessions"]').text()).toContain('Fail')
    expect(w.get('[data-check="sessions"]').text()).toContain('pid=42')
    expect(w.get('[data-check="queues"]').text()).toContain('Skipped')
    expect(w.get('[data-check="web_port"]').text()).toContain('local port 3000 not listening')
    expect(w.get('[data-testid="stop-check-run"]').text()).toBe('Check again')
    expect(primary(w).disabled).toBe(true)
    overrides.delete('test/source-stopped')
    await w.get('[data-testid="stop-check-run"]').trigger('click'); await flushPromises()
    expect(w.get('[data-testid="stop-check-status"]').text()).toContain('STOPPED')
    expect(primary(w).disabled).toBe(false)
    await submit(w)
    expect(calls.find(c => c.path === 'install')?.body.misskey).toMatchObject({ stop_check: { web_url: 'http://127.0.0.1:3000/api/meta', force: false } })
  })
  it('the force override enables the button and is sent to the server', async () => {
    const w = await toReview()
    overrides.set('test/source-stopped', () => reply({ result: stoppedResult('RUNNING') }))
    await w.get('[data-testid="stop-check-run"]').trigger('click'); await flushPromises()
    await w.get('[data-testid="stop-check-force"]').setValue(true)
    expect(primary(w).disabled).toBe(false)
    await submit(w)
    expect(calls.find(c => c.path === 'install')?.body.misskey).toMatchObject({ stop_check: { force: true } })
  })
})
