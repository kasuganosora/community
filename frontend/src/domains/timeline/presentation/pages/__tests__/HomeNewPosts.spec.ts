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

it('shows loading on the new-posts pill until the home timeline refresh finishes', async () => {
  const get = vi.spyOn(client, 'get').mockResolvedValue({ data: rows(100) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  useTimelineStore().homeTimeline.newer.push({ id: '101' })
  await nextTick()
  const pill = wrapper.get('[data-testid="new-posts-pill"]')
  expect(pill.text()).toContain('有新帖子，点击查看')

  const pending = deferred<{ data: MastodonStatus[] }>()
  get.mockImplementation(() => pending.promise)
  await pill.trigger('click')
  expect(pill.attributes('disabled')).toBeDefined()
  expect(pill.attributes('aria-busy')).toBe('true')
  expect(pill.text()).toContain('加载中…')
  expect(get).toHaveBeenCalledTimes(2)

  await pill.trigger('click')
  expect(get).toHaveBeenCalledTimes(2)

  pending.resolve({ data: rows(101, 2) })
  await flushPromises()
  expect(wrapper.find('[data-testid="new-posts-pill"]').exists()).toBe(false)
  expect(useTimelineStore().homeStatuses.map(status => status.id)).toEqual(['101', '100'])
})

it('re-enables the new-posts pill when the home refresh fails', async () => {
  vi.spyOn(console, 'error').mockImplementation(() => {})
  const get = vi.spyOn(client, 'get').mockResolvedValue({ data: rows(100) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  useTimelineStore().homeTimeline.newer.push({ id: '101' })
  await nextTick()
  const pending = deferred<{ data: MastodonStatus[] }>()
  get.mockImplementation(() => pending.promise)
  const pill = wrapper.get('[data-testid="new-posts-pill"]')
  await pill.trigger('click')
  expect(pill.text()).toContain('加载中…')

  pending.reject(new Error('刷新失败'))
  await flushPromises()
  const again = wrapper.get('[data-testid="new-posts-pill"]')
  expect(again.attributes('disabled')).toBeUndefined()
  expect(again.text()).toContain('有新帖子，点击查看')
  expect(wrapper.findComponent({ name: 'TimelineFeed' }).props('error')).toBe('刷新失败')
  expect(useTimelineStore().homeTimeline.newer.map(status => status.id)).toEqual(['101'])
})
