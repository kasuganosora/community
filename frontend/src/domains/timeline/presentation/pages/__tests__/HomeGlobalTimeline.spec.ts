import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { defineComponent, type PropType } from 'vue'
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
  expect(wrapper.findComponent({ name: 'TabBar' }).props('options')).toContainEqual({ value: 'global', label: '全局' })
  expect(wrapper.findComponent({ name: 'TabBar' }).props('options').map((o: { value: string }) => o.value)).toEqual(['following', 'for-you', 'global'])

  await select('global')
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/public', expect.objectContaining({ params: { limit: 20 } }))
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
  expect(get).toHaveBeenCalledExactlyOnceWith('/api/v1/timelines/public', expect.objectContaining({ params: { limit: 20 } }))
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
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/public', expect.objectContaining({ params: { max_id: '181', limit: 20 } }))
  expect(store.publicStatuses).toHaveLength(22)

  await select('following')
  expect(feed().props('scopeKey')).toBe('home')
  expect(stream.value).toBe('stream=user')
  await revalidate()
  expect(check).toHaveBeenLastCalledWith('home')
  feed().vm.$emit('loadMore')
  await flushPromises()
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/home', expect.objectContaining({ params: { max_id: '81', limit: 20 } }))
  expect(feed().props('statuses').map((post: MastodonStatus) => post.id)).toEqual(rows(100, 22).map(post => post.id))

  await select('global')
  expect(feed().props('statuses').map((post: MastodonStatus) => post.id)).toEqual(rows(200, 22).map(post => post.id))
  await revalidate()
  expect(check).toHaveBeenLastCalledWith('public')
  feed().vm.$emit('reloadHead')
  await flushPromises()
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/public', expect.objectContaining({ params: { limit: 20 } }))
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
  useTimelineStore().homeTab = 'for-you' // start on discovery so the preference request is pending
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
  expect(get).toHaveBeenLastCalledWith(`/api/v1/timelines/${tab === 'global' ? 'public' : 'home'}`, expect.objectContaining({ params: { limit: 20 } }))
  expect(feed().props('loading')).toBe(true)
  expect(feed().props('error')).toBeNull()
  complete({ data: rows(200, 1) })
  await flushPromises()
  expect(feed().props('loading')).toBe(false)
  expect(feed().props('statuses')[0].id).toBe('200')
})

it('keeps following posts when a tab return re-reads a publication-ordered home timeline', async () => {
  const visible = [
    { id: '240737', created_at: '2026-10-24T12:00:00.000Z', content: 'visible newest', visibility: 'public' },
    { id: '240700', created_at: '2026-10-24T11:00:00.000Z', content: 'visible older', visibility: 'public' },
  ] as MastodonStatus[]
  const arrived = { id: '240800', created_at: '2026-10-24T13:00:00.000Z', content: 'just arrived', visibility: 'public' } as MastodonStatus
  let homeHeads = 0
  vi.spyOn(client, 'get').mockImplementation(async (url, config) => {
    const maxId = (config?.params as { max_id?: string } | undefined)?.max_id
    if (String(url).endsWith('/public')) return { data: [{ id: '9', created_at: '2026-10-24T12:00:00.000Z', content: 'public', visibility: 'public' }] }
    if (!String(url).endsWith('/home')) return { data: [] }
    if (!maxId) {
      homeHeads += 1
      return { data: homeHeads === 1 ? visible : [arrived, ...visible] }
    }
    // id 240738 is the next serial after the loaded head and is older by publication time.
    if (maxId === '240738') return { data: [{ id: '100', created_at: '2026-08-01T00:00:00.000Z', content: 'ancient', visibility: 'public' }] }
    return { data: [] }
  })
  wrapper = mount(HomePage, options)
  await flushPromises()
  expect(feed().props('statuses').map((post: MastodonStatus) => post.id)).toEqual(['240737', '240700'])

  await select('global')
  await select('following')
  const shown = feed().props('statuses').map((post: MastodonStatus) => post.id)
  expect(shown).toEqual(['240737', '240700'])
  expect(feed().props('loading')).toBe(false)
  expect(feed().props('error')).toBeNull()
  expect(wrapper.get('[data-testid="new-posts-pill"]').text()).toContain('查看新帖')
  expect(shown).not.toContain('240800')
})

