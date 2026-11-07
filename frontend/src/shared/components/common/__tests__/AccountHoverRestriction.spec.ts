import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import AccountHoverCard from '../AccountHoverCard.vue'
import { closeAccountHover, showAccountHover } from '@/shared/composables/useAccountHover'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import type { MastodonAccount } from '@/shared/types/mastodon'

vi.mock('@/domains/user/infrastructure/userApi', () => ({
  userApi: { lookupCachedAccount: vi.fn(), getRelationships: vi.fn(), follow: vi.fn(), unfollow: vi.fn() },
  profileBanReason: (account: { suspended?: boolean; availability?: { reason?: string } } | null | undefined) => {
    const reason = account?.availability?.reason
    if (reason === 'account_suspended' || reason === 'domain_suspended') return reason
    return account?.suspended ? 'account_suspended' : null
  },
  classifyProfileError: () => null,
  profileRestrictionMessage: (reason: string) => reason === 'domain_suspended' ? '该节点已被本站封禁' : '该用户已被封禁',
}))

const person = {
  id: '9',
  username: 'ada',
  acct: 'ada',
  display_name: 'Cached Ada',
  note: '<p>cached biography</p>',
  avatar: '',
  header: '',
  emojis: [],
  statuses_count: 2,
  following_count: 1,
  followers_count: 4,
} as unknown as MastodonAccount

afterEach(() => {
  closeAccountHover()
  vi.useRealTimers()
  vi.clearAllMocks()
  document.body.innerHTML = ''
  localStorage.removeItem('auth_token')
})

async function open(target: { acct: string; account?: MastodonAccount }) {
  vi.useFakeTimers()
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/@:username', component: { template: '<div />' } }] })
  await router.push('/')
  localStorage.setItem('auth_token', 'fixture')
  const pinia = createPinia()
  const auth = useAuthStore(pinia)
  auth.currentUser = { id: 'me' } as MastodonAccount
  const wrapper = mount(AccountHoverCard, { attachTo: document.body, global: { plugins: [pinia, router] } })
  const anchor = document.createElement('button')
  document.body.append(anchor)
  showAccountHover(anchor, target)
  await vi.advanceTimersByTimeAsync(300)
  await flushPromises()
  return wrapper
}

describe('hover card ban status', () => {
  it('explains a suspended or origin-blocked account and does not load the relationship', async () => {
    vi.mocked(userApi.getRelationships).mockResolvedValue([])
    const suspended = await open({ acct: 'ada', account: { ...person, suspended: true, availability: { reason: 'account_suspended' } } })
    expect(document.querySelector('[data-testid="account-hover-restriction"]')?.textContent).toContain('该用户已被封禁')
    expect(document.body.textContent).not.toContain('cached biography')
    expect(document.body.textContent).not.toContain('Cached Ada')
    expect(userApi.getRelationships).not.toHaveBeenCalled()
    expect(userApi.lookupCachedAccount).not.toHaveBeenCalled()
    suspended.unmount()
    closeAccountHover()

    vi.mocked(userApi.lookupCachedAccount).mockResolvedValue({ ...person, availability: { reason: 'domain_suspended' } })
    const blocked = await open({ acct: 'ada@alias.example' })
    expect(document.querySelector('[data-testid="account-hover-restriction"]')?.textContent).toContain('该节点已被本站封禁')
    expect(document.body.textContent).not.toContain('cached biography')
    expect(userApi.getRelationships).not.toHaveBeenCalled()
    blocked.unmount()
  })

  it('shows a normal profile and loads the relationship when the account is not restricted', async () => {
    vi.mocked(userApi.getRelationships).mockResolvedValue([])
    vi.mocked(userApi.lookupCachedAccount).mockResolvedValue({ ...person })
    const wrapper = await open({ acct: 'ada', account: { ...person } })
    expect(document.querySelector('[data-testid="account-hover-restriction"]')).toBeNull()
    expect(document.body.textContent).toContain('Cached Ada')
    expect(userApi.getRelationships).toHaveBeenCalledWith(['9'], expect.anything())
    wrapper.unmount()
  })
})
