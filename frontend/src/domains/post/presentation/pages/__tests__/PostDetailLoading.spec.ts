import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { reactive } from 'vue'
import PostDetailPage from '../PostDetailPage.vue'
import { postApi } from '../../../infrastructure/postApi'
import type { MastodonStatus } from '@/shared/types/mastodon'

const route = reactive({ params: { statusId: 'a' }, query: {} })
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ push: vi.fn(), back: vi.fn() }) }))
vi.mock('@/shared/composables/useUiChrome', () => ({ useUiChrome: () => ({ openQuote: vi.fn() }) }))
vi.mock('@/domains/post/presentation/composables/useStatusActions', () => ({
  useStatusActions: () => ({}), applyStatusUpdate: vi.fn(), editedStatuses: new Map(), deletedStatusIds: new Set(),
}))
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const row = (id: string) => ({
  id, visibility: 'public', account: { id: 'alice', acct: 'alice', username: 'alice' },
  created_at: '2026-10-14T00:00:00Z', replies_count: 0,
} as MastodonStatus)
type Context = { ancestors: MastodonStatus[]; descendants: MastodonStatus[] }
function mountPage() { return shallowMount(PostDetailPage, { global: { plugins: [createPinia()] } }) }
afterEach(() => { vi.restoreAllMocks(); route.params.statusId = 'a' })

describe('post detail partial loading', () => {
  it('shows the post before replies finish and retries only a failed reply request', async () => {
    const pending = deferred<Context>()
    const getStatus = vi.spyOn(postApi, 'getStatus').mockResolvedValue(row('a'))
    const getContext = vi.spyOn(postApi, 'getContext').mockReturnValueOnce(pending.promise)
      .mockResolvedValueOnce({ ancestors: [], descendants: [row('reply')] })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.detail-main').exists()).toBe(true)
    expect(wrapper.find('[aria-label="正在加载回复"]').exists()).toBe(true)
    pending.reject(new Error('replies offline'))
    await flushPromises()
    expect(wrapper.find('.detail-main').exists()).toBe(true)
    expect(wrapper.get('.detail-context-error').text()).toContain('replies offline')
    await wrapper.get('.detail-context-error button').trigger('click')
    await flushPromises()
    expect(getContext).toHaveBeenCalledTimes(2)
    expect(getStatus).toHaveBeenCalledTimes(1)
    expect(wrapper.findComponent({ name: 'PostCard' }).props('status').id).toBe('reply')
    wrapper.unmount()
  })

  it('keeps a reply sent while context is pending and deduplicates it', async () => {
    const pending = deferred<Context>()
    vi.spyOn(postApi, 'getStatus').mockResolvedValue(row('a'))
    vi.spyOn(postApi, 'getContext').mockReturnValue(pending.promise)
    const wrapper = mountPage()
    await flushPromises()
    wrapper.findComponent({ name: 'PostComposer' }).vm.$emit('posted', row('local-reply'))
    pending.resolve({ ancestors: [], descendants: [row('existing-reply'), row('local-reply')] })
    await flushPromises()
    expect(wrapper.findAllComponents({ name: 'PostCard' }).map(card => card.props('status').id)).toEqual(['existing-reply', 'local-reply'])
    wrapper.unmount()
  })

  it('hides the old post when a new post is forbidden even if its context succeeds', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(postApi, 'getStatus').mockResolvedValueOnce(row('a'))
      .mockRejectedValueOnce({ response: { status: 403 } })
    vi.spyOn(postApi, 'getContext').mockResolvedValue({ ancestors: [], descendants: [row('reply')] })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.detail-main').exists()).toBe(true)
    route.params.statusId = 'forbidden'
    await flushPromises()
    expect(wrapper.find('.detail-main').exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'PostCard' }).exists()).toBe(false)
    expect(wrapper.text()).toContain('你没有权限查看这条帖子')
    expect(wrapper.get('[role="alert"] button').text()).toBe('重试')
    wrapper.unmount()
  })
})
