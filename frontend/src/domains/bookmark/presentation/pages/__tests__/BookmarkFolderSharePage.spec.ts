import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { reactive } from 'vue'
import BookmarkFolderSharePage from '../BookmarkFolderSharePage.vue'
import PostCard from '@/shared/components/post/PostCard.vue'
import { bookmarkApi } from '../../../infrastructure/bookmarkApi'
import type { BookmarkFolder, MastodonStatus } from '@/shared/types/mastodon'

const route = reactive({ params: { id: '42' } })
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ push: vi.fn(), back: vi.fn() }) }))
vi.mock('@/domains/post/presentation/composables/useStatusActions', () => ({
  useStatusActions: () => ({ toggleReblog: vi.fn(), toggleFavourite: vi.fn(), toggleBookmark: vi.fn() }),
  applyStatusUpdate: vi.fn(),
}))

const status = (id: string) => ({
  id,
  visibility: 'public',
  account: { id: 'u1', acct: 'alice', username: 'alice' },
  created_at: '2026-11-20T09:00:00Z',
  content: `<p>${id}</p>`,
} as MastodonStatus)

describe('BookmarkFolderSharePage', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    route.params.id = '42'
  })

  it('displays shared folder metadata and paginates statuses via nextMaxId', async () => {
    vi.spyOn(bookmarkApi, 'getFolder').mockResolvedValue({
      id: '42',
      account_id: '1',
      title: 'Go Architecture',
      description: 'Curated engineering threads',
      is_public: true,
      items_count: 2,
      created_at: '2026-11-20T00:00:00Z',
      updated_at: '2026-11-20T00:00:00Z',
    } satisfies BookmarkFolder)
    const listStatuses = vi.spyOn(bookmarkApi, 'listFolderStatuses')
      .mockResolvedValueOnce({ items: [status('s1')], nextCursor: '9' })
      .mockResolvedValueOnce({ items: [status('s2')], nextCursor: undefined })

    const wrapper = shallowMount(BookmarkFolderSharePage, { global: { plugins: [createPinia()] } })
    await flushPromises()

    expect(wrapper.get('.page-header__title').text()).toBe('Go Architecture')
    expect(wrapper.get('.folder-share-page__desc').text()).toBe('Curated engineering threads')
    expect(wrapper.findAllComponents(PostCard)).toHaveLength(1)

    await wrapper.get('.folder-share-page__more button').trigger('click')
    await flushPromises()
    expect(listStatuses).toHaveBeenLastCalledWith('42', { max_id: '9', limit: 40 })
    expect(wrapper.findAllComponents(PostCard)).toHaveLength(2)
    expect(wrapper.find('.folder-share-page__more').exists()).toBe(false)
    wrapper.unmount()
  })
})
