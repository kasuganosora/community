import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import HomePage from '../HomePage.vue'
import { discoveryApi, type DiscoveryPage } from '@/domains/discovery/infrastructure/discoveryApi'
import { sessionEpoch } from '@/shared/api/sessionScope'

const timeline = reactive({ homeTab: 'for-you' as 'for-you' | 'following', homeStatuses: [], homeTimeline: { newer: [], isLoading: false, error: null, hasMore: false, hasPrevious: false },
  fetchPublicTimeline: vi.fn(), fetchHomeTimeline: vi.fn(), checkNewer: vi.fn(), cancelRevalidation: vi.fn(), updateStatus: vi.fn(), prependStatus: vi.fn() })
vi.mock('../../stores/timelineStore', () => ({ useTimelineStore: () => timeline }))
vi.mock('@/shared/composables/useFeedStream', () => ({ useFeedStream: vi.fn() }))
vi.mock('vue-router', async () => ({ ...await vi.importActual<object>('vue-router'), onBeforeRouteLeave: vi.fn() }))
const options = { global: { stubs: { PostComposer: true, TimelineFeed: true, TabBar: true, RouterLink: true } } }
const result: DiscoveryPage = { items: [], next_cursor: null, generation: 'one', mode: 'rules', algorithm: 'rules-v1', preference: 'personalized', preference_revision: 1, disabled: false }
beforeEach(() => { sessionEpoch.value++; timeline.homeTab = 'for-you'; vi.clearAllMocks() })
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
