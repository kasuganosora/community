import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createPinia } from 'pinia'
import AccountLink from '../AccountLink.vue'
import AccountHoverCard from '../AccountHoverCard.vue'
import { accountHover, closeAccountHover, showAccountHover } from '@/shared/composables/useAccountHover'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { invalidateSessionData } from '@/shared/api/sessionScope'
import type { MastodonAccount, MastodonRelationship } from '@/shared/types/mastodon'

vi.mock('@/domains/user/infrastructure/userApi', () => ({ userApi: { lookupCachedAccount: vi.fn(), getRelationships: vi.fn(), follow: vi.fn(), unfollow: vi.fn() } }))
const account = { id: 'remote', acct: 'alice@example.test', username: 'alice', display_name: 'Alice', note: '<p>Hello</p>', avatar: '', header: '', emojis: [], statuses_count: 12, following_count: 3, followers_count: 5 } as unknown as MastodonAccount
const rel = { id: account.id, following: false, followed_by: true, requested: false } as MastodonRelationship
let wrapper: VueWrapper | undefined
beforeEach(() => {
  vi.useFakeTimers()
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  vi.mocked(userApi.getRelationships).mockResolvedValue([rel])
  vi.mocked(userApi.follow).mockResolvedValue({ ...rel, following: true })
})
afterEach(() => { wrapper?.unmount(); closeAccountHover(); vi.useRealTimers(); vi.clearAllMocks(); vi.restoreAllMocks(); localStorage.removeItem('auth_token'); vi.unstubAllGlobals(); document.body.innerHTML = '' })
async function setup() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }, { name: 'profile', path: '/@:username', component: { template: '<div />' } }] })
  await router.push('/')
  localStorage.setItem('auth_token', 'fixture')
  const pinia = createPinia()
  const auth = useAuthStore(pinia)
  auth.currentUser = { id: 'me' } as MastodonAccount
  wrapper = mount(defineComponent({ components: { AccountLink, AccountHoverCard }, setup: () => ({ account, accountHover }), template: '<div><AccountLink :account="account">Alice</AccountLink><AccountHoverCard v-if="accountHover" /></div>' }), { attachTo: document.body, global: { plugins: [pinia, router] } })
  return wrapper.get('.account-link')
}
async function open() {
  const link = await setup()
  await link.trigger('pointerenter', { pointerType: 'mouse' })
  await vi.advanceTimersByTimeAsync(300)
  await flushPromises()
  return link
}
describe('account hover interaction', () => {
  it('waits before opening, ignores touch, and keeps ordinary links working', async () => {
    const link = await setup()
    await link.trigger('pointerenter', { pointerType: 'touch' })
    await vi.advanceTimersByTimeAsync(400)
    expect(accountHover.value).toBeNull()
    await link.trigger('pointerenter', { pointerType: 'mouse' })
    await vi.advanceTimersByTimeAsync(200)
    expect(accountHover.value).toBeNull()
    await link.trigger('pointerleave')
    await vi.advanceTimersByTimeAsync(400)
    expect(userApi.getRelationships).not.toHaveBeenCalled()
    expect(link.attributes('href')).toBe('/@alice@example.test')
  })
  it('stays open on entering the card and follows once with the real returned state', async () => {
    const link = await open()
    expect(document.querySelector('#account-hover-card')?.textContent).toContain('Hello')
    await link.trigger('pointerleave')
    document.querySelector('#account-hover-card')!.dispatchEvent(new Event('pointerenter'))
    await vi.advanceTimersByTimeAsync(250)
    expect(accountHover.value).not.toBeNull()
    const button = document.querySelector<HTMLButtonElement>('#account-hover-card button')!
    button.click(); button.click()
    await flushPromises()
    expect(userApi.follow).toHaveBeenCalledTimes(1)
    expect(document.querySelector('#account-hover-card')?.textContent).toContain('6')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()
    expect(accountHover.value).toBeNull()
  })
  it('moves keyboard focus into the card and Escape returns to the trigger', async () => {
    const link = await open()
    ;(link.element as HTMLElement).focus()
    await link.trigger('keydown', { key: 'Tab' })
    expect(document.querySelector('#account-hover-card')?.contains(document.activeElement)).toBe(true)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()
    expect(document.activeElement).toBe(link.element)
    await vi.advanceTimersByTimeAsync(400)
    expect(accountHover.value).toBeNull()
  })
  it('aborts pending profile requests and discards their result after a session change', async () => {
    const link = await setup()
    let resolve!: (account: MastodonAccount) => void
    vi.mocked(userApi.lookupCachedAccount).mockImplementation(() => new Promise(r => { resolve = r }))
    showAccountHover(link.element as HTMLElement, { acct: 'other@example.test' })
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    const signal = vi.mocked(userApi.lookupCachedAccount).mock.calls.at(-1)![1]!
    invalidateSessionData()
    await flushPromises()
    expect(signal.aborted).toBe(true)
    resolve(account)
    await flushPromises()
    expect(document.querySelector('#account-hover-card')).toBeNull()
  })
})
