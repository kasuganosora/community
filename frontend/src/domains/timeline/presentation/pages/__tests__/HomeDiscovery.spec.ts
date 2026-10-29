import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import HomePage from '../HomePage.vue'
import { discoveryApi, type DiscoveryPage } from '@/domains/discovery/infrastructure/discoveryApi'
import { sessionEpoch } from '@/shared/api/sessionScope'

const timeline = reactive({ homeTab: 'for-you' as 'for-you' | 'following' | 'global', homeStatuses: [], homeTimeline: { statuses: [], newer: [], isLoading: false, error: null, hasMore: false, hasPrevious: false },
  publicTimeline: { statuses: [] as { id: string }[], newer: [], isLoading: false, error: null, hasMore: false, hasPrevious: false },
  fetchPublicTimeline: vi.fn(), fetchHomeTimeline: vi.fn(), checkNewer: vi.fn(), cancelRevalidation: vi.fn(), updateStatus: vi.fn(), prependStatus: vi.fn(),
  needsInitialLoad: (type: string) => (type === 'public' ? timeline.publicTimeline.statuses : timeline.homeTimeline.statuses).length === 0,
  pendingReveal: null as { id: string, nonce: number } | null })
vi.mock('../../stores/timelineStore', () => ({ useTimelineStore: () => timeline }))
vi.mock('@/shared/composables/useFeedStream', () => ({ useFeedStream: vi.fn() }))
vi.mock('vue-router', async () => ({ ...await vi.importActual<object>('vue-router'), onBeforeRouteLeave: vi.fn() }))
const options = { global: { stubs: { PostComposer: true, TimelineFeed: true, TabBar: true, RouterLink: true } } }
const result: DiscoveryPage = { items: [], next_cursor: null, generation: 'one', mode: 'rules', algorithm: 'rules-v1', preference: 'personalized', preference_revision: 1, disabled: false }
enableAutoUnmount(afterEach)
beforeEach(() => {
  sessionEpoch.value++; timeline.homeTab = 'for-you'; vi.clearAllMocks()
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})
afterEach(() => vi.restoreAllMocks())

it('connects the homepage recommendation tab to discovery, while following keeps its existing home API', async () => {
  vi.spyOn(discoveryApi, 'preferences').mockResolvedValue({ preference: 'personalized', revision: 1, enabled: true })
  const recommend = vi.spyOn(discoveryApi, 'statuses').mockResolvedValue(result)
  const wrapper = mount(HomePage, options)
  await flushPromises()
  expect(recommend).toHaveBeenCalledTimes(1)
  expect(timeline.fetchPublicTimeline).not.toHaveBeenCalled()
  wrapper.findComponent({ name: 'TabBar' }).vm.$emit('update:modelValue', 'following')
  await flushPromises()
  expect(timeline.fetchHomeTimeline).toHaveBeenCalledTimes(1)
  wrapper.unmount()
})

it.each([{ preference: 'off' as const, enabled: true }, { preference: 'generic' as const, enabled: false }])('lands on following when discovery is unavailable: %o', async preference => {
  vi.spyOn(discoveryApi, 'preferences').mockResolvedValue({ ...preference, revision: 1 })
  const recommend = vi.spyOn(discoveryApi, 'statuses')
  const wrapper = mount(HomePage, options)
  await flushPromises()
  expect(timeline.homeTab).toBe('following')
  expect(recommend).not.toHaveBeenCalled()
  expect(timeline.fetchHomeTimeline).toHaveBeenCalledTimes(1)
  wrapper.unmount()
})

it('reuses a fresh recommendation window on tab return and on page visibility instead of refetching', async () => {
  vi.spyOn(discoveryApi, 'preferences').mockResolvedValue({ preference: 'personalized', revision: 1, enabled: true })
  const filled = { ...result, items: [{ status: { id: '1', account: { id: 'a' } } as never }] }
  const recommend = vi.spyOn(discoveryApi, 'statuses').mockResolvedValue(filled)
  const wrapper = mount(HomePage, options)
  await flushPromises()
  expect(recommend).toHaveBeenCalledTimes(1)
  const tabs = wrapper.findComponent({ name: 'TabBar' })
  tabs.vm.$emit('update:modelValue', 'following'); await flushPromises()
  tabs.vm.$emit('update:modelValue', 'for-you'); await flushPromises()
  document.dispatchEvent(new Event('visibilitychange')); await flushPromises()
  expect(recommend).toHaveBeenCalledTimes(1)
  wrapper.unmount()
})

it.each(['head', 'page'] as const)('does not restart an in-flight recommendation %s request when the page becomes visible', async phase => {
  vi.spyOn(discoveryApi, 'preferences').mockResolvedValue({ preference: 'personalized', revision: 1, enabled: true })
  const first = { ...result, items: [{ status: { id: '1', account: { id: 'a' } } as never }], next_cursor: 'more' }
  let finish!: (value: DiscoveryPage) => void
  const recommend = vi.spyOn(discoveryApi, 'statuses')
  if (phase === 'page') recommend.mockResolvedValueOnce(first)
  recommend.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  const wrapper = mount(HomePage, options)
  await flushPromises()
  const feed = wrapper.findComponent({ name: 'TimelineFeed' })
  if (phase === 'page') { feed.vm.$emit('loadMore'); await flushPromises() }
  const expectedCalls = phase === 'page' ? 2 : 1
  const signal = recommend.mock.calls[expectedCalls - 1]![1]!
  document.dispatchEvent(new Event('visibilitychange'))
  await flushPromises()
  expect(recommend).toHaveBeenCalledTimes(expectedCalls)
  expect(signal.aborted).toBe(false)
  finish({ ...result, items: [{ status: { id: '2', account: { id: 'a' } } as never }] })
  await flushPromises()
  expect(feed.props('statuses').map((status: { id: string }) => status.id)).toEqual(phase === 'page' ? ['1', '2'] : ['2'])
  wrapper.unmount()
})

it.each([0, 1800])('restores cached recommendation scroll %i after viewing another home tab', async scroll => {
  vi.spyOn(discoveryApi, 'preferences').mockResolvedValue({ preference: 'personalized', revision: 1, enabled: true })
  const recommend = vi.spyOn(discoveryApi, 'statuses').mockResolvedValue({ ...result, items: [{ status: { id: '1', account: { id: 'a' } } as never }] })
  const position = vi.spyOn(window, 'scrollY', 'get').mockReturnValue(scroll)
  const wrapper = mount(HomePage, options)
  await flushPromises()
  const tabs = wrapper.findComponent({ name: 'TabBar' })
  tabs.vm.$emit('update:modelValue', 'following'); await flushPromises()
  position.mockReturnValue(3400)
  tabs.vm.$emit('update:modelValue', 'for-you'); await flushPromises()
  expect(window.scrollTo).toHaveBeenLastCalledWith(0, scroll)
  expect(recommend).toHaveBeenCalledTimes(1)
  wrapper.unmount()
})
