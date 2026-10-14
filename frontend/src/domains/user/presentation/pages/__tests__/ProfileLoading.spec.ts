import { describe, expect, it, vi, afterEach } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { nextTick } from 'vue'
import ProfilePage from '../ProfilePage.vue'
import { userApi } from '@/domains/user/infrastructure/userApi'
import type { MastodonAccount, MastodonStatus } from '@/shared/types/mastodon'

vi.mock('vue-router', () => ({ useRoute: () => ({ params: { username: 'alice' } }) }))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const row = (id: string) => ({ id } as MastodonStatus)
function mountPage() {
  vi.spyOn(userApi, 'lookupAccount').mockResolvedValue({ id: 'alice', username: 'alice', acct: 'alice' } as MastodonAccount)
  vi.spyOn(userApi, 'getRelationships').mockResolvedValue([])
  return shallowMount(ProfilePage, { global: { plugins: [createPinia()] } })
}
afterEach(() => vi.restoreAllMocks())

describe('profile filter loading', () => {
  it('clears old posts immediately, shows the media failure, and retries that query', async () => {
    const pending = deferred<MastodonStatus[]>()
    const getStatuses = vi.spyOn(userApi, 'getStatuses')
      .mockResolvedValueOnce([row('old-post')])
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValueOnce([row('media-post')])
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.findComponent({ name: 'TimelineFeed' }).props('statuses')).toEqual([row('old-post')])
    wrapper.findComponent({ name: 'TabBar' }).vm.$emit('update:modelValue', 'media')
    await nextTick()
    const media = wrapper.findComponent({ name: 'ProfileMediaGrid' })
    expect(media.props('statuses')).toEqual([])
    expect(media.props('loading')).toBe(true)
    pending.reject(new Error('media request failed'))
    await flushPromises()
    expect(media.props('error')).toBe('media request failed')
    expect(media.props('statuses')).toEqual([])
    media.vm.$emit('retry')
    await flushPromises()
    expect(getStatuses).toHaveBeenLastCalledWith('alice', expect.objectContaining({ only_media: true }))
    expect(media.props('statuses')).toEqual([row('media-post')])
    expect(media.props('error')).toBeNull()
    wrapper.unmount()
  })

  it('ignores a slow media response after returning to posts', async () => {
    const pending = deferred<MastodonStatus[]>()
    vi.spyOn(userApi, 'getStatuses')
      .mockResolvedValueOnce([row('old-post')])
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValueOnce([row('current-post')])
    const wrapper = mountPage()
    await flushPromises()
    wrapper.findComponent({ name: 'TabBar' }).vm.$emit('update:modelValue', 'media')
    await nextTick()
    wrapper.findComponent({ name: 'TabBar' }).vm.$emit('update:modelValue', 'posts')
    await flushPromises()
    pending.resolve([row('stale-media')])
    await flushPromises()
    expect(wrapper.findComponent({ name: 'TimelineFeed' }).props('statuses')).toEqual([row('current-post')])
    wrapper.unmount()
  })
})
