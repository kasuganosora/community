import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises, enableAutoUnmount } from '@vue/test-utils'
import SetupApp from '../SetupApp.vue'
import { i18n, applyLocale, messages } from '../i18n'

enableAutoUnmount(afterEach)
afterEach(() => vi.unstubAllGlobals())

function respond(status: number, body: unknown) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
}

describe('SetupApp', () => {
  beforeEach(() => {
    applyLocale('en')
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      if (url.endsWith('/status')) return respond(200, { env_file: '/app/config/.env', defaults: { redis_address: 'redis:6379' } })
      if (url.endsWith('/verify')) return respond(401, { error_code: 'invalid_token' })
      return respond(404, {})
    }))
  })

  it('switches language immediately from the first step', async () => {
    const w = mount(SetupApp, { global: { plugins: [i18n] } })
    await flushPromises()
    expect(w.find('h2').text()).toBe(messages.en.language.title)
    const select = w.find('select')
    expect(select.findAll('option').map(o => o.text())).toContain('日本語')
    await select.setValue('ja')
    expect(w.find('h2').text()).toBe(messages.ja.language.title)
    expect(localStorage.getItem('ui-locale')).toBe('ja')
  })

  it('shows localized error codes from the backend', async () => {
    const w = mount(SetupApp, { global: { plugins: [i18n] } })
    await flushPromises()
    await w.get('form').trigger('submit')
    await w.find('input[autocomplete="one-time-code"]').setValue('wrong')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.find('[role="alert"]').text()).toBe(messages.en.errors.invalid_token)
  })

  it('runs the Mastodon source step against /test/mastodon and locks the detected domain', async () => {
    const calls: { url: string; body: Record<string, unknown> }[] = []
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      calls.push({ url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      if (url.endsWith('/status')) return respond(200, { env_file: '/app/config/.env', defaults: {} })
      if (url.endsWith('/verify')) return respond(200, { ok: true })
      if (url.endsWith('/test/mastodon')) return respond(200, { inspection: { counts: { local_users: 2, remote_users: 1, notes: 4, files: 3 }, detected_base_url: 'https://masto.example' } })
      return respond(404, {})
    }))
    const w = mount(SetupApp, { global: { plugins: [i18n] } })
    await flushPromises()
    await w.get('form').trigger('submit')
    await w.find('input[autocomplete="one-time-code"]').setValue('tok')
    await w.get('form').trigger('submit')
    await flushPromises()
    await w.find('input[value="mastodon"]').setValue(true)
    await w.get('form').trigger('submit')
    expect(w.find('h2').text()).toBe(messages.en.mastodon.title)
    expect(w.find('.steps .active').text()).toContain(messages.en.mastodon.step)
    await w.get('form').trigger('submit')
    await flushPromises()
    const req = calls.find(c => c.url.endsWith('/test/mastodon'))!
    expect(req.body.name).toBe('mastodon_production')
    expect(req.body).not.toHaveProperty('id_method')
    expect(w.text()).toContain('2 local accounts, 1 remote accounts, 4 posts and 3 media files')
    expect(w.text()).toContain(messages.en.mastodon.filesPath)
    expect(w.findAll('input').some(i => (i.element as HTMLInputElement).value === '/mastodon/public/system')).toBe(true)
    await w.get('form').trigger('submit')
    const domain = w.find('input[data-field="domain"]')
    expect((domain.element as HTMLInputElement).value).toBe('https://masto.example')
    expect(domain.attributes('readonly')).toBeDefined()
  })

  it('has every key in every locale', () => {
    const keys = (o: object, p = ''): string[] => Object.entries(o).flatMap(([k, v]) => typeof v === 'object' ? keys(v, `${p}${k}.`) : [`${p}${k}`])
    const base = keys(messages['zh-CN']).sort()
    for (const m of Object.values(messages)) expect(keys(m).sort()).toEqual(base)
  })
})
