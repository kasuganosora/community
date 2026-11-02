import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { reactive } from 'vue'
import PostDetailPage from '../PostDetailPage.vue'
import PostCard from '@/shared/components/post/PostCard.vue'
import { postApi } from '../../../infrastructure/postApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useFeedStream, type FeedStreamStatus } from '@/shared/composables/useFeedStream'
import type { MastodonAccount, MastodonStatus } from '@/shared/types/mastodon'

const route = reactive({ params: { statusId: 'a' }, query: {} as Record<string, string>, fullPath: '/status/a' })
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ push: vi.fn(), back: vi.fn() }) }))
vi.mock('@/shared/composables/useUiChrome', () => ({ useUiChrome: () => ({ openQuote: vi.fn() }) }))
vi.mock('@/shared/composables/useFeedStream', () => ({ useFeedStream: vi.fn() }))

const row = (id: string, parent?: string, extra: Partial<MastodonStatus> = {}) => ({
  id, visibility: 'public', account: { id: 'alice', acct: 'alice', username: 'alice' },
  created_at: '2026-10-14T00:00:00Z', replies_count: 0, reblogs_count: 2, favourites_count: 3,
  in_reply_to_id: parent ?? null, reblog: null, content: id, ...extra,
} as MastodonStatus)

function hooks() {
  const call = vi.mocked(useFeedStream).mock.calls.at(-1)!
  return {
    target: call[0],
    revalidate: call[1] as (reason?: string) => Promise<unknown>,
    onStatus: call[2]!.onStatus as (status: FeedStreamStatus) => Promise<void>,
  }
}
function replyCount(wrapper: ReturnType<typeof shallowMount>) {
  return wrapper.findAll('.detail-main__stats strong').at(-1)!.text()
}
function ids(wrapper: ReturnType<typeof shallowMount>) {
  return wrapper.findAllComponents(PostCard).map(card => card.props('status').id as string)
}
const mounted: ReturnType<typeof shallowMount>[] = []
function mountPage(loggedIn = true) {
  const pinia = createPinia()
  setActivePinia(pinia)
  if (loggedIn) useAuthStore().currentUser = { id: 'me', acct: 'me', username: 'me' } as MastodonAccount
  const wrapper = shallowMount(PostDetailPage, { global: { plugins: [pinia], renderStubDefaultSlot: true } })
  mounted.push(wrapper)
  return wrapper
}

