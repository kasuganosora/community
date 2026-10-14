import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createPinia, disposePinia, setActivePinia, type Pinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import client from '@/shared/api/client'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { useFeedStream } from '@/shared/composables/useFeedStream'
import { discoveryApi } from '@/domains/discovery/infrastructure/discoveryApi'
import { discoveryPreference, invalidateDiscovery } from '@/domains/discovery/presentation/composables/discoveryState'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { useTimelineStore } from '../../stores/timelineStore'
import HomePage from '../HomePage.vue'

vi.mock('@/shared/composables/useFeedStream', () => ({ useFeedStream: vi.fn() }))
vi.mock('vue-router', async () => ({ ...await vi.importActual<object>('vue-router'), onBeforeRouteLeave: vi.fn() }))

let pinia: Pinia
let wrapper: VueWrapper | undefined
const options = { global: { stubs: { PostComposer: true, TimelineFeed: true, TabBar: true, RouterLink: true } } }
const rows = (head: number, count = 20) => Array.from({ length: count }, (_, i) => ({ id: String(head - i), visibility: 'public' }) as MastodonStatus)
const feed = () => wrapper!.findComponent({ name: 'TimelineFeed' })
async function select(tab: 'global' | 'following' | 'for-you') {
  wrapper!.findComponent({ name: 'TabBar' }).vm.$emit('update:modelValue', tab)
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  sessionEpoch.value++
  pinia = createPinia()
  setActivePinia(pinia)
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  disposePinia(pinia)
  vi.restoreAllMocks()
})

it.each([{ preference: 'off' as const, enabled: true }, { preference: 'generic' as const, enabled: false }])('offers the original public timeline even when discovery is unavailable: %o', async preference => {
  vi.spyOn(discoveryApi, 'preferences').mockResolvedValue({ ...preference, revision: 1 })
  const discover = vi.spyOn(discoveryApi, 'statuses')
  const get = vi.spyOn(client, 'get').mockImplementation(async url => ({ data: url.endsWith('/public') ? rows(200, 1) : rows(100, 1) }))
  wrapper = mount(HomePage, options)
  await flushPromises()
  expect(useTimelineStore().homeTab).toBe('following')
  expect(wrapper.text()).not.toContain('发现设置')
  expect(wrapper.text()).not.toContain('尚未开放内容发现')
  expect(wrapper.text()).not.toContain('关闭内容发现')
  expect(wrapper.findComponent({ name: 'TabBar' }).props('options')).toContainEqual({ value: 'global', label: '全局时间线' })

  await select('global')
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/public', { params: { limit: 20 } })
  expect(feed().props('statuses').map((post: MastodonStatus) => post.id)).toEqual(['200'])
  expect(feed().props('scopeKey')).toBe('public')
  expect(feed().props('recommendation')).toBe(false)
  expect(wrapper.text()).not.toContain('发现设置')
  expect(discover).not.toHaveBeenCalled()
  invalidateDiscovery({ ...preference, revision: 2 })
  await flushPromises()
  expect(useTimelineStore().homeTab).toBe('global')
})

