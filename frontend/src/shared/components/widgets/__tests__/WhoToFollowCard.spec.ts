import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import WhoToFollowCard from '../WhoToFollowCard.vue'
import { suggestionApi } from '@/domains/user/infrastructure/suggestionApi'
import { invalidateDiscovery } from '@/domains/discovery/presentation/composables/discoveryState'
import { sessionEpoch } from '@/shared/api/sessionScope'
import type { MastodonAccount } from '@/shared/types/mastodon'

const { load } = vi.hoisted(() => ({ load: vi.fn().mockResolvedValue(undefined) }))
vi.mock('@/domains/auth/presentation/stores/authStore', () => ({ useAuthStore: () => ({ currentUser: { id: 'me' } }) }))
vi.mock('@/domains/user/presentation/composables/useRelationships', () => ({ useRelationships: () => ({ load, state: () => 'none', pending: new Set(), rels: new Map() }) }))
afterEach(() => { vi.restoreAllMocks(); load.mockReset().mockResolvedValue(undefined) })
const account = (id: string) => ({ id, acct: id, username: id, display_name: id, avatar: '', emojis: [] }) as unknown as MastodonAccount
const options = { global: { stubs: { UserAvatar: true, EmojiText: { props: ['text'], template: '<span>{{ text }}</span>' }, RouterLink: { template: '<a><slot /></a>' } } } }

it('retains the visible accounts through refresh and relationship hydration, and on failure', async () => {
  const api = vi.spyOn(suggestionApi, 'list').mockResolvedValue([{ account: account('old') }] as never)
  const wrapper = mount(WhoToFollowCard, options)
  await flushPromises()
  const row = wrapper.get('.follow-item').element
  let finish!: (value: never) => void
  let hydrate!: () => void
  api.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  load.mockImplementationOnce(() => new Promise<void>(resolve => { hydrate = resolve }))
  invalidateDiscovery(); await flushPromises()
  expect(wrapper.get('.follow-item').element).toBe(row)
  expect(wrapper.find('.follow-card__loading').exists()).toBe(false)
  finish([{ account: account('new') }] as never); await flushPromises()
  expect(wrapper.get('.follow-item').element).toBe(row)
  hydrate(); await flushPromises()
  expect(wrapper.text()).toContain('new')
  expect(wrapper.text()).not.toContain('old')
  vi.spyOn(console, 'error').mockImplementation(() => {})
  api.mockRejectedValueOnce(new Error('offline'))
  invalidateDiscovery(); await flushPromises()
  expect(wrapper.text()).toContain('new')
  expect(wrapper.find('.follow-card__loading').exists()).toBe(false)
  wrapper.unmount()
})

it('clears previous-account suggestions immediately and coalesces the session invalidation', async () => {
  const api = vi.spyOn(suggestionApi, 'list').mockResolvedValueOnce([{ account: account('old') }] as never)
    .mockImplementation(() => new Promise(() => {}))
  const wrapper = mount(WhoToFollowCard, options)
  await flushPromises()
  sessionEpoch.value++
  await flushPromises()
  expect(wrapper.text()).not.toContain('old')
  expect(api).toHaveBeenCalledTimes(2)
  wrapper.unmount()
})
