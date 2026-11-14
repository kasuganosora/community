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
  vi.useRealTimers()
  vi.restoreAllMocks()
})

it('pushes new home posts directly into the timeline without showing a pill', async () => {
  const get = vi.spyOn(client, 'get')
    .mockResolvedValueOnce({ data: rows(100) })
    .mockResolvedValueOnce({ data: rows(101, 2) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  expect(get).toHaveBeenCalledTimes(1)

  useTimelineStore().homeTimeline.newer.push({ id: '101' })
  await flushPromises()
  expect(wrapper.find('[data-testid="new-posts-pill"]').exists()).toBe(false)
  expect(get).toHaveBeenCalledTimes(2)
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/home', expect.objectContaining({ params: { limit: 20 }, timeout: 20_000, signal: expect.any(AbortSignal) }))
  expect(useTimelineStore().homeStatuses.map(status => status.id)).toEqual(['101', '100'])
  expect(useTimelineStore().homeTimeline.newer).toEqual([])
})

it('pushes streamed home posts directly into the timeline', async () => {
  const get = vi.spyOn(client, 'get')
    .mockResolvedValueOnce({ data: rows(100) })
    .mockResolvedValueOnce({ data: rows(101, 2) })
  wrapper = mount(HomePage, options)
  await flushPromises()

  useTimelineStore().applyStreamed('home', { event: 'update', id: '101' })
  await flushPromises()
  expect(wrapper.find('[data-testid="new-posts-pill"]').exists()).toBe(false)
  expect(useTimelineStore().homeStatuses.map(status => status.id)).toEqual(['101', '100'])
  expect(useTimelineStore().homeTimeline.newer).toEqual([])
  expect(get).toHaveBeenCalledTimes(2)
})

it('refreshes the prefetch when a newer post arrives and does not apply the stale page', async () => {
  const get = vi.spyOn(client, 'get').mockResolvedValue({ data: rows(100) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  const first = deferred<{ data: MastodonStatus[] }>()
  get.mockImplementationOnce(() => first.promise)
  useTimelineStore().homeTimeline.newer.push({ id: '101' })
  await nextTick()
  expect(get).toHaveBeenCalledTimes(2)

  const second = deferred<{ data: MastodonStatus[] }>()
  get.mockImplementationOnce(() => second.promise)
  useTimelineStore().homeTimeline.newer.unshift({ id: '102' })
  await nextTick()
  expect(get).toHaveBeenCalledTimes(3)

  first.resolve({ data: rows(101, 2) })
  await flushPromises()
  expect(useTimelineStore().homeStatuses.map(status => status.id)).toEqual(['100'])

  second.resolve({ data: rows(102, 3) })
  await flushPromises()
  expect(get).toHaveBeenCalledTimes(3)
  expect(wrapper.find('[data-testid="new-posts-pill"]').exists()).toBe(false)
  expect(useTimelineStore().homeStatuses.map(status => status.id)).toEqual(['102', '101', '100'])
})

it('pushes new public posts directly into the global timeline', async () => {
  useTimelineStore().homeTab = 'global'
  const get = vi.spyOn(client, 'get')
    .mockResolvedValueOnce({ data: rows(200, 1) })
    .mockResolvedValueOnce({ data: rows(201, 2) })
  wrapper = mount(HomePage, options)
  await flushPromises()

  useTimelineStore().applyStreamed('public', { event: 'update', id: '201' })
  await flushPromises()
  expect(wrapper.find('[data-testid="new-posts-pill"]').exists()).toBe(false)
  expect(get).toHaveBeenCalledTimes(2)
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/public', expect.objectContaining({ params: { limit: 20 }, timeout: 20_000, signal: expect.any(AbortSignal) }))
  expect(useTimelineStore().publicStatuses.map(status => status.id)).toEqual(['201', '200'])
})

it('clears the public timeline loading state when the first page never returns', async () => {
  vi.useFakeTimers()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  useTimelineStore().homeTab = 'global'
  vi.spyOn(client, 'get').mockImplementation(() => new Promise(() => {}))
  wrapper = mount(HomePage, options)
  await flushPromises()
  expect(wrapper.findComponent({ name: 'TimelineFeed' }).props('loading')).toBe(true)
  await vi.advanceTimersByTimeAsync(20_000)
  await flushPromises()
  const feed = wrapper.findComponent({ name: 'TimelineFeed' })
  expect(feed.props('loading')).toBe(false)
  expect(feed.props('error')).toBe('加载超时，点重试')
  vi.useRealTimers()
})