it('shows a composed status at the top of the open timeline', async () => {
  const revealHead = vi.fn().mockResolvedValue(undefined)
  const TimelineFeed = defineComponent({
    name: 'TimelineFeed',
    props: { statuses: { type: Array as PropType<MastodonStatus[]>, required: true } },
    setup(_, { expose }) { expose({ revealHead }); return () => null },
  })
  vi.spyOn(client, 'get').mockResolvedValue({ data: rows(100, 1) })
  wrapper = mount(HomePage, { global: { stubs: { PostComposer: true, TimelineFeed, TabBar: true, RouterLink: true } } })
  await flushPromises()
  wrapper.findComponent({ name: 'PostComposer' }).vm.$emit('posted', {
    id: '101', visibility: 'public', created_at: '2026-10-26T13:00:00.000Z', content: 'hello',
  })
  await flushPromises()
  expect(feed().props('statuses').map((post: MastodonStatus) => post.id)).toEqual(['101', '100'])
  expect(wrapper.find('[data-testid="new-posts-pill"]').exists()).toBe(false)
  expect(revealHead).toHaveBeenCalled()
})

it('fetches a full public page when global only has the status just composed', async () => {
  const page = [
    { id: '200', created_at: '2026-10-26T12:00:00.000Z', visibility: 'public', content: 'public newest' },
    { id: '199', created_at: '2026-10-26T11:00:00.000Z', visibility: 'public', content: 'public older' },
    { id: '198', created_at: '2026-10-26T10:00:00.000Z', visibility: 'public', content: 'public tail' },
  ] as MastodonStatus[]
  const get = vi.spyOn(client, 'get').mockImplementation(async url => ({
    data: String(url).endsWith('/public') ? page : rows(100, 1),
  }))
  wrapper = mount(HomePage, options)
  await flushPromises()
  wrapper.findComponent({ name: 'PostComposer' }).vm.$emit('posted', {
    id: '101', visibility: 'public', created_at: '2026-10-26T13:00:00.000Z', content: 'mine',
  })
  await flushPromises()
  get.mockClear()
  await select('global')
  expect(get).toHaveBeenCalledWith('/api/v1/timelines/public', expect.objectContaining({ params: { limit: 20 } }))
  expect(get.mock.calls.some(call => (call[1] as { params?: { max_id?: string } } | undefined)?.params?.max_id)).toBe(false)
  expect(feed().props('statuses').map((post: MastodonStatus) => post.id)).toEqual(['101', '200', '199', '198'])
  expect(wrapper.find('[data-testid="new-posts-pill"]').exists()).toBe(false)
  expect(feed().props('loading')).toBe(false)
})

it('fetches a full following page when that tab has no loaded posts', async () => {
  useTimelineStore().homeTab = 'global'
  const get = vi.spyOn(client, 'get').mockImplementation(async url => ({
    data: String(url).endsWith('/home')
      ? [{ id: '100', created_at: '2026-10-26T12:00:00.000Z', visibility: 'public', content: 'home' }]
      : [{ id: '200', created_at: '2026-10-26T12:00:00.000Z', visibility: 'public', content: 'public' }],
  }))
  wrapper = mount(HomePage, options)
  await flushPromises()
  get.mockClear()
  await select('following')
  expect(get).toHaveBeenCalledWith('/api/v1/timelines/home', expect.objectContaining({ params: { limit: 20 } }))
  expect(feed().props('statuses').map((post: MastodonStatus) => post.id)).toEqual(['100'])
  expect(feed().props('scopeKey')).toBe('home')
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
  expect(get).toHaveBeenLastCalledWith('/api/v1/timelines/public', expect.objectContaining({ params: { max_id: '181', limit: 20 } }))
  expect(feed().props('statuses')).toHaveLength(22)
  expect(feed().props('error')).toBeNull()
})
