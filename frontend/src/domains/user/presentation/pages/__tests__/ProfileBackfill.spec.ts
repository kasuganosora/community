import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import ProfilePage from '../ProfilePage.vue'
import { userApi, parseBackfillState } from '@/domains/user/infrastructure/userApi'
import type { MastodonAccount, MastodonStatus } from '@/shared/types/mastodon'

vi.mock('vue-router', () => ({ useRoute: () => ({ params: { username: 'luna@maid.lat' } }) }))

const remote = { id: '7', username: 'luna', acct: 'luna@maid.lat', url: 'https://maid.lat/@luna', followers_count: 1, following_count: 1, statuses_count: 3, emojis: [] } as unknown as MastodonAccount

function mountPage() {
  return mount(ProfilePage, {
    global: {
      plugins: [createPinia()],
      stubs: { TimelineFeed: { props: ['statuses'], template: '<div class="feed">{{ statuses.length }}</div>' }, 't-loading': true, 't-icon': true, 't-button': true, UserAvatar: true, RichText: true, TabBar: true, AddToListButton: true, ReportDialog: true, ProfileMediaGrid: true, EmojiText: true, 'router-link': true },
    },
  })
}

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers() })

describe('remote profile backfill', () => {
  it('parses the header', () => {
    expect(parseBackfillState('pending')).toBe('pending')
    expect(parseBackfillState('bogus')).toBeNull()
    expect(parseBackfillState(undefined)).toBeNull()
  })

  it('shows a loading state while pending and refetches until posts arrive', async () => {
    vi.useFakeTimers()
    vi.spyOn(userApi, 'lookupAccount').mockResolvedValue(remote)
    vi.spyOn(userApi, 'getRelationships').mockResolvedValue([])
    const post = { id: '1' } as MastodonStatus
    const fetch = vi.spyOn(userApi, 'getStatusesWithBackfill')
      .mockResolvedValueOnce({ statuses: [], backfill: 'pending' })
      .mockResolvedValueOnce({ statuses: [post], backfill: 'done' })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('[data-testid="profile-backfill-pending"]').exists()).toBe(true)
    expect(wrapper.find('.feed').exists()).toBe(false)
    await vi.advanceTimersByTimeAsync(2600)
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="profile-backfill-pending"]').exists()).toBe(false)
    expect(wrapper.find('.feed').text()).toBe('1')
    await vi.advanceTimersByTimeAsync(10000)
    expect(fetch).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('stops polling after two retries', async () => {
    vi.useFakeTimers()
    vi.spyOn(userApi, 'lookupAccount').mockResolvedValue(remote)
    vi.spyOn(userApi, 'getRelationships').mockResolvedValue([])
    const fetch = vi.spyOn(userApi, 'getStatusesWithBackfill').mockResolvedValue({ statuses: [], backfill: 'pending' })
    const wrapper = mountPage()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(20000)
    expect(fetch).toHaveBeenCalledTimes(3)
    expect(wrapper.find('[data-testid="profile-backfill-pending"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('links to the remote profile when the outbox is hidden', async () => {
    vi.spyOn(userApi, 'lookupAccount').mockResolvedValue(remote)
    vi.spyOn(userApi, 'getRelationships').mockResolvedValue([])
    vi.spyOn(userApi, 'getStatusesWithBackfill').mockResolvedValue({ statuses: [], backfill: 'unavailable' })
    const wrapper = mountPage()
    await flushPromises()
    const hint = wrapper.find('[data-testid="profile-backfill-unavailable"]')
    expect(hint.exists()).toBe(true)
    const link = hint.find('a')
    expect(link.text()).toBe('去对方站点查看')
    expect(link.attributes('href')).toBe('https://maid.lat/@luna')
    wrapper.unmount()
  })
})
