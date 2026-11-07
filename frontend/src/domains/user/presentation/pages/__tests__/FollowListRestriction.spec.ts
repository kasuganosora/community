import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import FollowListPage from '../FollowListPage.vue'
import { userApi } from '@/domains/user/infrastructure/userApi'
import type { MastodonAccount } from '@/shared/types/mastodon'

const owner = {
  id: '9',
  username: 'ada',
  acct: 'ada@alias.example',
  display_name: 'Cached Ada',
  note: 'cached biography',
  followers_count: 3,
  following_count: 1,
  emojis: [],
} as unknown as MastodonAccount

function routerFor(kind: 'followers' | 'following') {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: `/@:username/${kind}`, component: FollowListPage, props: { kind } }],
  })
}

async function mountList(kind: 'followers' | 'following' = 'followers') {
  const router = routerFor(kind)
  await router.push(`/@ada@alias.example/${kind}`)
  await router.isReady()
  const wrapper = mount(FollowListPage, {
    props: { kind },
    global: {
      plugins: [createPinia(), router],
      stubs: {
        'router-link': true,
        't-icon': true,
        't-loading': true,
        EmojiText: { props: ['text'], template: '<span>{{ text }}</span>' },
        AccountRow: true,
        TabBar: true,
      },
    },
  })
  await flushPromises()
  return wrapper
}

afterEach(() => vi.restoreAllMocks())

describe('follow list ban status', () => {
  it('does not render or fetch followers for a suspended account or an origin-only node block', async () => {
    const followers = vi.spyOn(userApi, 'getFollowPage').mockResolvedValue({ items: [], nextCursor: undefined })
    vi.spyOn(userApi, 'lookupAccount').mockResolvedValue({ ...owner, suspended: true, availability: { reason: 'account_suspended' } })
    const suspended = await mountList()
    expect(followers).not.toHaveBeenCalled()
    expect(suspended.get('[data-testid="follow-list-restriction"]').text()).toContain('该用户已被封禁')
    expect(suspended.text()).not.toContain('Cached Ada')
    expect(suspended.text()).not.toContain('cached biography')
    suspended.unmount()

    followers.mockClear()
    vi.spyOn(userApi, 'lookupAccount').mockResolvedValue({ ...owner, availability: { reason: 'domain_suspended' } })
    const blocked = await mountList('following')
    expect(followers).not.toHaveBeenCalled()
    expect(blocked.get('[data-testid="follow-list-restriction"]').text()).toContain('该节点已被本站封禁')
    expect(blocked.text()).not.toContain('Cached Ada')
    blocked.unmount()
  })

  it('loads followers again after the account is unsuspended and for a normal profile', async () => {
    const followers = vi.spyOn(userApi, 'getFollowPage').mockResolvedValue({ items: [], nextCursor: undefined })
    const lookup = vi.spyOn(userApi, 'lookupAccount')
      .mockResolvedValueOnce({ ...owner, suspended: true, availability: { reason: 'account_suspended' } })
      .mockResolvedValueOnce({ ...owner })
    const wrapper = await mountList()
    expect(followers).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="follow-list-restriction"] button').trigger('click')
    await flushPromises()
    expect(lookup).toHaveBeenCalledTimes(2)
    expect(followers).toHaveBeenCalledWith('9', 'followers', expect.any(Object))
    expect(wrapper.find('[data-testid="follow-list-restriction"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Cached Ada')
    wrapper.unmount()
  })
})
