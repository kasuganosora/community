import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { nextTick } from 'vue'
import ProfilePage from '../ProfilePage.vue'
import { clearCachedAccounts, rememberCachedAccount, userApi } from '@/domains/user/infrastructure/userApi'
import type { MastodonAccount } from '@/shared/types/mastodon'

vi.mock('vue-router', () => ({ useRoute: () => ({ params: { username: 'kanna' } }) }))

const kanna: MastodonAccount = {
  id: '1538',
  username: 'kanna',
  acct: 'kanna',
  display_name: 'Kanna',
  locked: false,
  bot: false,
  discoverable: false,
  indexable: false,
  group: false,
  created_at: '2024-01-01T00:00:00.000Z',
  note: '',
  url: '',
  uri: '',
  avatar: '',
  avatar_static: '',
  header: '',
  header_static: '',
  followers_count: 0,
  following_count: 0,
  statuses_count: 3,
  last_status_at: null,
  hide_collections: false,
  emojis: [],
  fields: [],
}

function timeoutError() {
  return Object.assign(new Error('加载超时，点重试'), { code: 'ECONNABORTED' })
}

function mountPage() {
  vi.spyOn(userApi, 'getStatuses').mockResolvedValue([])
  vi.spyOn(userApi, 'getRelationships').mockResolvedValue([])
  return shallowMount(ProfilePage, { global: { plugins: [createPinia()], stubs: { 'router-link': true } } })
}

afterEach(() => {
  vi.restoreAllMocks()
  clearCachedAccounts()
})

describe('local profile timeout', () => {
  it('shows the timeout empty state and keeps manual retry when nothing is cached', async () => {
    const lookup = vi.spyOn(userApi, 'lookupAccount')
      .mockRejectedValueOnce(timeoutError())
      .mockResolvedValueOnce(kanna)
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.profile-page').exists()).toBe(false)
    expect(wrapper.find('.empty-state__title').text()).toBe('无法加载此用户')
    expect(wrapper.find('.empty-state__text').text()).toBe('加载超时，点重试')
    await wrapper.find('.empty-state button').trigger('click')
    await flushPromises()
    expect(lookup).toHaveBeenCalledTimes(2)
    expect(wrapper.find('.profile-page').exists()).toBe(true)
    wrapper.unmount()
  })

  it('keeps a cached local profile on screen while lookup is in flight', async () => {
    rememberCachedAccount(kanna, 'kanna')
    const pending = new Promise<MastodonAccount>(() => {})
    vi.spyOn(userApi, 'lookupAccount').mockReturnValue(pending)
    const wrapper = mountPage()
    await nextTick()
    await flushPromises()
    expect(wrapper.find('.profile-page').exists()).toBe(true)
    expect(wrapper.find('.empty-state').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps the cached profile and a retry action when the client aborts the refetch', async () => {
    rememberCachedAccount(kanna, 'kanna')
    const lookup = vi.spyOn(userApi, 'lookupAccount').mockRejectedValue(timeoutError())
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.profile-page').exists()).toBe(true)
    expect(wrapper.find('.empty-state').exists()).toBe(false)
    const banner = wrapper.get('[data-testid="profile-refresh-error"]')
    expect(banner.text()).toContain('加载超时，点重试')
    await banner.get('button').trigger('click')
    await flushPromises()
    expect(lookup).toHaveBeenCalledTimes(2)
    expect(wrapper.find('.profile-page').exists()).toBe(true)
    wrapper.unmount()
  })

  it('still replaces the page on 404 and 410 when a cached account exists', async () => {
    rememberCachedAccount(kanna, 'kanna')
    vi.spyOn(userApi, 'lookupAccount').mockRejectedValueOnce(Object.assign(new Error('gone'), { response: { status: 410 } }))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.profile-page').exists()).toBe(false)
    expect(wrapper.find('.empty-state__text').text()).toBe('gone')
    wrapper.unmount()

    clearCachedAccounts()
    rememberCachedAccount(kanna, 'kanna')
    vi.spyOn(userApi, 'lookupAccount').mockRejectedValueOnce(Object.assign(new Error('用户不存在'), { response: { status: 404 } }))
    const missing = mountPage()
    await flushPromises()
    expect(missing.find('.profile-page').exists()).toBe(false)
    expect(missing.find('.empty-state__text').text()).toBe('用户不存在')
    missing.unmount()
  })
})