describe('live replies on the open thread', () => {
  afterEach(() => {
    mounted.splice(0).forEach(wrapper => wrapper.unmount())
    vi.restoreAllMocks()
    vi.mocked(useFeedStream).mockClear()
    route.params.statusId = 'a'
  })

  it('subscribes a signed-in thread to the home stream and ignores a signed-out one', async () => {
    vi.spyOn(postApi, 'getStatus').mockImplementation(async (id: string) => row(id === 'a' ? 'a' : id, undefined, id === 'a' ? { replies_count: 4 } : {}))
    vi.spyOn(postApi, 'getContext').mockResolvedValue({ ancestors: [], descendants: [] })
    const signedIn = mountPage(true)
    await flushPromises()
    expect(hooks().target.value).toBe('stream=user')
    expect(vi.mocked(useFeedStream).mock.calls.at(-1)![2]).toMatchObject({ poll: false })
    signedIn.unmount()

    const signedOut = mountPage(false)
    await flushPromises()
    expect(hooks().target.value).toBe('')
    signedOut.unmount()
  })

  it('appends a streamed reply under its parent and ignores posts from other threads', async () => {
    const child = row('child', 'a', { replies_count: 1 })
    const grand = row('grand', 'child')
    const sibling = row('sibling', 'a')
    const main = row('a', undefined, { replies_count: 4 })
    const nested = row('900', 'child', { content: '<p>live</p>' })
    const unrelated = row('901', 'elsewhere')
    const direct = row('902', 'a')
    vi.spyOn(postApi, 'getStatus').mockImplementation(async (id: string) => {
      if (id === '900') return nested
      if (id === '901') return unrelated
      if (id === '902') return direct
      return id === 'a' ? main : row(id)
    })
    vi.spyOn(postApi, 'getContext').mockResolvedValue({ ancestors: [], descendants: [child, grand, sibling] })
    const wrapper = mountPage()
    await flushPromises()
    const { onStatus, revalidate } = hooks()

    await onStatus({ event: 'update', id: '900' })
    await onStatus({ event: 'update', id: '901' })
    expect(ids(wrapper)).toEqual(['child', 'grand', '900', 'sibling'])
    expect(wrapper.findAllComponents(PostCard)[0]!.props('status').replies_count).toBe(2)
    expect(replyCount(wrapper)).toBe('4')

    await onStatus({ event: 'update', id: '902' })
    await onStatus({ event: 'update', id: '902' })
    expect(ids(wrapper)).toEqual(['child', 'grand', '900', 'sibling', '902'])
    expect(replyCount(wrapper)).toBe('5')
    expect(postApi.getStatus).toHaveBeenCalledWith('902')
    const replyFetches = vi.mocked(postApi.getStatus).mock.calls.filter(call => call[0] === '902')
    expect(replyFetches).toHaveLength(1)

    await revalidate('poll')
    expect(postApi.getContext).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('does not duplicate a reply the composer already showed', async () => {
    const main = row('a', undefined, { replies_count: 4 })
    vi.spyOn(postApi, 'getStatus').mockImplementation(async (id: string) => id === 'a' ? main : row(id, 'a'))
    vi.spyOn(postApi, 'getContext').mockResolvedValue({ ancestors: [], descendants: [] })
    const wrapper = mountPage()
    await flushPromises()
    wrapper.findComponent({ name: 'PostComposer' }).vm.$emit('posted', row('901', 'a'))
    await hooks().onStatus({ event: 'update', id: '901' })
    expect(ids(wrapper)).toEqual(['901'])
    expect(replyCount(wrapper)).toBe('5')
    expect(vi.mocked(postApi.getStatus).mock.calls.filter(call => call[0] === '901')).toHaveLength(0)
    wrapper.unmount()
  })

  it('drops a reply fetched for the previous thread after navigation', async () => {
    let resolveLate!: (status: MastodonStatus) => void
    const late = new Promise<MastodonStatus>(resolve => { resolveLate = resolve })
    vi.spyOn(postApi, 'getStatus').mockImplementation(async (id: string) => {
      if (id === '900') return late
      return row(id, undefined, id === 'a' ? { replies_count: 4 } : {})
    })
    vi.spyOn(postApi, 'getContext').mockImplementation(async (id: string) => ({
      ancestors: [], descendants: id === 'a' ? [row('child', 'a')] : [],
    }))
    const wrapper = mountPage()
    await flushPromises()
    const pending = hooks().onStatus({ event: 'update', id: '900' })
    route.params.statusId = 'b'
    await flushPromises()
    resolveLate(row('900', 'a'))
    await pending
    await flushPromises()
    expect(ids(wrapper)).toEqual([])
    wrapper.unmount()
  })

  it('inserts replies missed during a socket gap without a routine poll', async () => {
    const child = row('child', 'a')
    const arrived = row('950', 'a')
    const main = row('a', undefined, { replies_count: 4 })
    vi.spyOn(postApi, 'getStatus').mockResolvedValue(main)
    const getContext = vi.spyOn(postApi, 'getContext')
      .mockResolvedValueOnce({ ancestors: [], descendants: [child] })
      .mockResolvedValueOnce({ ancestors: [], descendants: [child, arrived] })
    const wrapper = mountPage()
    await flushPromises()
    const { revalidate } = hooks()
    await revalidate('poll')
    expect(getContext).toHaveBeenCalledTimes(1)
    await revalidate('gap')
    expect(ids(wrapper)).toEqual(['child', '950'])
    expect(replyCount(wrapper)).toBe('5')
    wrapper.unmount()
  })

  it('removes a streamed deletion from the open thread', async () => {
    vi.spyOn(postApi, 'getStatus').mockImplementation(async (id: string) => row(id, undefined, id === 'a' ? { replies_count: 4 } : {}))
    vi.spyOn(postApi, 'getContext').mockResolvedValue({ ancestors: [], descendants: [row('11', 'a'), row('12', 'a')] })
    const wrapper = mountPage()
    await flushPromises()
    hooks().onStatus({ event: 'delete', id: '11' })
    hooks().onStatus({ event: 'delete', id: '999' })
    await flushPromises()
    expect(ids(wrapper)).toEqual(['12'])
    wrapper.unmount()
  })
})
