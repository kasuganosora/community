import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { createPinia, disposePinia, setActivePinia, type Pinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import client from '@/shared/api/client'
import { sessionEpoch } from '@/shared/api/sessionScope'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { useTimelineStore } from '../../stores/timelineStore'
import HomePage from '../HomePage.vue'

vi.mock('@/shared/composables/useFeedStream', () => ({ useFeedStream: vi.fn() }))
vi.mock('vue-router', async () => ({ ...await vi.importActual<object>('vue-router'), onBeforeRouteLeave: vi.fn() }))

let pinia: Pinia
let wrapper: VueWrapper | undefined
const options = { global: { stubs: { PostComposer: true, TimelineFeed: true, TabBar: true, RouterLink: true } } }
const rows = (head: number, count = 1) => Array.from({ length: count }, (_, i) => ({ id: String(head - i), visibility: 'public' }) as MastodonStatus)

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

beforeEach(() => {
  vi.clearAllMocks()
  sessionEpoch.value++
  pinia = createPinia()
  setActivePinia(pinia)
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  window.matchMedia = vi.fn().mockReturnValue({ matches: true }) as unknown as typeof window.matchMedia
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  disposePinia(pinia)
  vi.restoreAllMocks()
})

it('prefetches the home head when the new-posts pill appears, without inserting it', async () => {
  const get = vi.spyOn(client, 'get').mockResolvedValue({ data: rows(100) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  expect(get).toHaveBeenCalledTimes(1)

  useTimelineStore().homeTimeline.newer.push({ id: '101' })
  await flushPromises()
  const pill = wrapper.get('[data-testid="new-posts-pill"]')
  expect(pill.text()).toContain('有新帖子，点击查看')
  expect(pill.text()).not.toContain('加载中')
  expect(get).toHaveBeenCalledTimes(2)
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/home', { params: { limit: 20 } })
  expect(useTimelineStore().homeStatuses.map(status => status.id)).toEqual(['100'])
  expect(useTimelineStore().homeTimeline.newer.map(status => status.id)).toEqual(['101'])
})

it('inserts a ready prefetch on click without another request or a loading wait', async () => {
  const get = vi.spyOn(client, 'get').mockResolvedValue({ data: rows(100) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  const pending = deferred<{ data: MastodonStatus[] }>()
  get.mockImplementationOnce(() => pending.promise)
  useTimelineStore().homeTimeline.newer.push({ id: '101' })
  await nextTick()
  expect(get).toHaveBeenCalledTimes(2)
  pending.resolve({ data: rows(101, 2) })
  await flushPromises()

  const calls = get.mock.calls.length
  const pill = wrapper.get('[data-testid="new-posts-pill"]')
  expect(pill.text()).toContain('有新帖子，点击查看')
  await pill.trigger('click')
  expect(get).toHaveBeenCalledTimes(calls)
  expect(wrapper.find('[data-testid="new-posts-pill"]').exists()).toBe(false)
  expect(wrapper.text()).not.toContain('加载中')
  expect(useTimelineStore().homeStatuses.map(status => status.id)).toEqual(['101', '100'])
})

it('reuses the in-flight prefetch when the pill is clicked and keeps the loading state', async () => {
  const get = vi.spyOn(client, 'get').mockResolvedValue({ data: rows(100) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  const pending = deferred<{ data: MastodonStatus[] }>()
  get.mockImplementation(() => pending.promise)
  useTimelineStore().homeTimeline.newer.push({ id: '101' })
  await nextTick()
  expect(get).toHaveBeenCalledTimes(2)

  const pill = wrapper.get('[data-testid="new-posts-pill"]')
  await pill.trigger('click')
  expect(get).toHaveBeenCalledTimes(2)
  expect(pill.attributes('disabled')).toBeDefined()
  expect(pill.attributes('aria-busy')).toBe('true')
  expect(pill.text()).toContain('加载中…')

  await pill.trigger('click')
  expect(get).toHaveBeenCalledTimes(2)

  pending.resolve({ data: rows(101, 2) })
  await flushPromises()
  expect(wrapper.find('[data-testid="new-posts-pill"]').exists()).toBe(false)
  expect(useTimelineStore().homeStatuses.map(status => status.id)).toEqual(['101', '100'])
})

it('refreshes the prefetch when a newer post arrives and does not apply the stale page', async () => {
  const get = vi.spyOn(client, 'get').mockResolvedValue({ data: rows(100) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  const first = deferred<{ data: MastodonStatus[] }>()
  get.mockImplementationOnce(() => first.promise)
  useTimelineStore().homeTimeline.newer.push({ id: '101' })
  await nextTick()
  const pill = wrapper.get('[data-testid="new-posts-pill"]')
  const click = pill.trigger('click')
  await nextTick()
  expect(get).toHaveBeenCalledTimes(2)
  expect(pill.text()).toContain('加载中…')

  const second = deferred<{ data: MastodonStatus[] }>()
  get.mockImplementationOnce(() => second.promise)
  useTimelineStore().homeTimeline.newer.unshift({ id: '102' })
  await nextTick()
  expect(get).toHaveBeenCalledTimes(3)

  first.resolve({ data: rows(101, 2) })
  await flushPromises()
  expect(useTimelineStore().homeStatuses.map(status => status.id)).toEqual(['100'])
  expect(wrapper.get('[data-testid="new-posts-pill"]').text()).toContain('加载中…')

  second.resolve({ data: rows(102, 3) })
  await click
  await flushPromises()
  expect(get).toHaveBeenCalledTimes(3)
  expect(wrapper.find('[data-testid="new-posts-pill"]').exists()).toBe(false)
  expect(useTimelineStore().homeStatuses.map(status => status.id)).toEqual(['102', '101', '100'])
})

it('loads on click when the prefetch failed, and re-enables the pill if that load fails', async () => {
  vi.spyOn(console, 'error').mockImplementation(() => {})
  const get = vi.spyOn(client, 'get').mockResolvedValue({ data: rows(100) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  get.mockRejectedValueOnce(new Error('预取失败'))
  useTimelineStore().homeTimeline.newer.push({ id: '101' })
  await flushPromises()
  const pill = wrapper.get('[data-testid="new-posts-pill"]')
  expect(pill.text()).toContain('有新帖子，点击查看')
  expect(pill.attributes('disabled')).toBeUndefined()
  expect(useTimelineStore().homeTimeline.error).toBeNull()
  expect(useTimelineStore().homeStatuses.map(status => status.id)).toEqual(['100'])

  const pending = deferred<{ data: MastodonStatus[] }>()
  get.mockImplementationOnce(() => pending.promise)
  await pill.trigger('click')
  expect(get).toHaveBeenCalledTimes(3)
  expect(pill.attributes('aria-busy')).toBe('true')
  expect(pill.text()).toContain('加载中…')

  pending.reject(new Error('刷新失败'))
  await flushPromises()
  const again = wrapper.get('[data-testid="new-posts-pill"]')
  expect(again.attributes('disabled')).toBeUndefined()
  expect(again.text()).toContain('有新帖子，点击查看')
  expect(wrapper.findComponent({ name: 'TimelineFeed' }).props('error')).toBe('刷新失败')
  expect(useTimelineStore().homeTimeline.newer.map(status => status.id)).toEqual(['101'])
})

it('prefetches the public timeline when the global tab pill appears', async () => {
  useTimelineStore().homeTab = 'global'
  const get = vi.spyOn(client, 'get').mockResolvedValue({ data: rows(200, 1) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  useTimelineStore().publicTimeline.newer.push({ id: '201' })
  await nextTick()
  expect(get).toHaveBeenCalledTimes(2)
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/public', { params: { limit: 20 } })
  expect(useTimelineStore().publicStatuses.map(status => status.id)).toEqual(['200'])
  expect(wrapper.get('[data-testid="new-posts-pill"]').text()).toContain('有新帖子，点击查看')
})
