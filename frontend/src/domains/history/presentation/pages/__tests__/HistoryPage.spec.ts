import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import HistoryPage from '../HistoryPage.vue'
import PostCard from '@/shared/components/post/PostCard.vue'
import { historyApi } from '../../../infrastructure/historyApi'
import type { HistoryItem, MastodonStatus } from '@/shared/types/mastodon'

vi.mock('@/shared/composables/useScrollReturn', () => ({ useScrollReturn: () => ({ onHeaderTap: vi.fn() }) }))
vi.mock('@/domains/post/presentation/composables/useStatusActions', () => ({
  useStatusActions: () => ({ toggleReblog: vi.fn(), toggleFavourite: vi.fn(), toggleBookmark: vi.fn() }),
  applyStatusUpdate: vi.fn(),
}))

function item(id: string, cursor: string): HistoryItem {
  return {
    viewed_at: '2026-11-20T10:00:00Z',
    cursor,
    status: {
      id,
      visibility: 'public',
      account: { id: 'u1', acct: 'alice', username: 'alice' },
      created_at: '2026-11-20T09:00:00Z',
      content: `<p>${id}</p>`,
    } as MastodonStatus,
  }
}

describe('HistoryPage', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('loads browsing history, toggles pause state, and removes a single entry', async () => {
    vi.spyOn(historyApi, 'list').mockResolvedValue([item('101', '1700000000200:101'), item('102', '1700000000100:102')])
    vi.spyOn(historyApi, 'getSettings').mockResolvedValue({ paused: false })
    const updateSettings = vi.spyOn(historyApi, 'updateSettings').mockResolvedValue({ paused: true })
    const remove = vi.spyOn(historyApi, 'remove').mockResolvedValue()

    const wrapper = shallowMount(HistoryPage, { global: { plugins: [createPinia()] } })
    await flushPromises()

    expect(wrapper.findAllComponents(PostCard)).toHaveLength(2)
    expect(wrapper.find('.history-page__banner').exists()).toBe(false)

    // Toggle pause on
    await wrapper.get('.history-page__actions button').trigger('click')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith(true)
    expect(wrapper.find('.history-page__banner').exists()).toBe(true)

    // Remove first entry
    await wrapper.findAll('.history-entry__remove')[0]!.trigger('click')
    await flushPromises()
    expect(remove).toHaveBeenCalledWith('101')
    expect(wrapper.findAllComponents(PostCard)).toHaveLength(1)
    wrapper.unmount()
  })
})