it('opens a retained global tab without reading recommendation preferences or depending on login', async () => {
  useTimelineStore().homeTab = 'global'
  const preferences = vi.spyOn(discoveryApi, 'preferences')
  const get = vi.spyOn(client, 'get').mockResolvedValue({ data: rows(200, 1) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  expect(discoveryPreference.value).toBeNull()
  expect(preferences).not.toHaveBeenCalled()
  expect(get).toHaveBeenCalledExactlyOnceWith('/api/v1/timelines/public', { params: { limit: 20 } })
  expect(feed().props('statuses')).toHaveLength(1)
})

it('keeps following and global pagination, stream updates and refreshes isolated', async () => {
  const store = useTimelineStore()
  store.homeTab = 'global'
  const check = vi.spyOn(store, 'checkNewer').mockResolvedValue(true)
  const get = vi.spyOn(client, 'get').mockImplementation(async (url, config) => {
    const cursor = (config?.params as { max_id?: string } | undefined)?.max_id
    return { data: url.endsWith('/public') ? cursor ? rows(180, 2) : rows(200) : cursor ? rows(80, 2) : rows(100) }
  })
  wrapper = mount(HomePage, options)
  await flushPromises()
  const [stream, revalidate] = vi.mocked(useFeedStream).mock.calls[0]!
  expect(stream.value).toBe('stream=public')
  feed().vm.$emit('loadMore')
  await flushPromises()
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/public', { params: { max_id: '181', limit: 20 } })
  expect(store.publicStatuses).toHaveLength(22)

  await select('following')
  expect(feed().props('scopeKey')).toBe('home')
  expect(stream.value).toBe('stream=user')
  await revalidate()
  expect(check).toHaveBeenLastCalledWith('home')
  feed().vm.$emit('loadMore')
  await flushPromises()
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/home', { params: { max_id: '81', limit: 20 } })
  expect(feed().props('statuses').map((post: MastodonStatus) => post.id)).toEqual(rows(100, 22).map(post => post.id))

  await select('global')
  expect(feed().props('statuses').map((post: MastodonStatus) => post.id)).toEqual(rows(200, 22).map(post => post.id))
  await revalidate()
  expect(check).toHaveBeenLastCalledWith('public')
  feed().vm.$emit('reloadHead')
  await flushPromises()
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/public', { params: { limit: 20 } })
  expect(store.publicStatuses).toHaveLength(20)
  expect(store.homeStatuses).toHaveLength(22)
})

it('does not display an in-flight global response after switching to following', async () => {
  const store = useTimelineStore()
  store.homeTab = 'global'
  let resolvePublic!: (value: { data: MastodonStatus[] }) => void
  vi.spyOn(client, 'get').mockImplementation(url => url.endsWith('/public')
    ? new Promise(resolve => { resolvePublic = resolve })
    : Promise.resolve({ data: rows(100, 1) }))
  wrapper = mount(HomePage, options)
  await select('following')
  resolvePublic({ data: rows(200, 1) })
  await flushPromises()
  expect(feed().props('statuses').map((post: MastodonStatus) => post.id)).toEqual(['100'])
  expect(feed().props('scopeKey')).toBe('home')
  expect(store.publicStatuses.map(post => post.id)).toEqual(['200'])
})

it.each(['following', 'global'] as const)('does not show discovery notices or reset %s when a disabled preference arrives late', async tab => {
  let resolvePreference!: (value: Awaited<ReturnType<typeof discoveryApi.preferences>>) => void
  vi.spyOn(discoveryApi, 'preferences').mockImplementation(() => new Promise(resolve => { resolvePreference = resolve }))
  const discover = vi.spyOn(discoveryApi, 'statuses')
  vi.spyOn(client, 'get').mockImplementation(async url => ({ data: url.endsWith('/public') ? rows(200, 1) : rows(100, 1) }))
  wrapper = mount(HomePage, options)
  await select(tab)
  resolvePreference({ preference: 'off', enabled: false, revision: 1 })
  await flushPromises()
  expect(useTimelineStore().homeTab).toBe(tab)
  expect(discover).not.toHaveBeenCalled()
  expect(feed().props('recommendation')).toBe(false)
  expect(feed().props('statuses').map((post: MastodonStatus) => post.id)).toEqual([tab === 'global' ? '200' : '100'])
  expect(wrapper.find('.discovery-note').exists()).toBe(false)
})

it.each([
  ['following', 1], ['following', 20], ['global', 1], ['global', 20],
] as const)('retries a failed %s refresh with %i existing rows and immediate loading', async (tab, count) => {
  const store = useTimelineStore()
  store.homeTab = tab
  vi.spyOn(console, 'error').mockImplementation(() => {})
  const get = vi.spyOn(client, 'get').mockResolvedValue({ data: rows(100, count) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  get.mockRejectedValueOnce(new Error('刷新失败'))
  feed().vm.$emit('reloadHead')
  await flushPromises()
  expect(feed().props('error')).toBe('刷新失败')
  expect(feed().props('statuses')).toHaveLength(count)

  let complete!: (value: { data: MastodonStatus[] }) => void
  get.mockImplementationOnce(() => new Promise(resolve => { complete = resolve }))
  feed().vm.$emit('retry')
  feed().vm.$emit('retry')
  await flushPromises()
  expect(get).toHaveBeenCalledTimes(3)
  expect(get).toHaveBeenLastCalledWith(`/api/v1/timelines/${tab === 'global' ? 'public' : 'home'}`, { params: { limit: 20 } })
  expect(feed().props('loading')).toBe(true)
  expect(feed().props('error')).toBeNull()
  complete({ data: rows(200, 1) })
  await flushPromises()
  expect(feed().props('loading')).toBe(false)
  expect(feed().props('statuses')[0].id).toBe('200')
})

it('retries a failed page with its original cursor instead of refreshing the head', async () => {
  const store = useTimelineStore()
  store.homeTab = 'global'
  vi.spyOn(console, 'error').mockImplementation(() => {})
  const get = vi.spyOn(client, 'get').mockResolvedValueOnce({ data: rows(200) })
  wrapper = mount(HomePage, options)
  await flushPromises()
  get.mockRejectedValueOnce(new Error('分页失败'))
  feed().vm.$emit('loadMore')
  await flushPromises()
  get.mockResolvedValueOnce({ data: rows(180, 2) })
  feed().vm.$emit('retry')
  await flushPromises()
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/public', { params: { max_id: '181', limit: 20 } })
  expect(feed().props('statuses')).toHaveLength(22)
  expect(feed().props('error')).toBeNull()
})
