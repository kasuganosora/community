import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import SetupApp from '../SetupApp.vue'
import { applyLocale, i18n } from '../i18n'
import { normalizeSetupDomain } from '../validation'

const reply = (body: unknown, status = 200) => Promise.resolve(new Response(JSON.stringify(body), { status }))
const job = { id: 'job-1', status: 'running', stages: [{ name: 'schema', status: 'running' }], log: [] }
const calls: { path: string; body: Record<string, unknown>; signal?: AbortSignal | null }[] = []
const overrides = new Map<string, () => Promise<Response>>()
let wrappers: VueWrapper[] = []
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
    await w.findAll('button').find(b => b.text() === 'Connect and inspect')!.trigger('click'); await flushPromises()
    await submit(w)
    expect(w.get('[data-field="domain"]').element).toHaveProperty('value', 'https://old.example')
    await back(w); await w.get('.grid input').setValue('other-source')
    expect(w.find('.notice--ok').exists()).toBe(false)
    expect(w.get('.actions .btn--primary').attributes('disabled')).toBeDefined()
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
