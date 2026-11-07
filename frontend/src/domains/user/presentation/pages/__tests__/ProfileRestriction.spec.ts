import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { nextTick } from 'vue'
import ProfilePage from '../ProfilePage.vue'
import { clearCachedAccounts, rememberCachedAccount, userApi } from '@/domains/user/infrastructure/userApi'
import type { MastodonAccount } from '@/shared/types/mastodon'

const state = vi.hoisted(() => ({ username: 'ada@blocked.example' }))

vi.mock('vue-router', async () => {
  const { reactive } = await import('vue')
  const route = reactive({ params: { username: state.username }, fullPath: '/@ada@blocked.example' })
  return { useRoute: () => route, __route: route }
})

const cached: MastodonAccount = {
  id: '9',
  username: 'ada',
  acct: 'ada@blocked.example',
  display_name: 'Cached Ada',
  locked: false,
  bot: false,
  discoverable: false,
  indexable: false,
  group: false,
  created_at: '2024-01-01T00:00:00.000Z',
  note: 'cached biography',
  url: 'https://blocked.example/@ada',
  uri: 'https://blocked.example/users/ada',
  avatar: '',
  avatar_static: '',
  header: '',
  header_static: '',
  followers_count: 4,
  following_count: 1,
  statuses_count: 8,
  last_status_at: null,
  hide_collections: false,
  emojis: [],
  fields: [],
}

const healthy: MastodonAccount = { ...cached, id: '3', username: 'helen', acct: 'helen', display_name: 'Helen', note: 'healthy biography', url: '', uri: '' }

function mountPage() {
  vi.spyOn(userApi, 'getStatuses').mockResolvedValue([])
  vi.spyOn(userApi, 'getRelationships').mockResolvedValue([])
  return shallowMount(ProfilePage, {
    global: {
      plugins: [createPinia()],
      stubs: { 'router-link': true, 't-icon': true, EmojiText: { props: ['text'], template: '<span>{{ text }}</span>' } },
    },
  })
}

afterEach(async () => {
  const { __route: route } = await import('vue-router') as unknown as { __route: { params: { username: string } } }
  route.params.username = 'ada@blocked.example'
  state.username = 'ada@blocked.example'
  vi.restoreAllMocks()
  clearCachedAccounts()
})

describe('profile ban explanations', () => {
  it('shows a node ban instead of a cached profile, including while lookup is still running', async () => {
    rememberCachedAccount({ ...cached, availability: { reason: 'domain_suspended' } }, 'ada@blocked.example')
    let release!: (value: MastodonAccount) => void
    vi.spyOn(userApi, 'lookupAccount').mockReturnValue(new Promise((resolve) => { release = resolve }))
    const wrapper = mountPage()
    await nextTick()
    expect(wrapper.find('.profile-page').exists()).toBe(false)
    expect(wrapper.get('[data-testid="profile-restriction"]').text()).toContain('该节点已被本站封禁')
    expect(wrapper.text()).not.toContain('Cached Ada')
    expect(wrapper.text()).not.toContain('cached biography')
    expect(userApi.getStatuses).not.toHaveBeenCalled()
    release({ ...cached, availability: { reason: 'domain_suspended' } })
    await flushPromises()
    expect(wrapper.get('[data-testid="profile-restriction"]').text()).toContain('该节点已被本站封禁')
    expect(userApi.getStatuses).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows an account ban from a 200 payload and clears it after the account is unsuspended', async () => {
    const lookup = vi.spyOn(userApi, 'lookupAccount')
      .mockResolvedValueOnce({ ...cached, id: '4', acct: 'banned', display_name: 'Banned Name', note: 'secret bio', suspended: true, availability: { reason: 'account_suspended' } })
      .mockResolvedValueOnce({ ...healthy, id: '4', username: 'banned', acct: 'banned', display_name: 'Banned Name' })
    state.username = 'banned'
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[data-testid="profile-restriction"]').text()).toContain('该用户已被封禁')
    expect(wrapper.text()).not.toContain('secret bio')
    expect(wrapper.text()).not.toContain('Banned Name')
    expect(userApi.getStatuses).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="profile-restriction"] button').trigger('click')
    await flushPromises()
    expect(lookup).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="profile-restriction"]').exists()).toBe(false)
    expect(wrapper.find('.profile-page').exists()).toBe(true)
    expect(wrapper.text()).toContain('Banned Name')
    wrapper.unmount()
  })

  it('clears the ban when the route changes to a healthy profile', async () => {
    const { __route: route } = await import('vue-router') as unknown as { __route: { params: { username: string } } }
    vi.spyOn(userApi, 'lookupAccount').mockImplementation(async (acct: string) => {
      if (acct === 'helen') return healthy
      return { ...cached, availability: { reason: 'domain_suspended' } }
    })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[data-testid="profile-restriction"]').text()).toContain('该节点已被本站封禁')
    route.params.username = 'helen'
    state.username = 'helen'
    await flushPromises()
    expect(wrapper.find('[data-testid="profile-restriction"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Helen')
    expect(wrapper.text()).not.toContain('该节点已被本站封禁')
    wrapper.unmount()
  })

  it('does not present timeout, fetch failure, not found, 401, or avatar 403 as a ban', async () => {
    const transient: Array<{ error: unknown; text: string }> = [
      { error: Object.assign(new Error('加载超时，点重试'), { code: 'ECONNABORTED' }), text: '加载超时，点重试' },
      { error: Object.assign(new Error('网络连接失败，请检查网络设置'), { code: 'ERR_NETWORK' }), text: '网络连接失败，请检查网络设置' },
    ]
    for (const item of transient) {
      clearCachedAccounts()
      rememberCachedAccount({ ...cached }, 'ada@blocked.example')
      vi.spyOn(userApi, 'lookupAccount').mockRejectedValueOnce(item.error)
      const wrapper = mountPage()
      await flushPromises()
      expect(wrapper.find('[data-testid="profile-restriction"]').exists()).toBe(false)
      expect(wrapper.find('.profile-page').exists()).toBe(true)
      expect(wrapper.get('[data-testid="profile-refresh-error"]').text()).toContain(item.text)
      expect(wrapper.text()).not.toContain('该用户已被封禁')
      expect(wrapper.text()).not.toContain('该节点已被本站封禁')
      wrapper.unmount()
      vi.restoreAllMocks()
    }

    const finalErrors: Array<{ error: unknown; text: string }> = [
      { error: Object.assign(new Error('用户不存在'), { response: { status: 404 } }), text: '用户不存在' },
      { error: Object.assign(new Error('登录已过期，请重新登录'), { response: { status: 401 } }), text: '登录已过期，请重新登录' },
      { error: Object.assign(new Error('没有权限执行此操作'), { response: { status: 403 } }), text: '没有权限执行此操作' },
    ]
    for (const item of finalErrors) {
      clearCachedAccounts()
      rememberCachedAccount({ ...cached }, 'ada@blocked.example')
      vi.spyOn(userApi, 'lookupAccount').mockRejectedValueOnce(item.error)
      const wrapper = mountPage()
      await flushPromises()
      expect(wrapper.find('.profile-page').exists()).toBe(false)
      expect(wrapper.find('[data-testid="profile-restriction"]').exists()).toBe(false)
      expect(wrapper.get('.empty-state__text').text()).toBe(item.text)
      expect(wrapper.text()).not.toContain('该用户已被封禁')
      expect(wrapper.text()).not.toContain('该节点已被本站封禁')
      expect(wrapper.text()).not.toContain('Cached Ada')
      wrapper.unmount()
      vi.restoreAllMocks()
    }
  })
})
