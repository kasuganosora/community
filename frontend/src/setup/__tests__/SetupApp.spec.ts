import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import SetupApp from '../SetupApp.vue'
import { i18n, applyLocale, messages } from '../i18n'

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
    await w.find('input[value="ja"]').setValue(true)
    expect(w.find('h2').text()).toBe(messages.ja.language.title)
    expect(localStorage.getItem('ui-locale')).toBe('ja')
  })

  it('shows localized error codes from the backend', async () => {
    const w = mount(SetupApp, { global: { plugins: [i18n] } })
    await flushPromises()
    await w.find('.btn--primary').trigger('click')
    await w.find('input[autocomplete="one-time-code"]').setValue('wrong')
    await w.find('.btn--primary').trigger('click')
    await flushPromises()
    expect(w.find('[role="alert"]').text()).toBe(messages.en.errors.invalid_token)
  })

  it('has every key in every locale', () => {
    const keys = (o: object, p = ''): string[] => Object.entries(o).flatMap(([k, v]) => typeof v === 'object' ? keys(v, `${p}${k}.`) : [`${p}${k}`])
    const base = keys(messages['zh-CN']).sort()
    for (const m of Object.values(messages)) expect(keys(m).sort()).toEqual(base)
  })
})
