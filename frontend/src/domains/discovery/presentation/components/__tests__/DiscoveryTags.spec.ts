import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import DiscoveryTags from '../DiscoveryTags.vue'
import { discoveryApi, type TagsPage } from '../../../infrastructure/discoveryApi'
import { sessionEpoch } from '@/shared/api/sessionScope'
vi.mock('@/domains/auth/presentation/stores/authStore', () => ({ useAuthStore: () => ({ currentUser: null }) }))
afterEach(() => vi.restoreAllMocks())
const page: TagsPage = { items: [{ name: '摄影', url: '', posts_count: 17, authors_count: 6, growth: null, growth_state: 'no_previous', new_entry: false }], next_cursor: null, as_of: '2026-10-14T12:00:00Z', checked_at: '2026-10-14T12:00:01Z', window: '24h', scope: 'federated', kind: 'recent', version: '1', coverage_complete: true }
const options = { global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } } }

it('shows global topics without a scope setting and keeps the global scope when changing time windows', async () => {
  const api = vi.spyOn(discoveryApi, 'tags').mockResolvedValue(page)
  const wrapper = mount(DiscoveryTags, options)
  await flushPromises()
  expect(wrapper.text()).toContain('近期话题')
  expect(wrapper.text()).toContain('17 条公开帖 · 6 位作者')
  for (const removed of ['全局（含跨站）', '统计窗口', '内容范围', '包含本站及已接收的跨站公开内容。', '统计截至', '上一窗口暂无可比数据', '近期参与上升']) expect(wrapper.text()).not.toContain(removed)
  expect(wrapper.text()).toContain('刷新话题')
  expect(wrapper.get('select').attributes('aria-label')).toBe('统计窗口')
  expect(api.mock.lastCall?.[0]).toEqual({ window: '24h', scope: 'federated', limit: 20, cursor: undefined })
  expect(wrapper.find('a').attributes('href')).toBe('/tags/%E6%91%84%E5%BD%B1')
  const selects = wrapper.findAll('select')
  expect(selects).toHaveLength(1)
  await selects[0]!.setValue('7d'); await flushPromises()
  expect(api.mock.lastCall?.[0]).toEqual({ window: '7d', scope: 'federated', limit: 20, cursor: undefined })
  wrapper.unmount()
})

it('loads global topics in the compact rail without any settings', async () => {
  const api = vi.spyOn(discoveryApi, 'tags').mockResolvedValue(page)
  const wrapper = mount(DiscoveryTags, { ...options, props: { compact: true } })
  await flushPromises()
  expect(api.mock.lastCall?.[0]).toEqual({ window: '24h', scope: 'federated', limit: 5, cursor: undefined })
  expect(wrapper.text()).not.toContain('全局（含跨站） · 过去 24 小时')
  expect(wrapper.find('select').exists()).toBe(false)
  expect(wrapper.findAll('a').at(-1)?.attributes('href')).toBe('/explore/topics')
  wrapper.unmount()
})

it('clears previous-user tags before accepting a new session response', async () => {
  let finish!: (value: TagsPage) => void
  vi.spyOn(discoveryApi, 'tags').mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValueOnce({ ...page, items: [] })
  const wrapper = mount(DiscoveryTags, options)
  sessionEpoch.value++
  finish(page)
  await flushPromises()
  expect(wrapper.text()).not.toContain('#摄影')
  wrapper.unmount()
})

it('hides the compact rail when trends are disabled and stops retries', async () => {
  const api = vi.spyOn(discoveryApi, 'tags').mockResolvedValue({ ...page, disabled: true, items: [] })
  const wrapper = mount(DiscoveryTags, { ...options, props: { compact: true } })
  await flushPromises()
  expect(wrapper.find('section').exists()).toBe(false)
  expect(api).toHaveBeenCalledTimes(1)
  wrapper.unmount()
})

it('keeps existing topics and their DOM while refreshing, commits together, and retries the failed head', async () => {
  const { invalidateDiscovery } = await import('../../composables/discoveryState')
  let finish!: (value: TagsPage) => void
  let fail!: (reason: unknown) => void
  const api = vi.spyOn(discoveryApi, 'tags').mockResolvedValueOnce(page).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  const wrapper = mount(DiscoveryTags, options)
  await flushPromises()
  const oldLink = wrapper.get('a').element
  invalidateDiscovery()
  await flushPromises()
  expect(wrapper.get('a').element).toBe(oldLink)
  expect(wrapper.find('[role="status"]').exists()).toBe(false)
  expect(wrapper.text()).toContain('#摄影')
  finish({ ...page, items: [{ ...page.items[0]!, name: '新话题' }] })
  await flushPromises()
  expect(wrapper.text()).toContain('#新话题')
  expect(wrapper.text()).not.toContain('#摄影')
  api.mockImplementationOnce(() => new Promise((_resolve, reject) => { fail = reject }))
  invalidateDiscovery(); await flushPromises()
  fail(new Error('offline')); await flushPromises()
  expect(wrapper.text()).toContain('#新话题')
  api.mockResolvedValueOnce(page)
  await wrapper.get('[role="alert"] button').trigger('click')
  await flushPromises()
  expect(api.mock.lastCall?.[0].cursor).toBeUndefined()
  expect(wrapper.text()).toContain('#摄影')
  wrapper.unmount()
})

it('does not clear or re-request fresh topics every time the browser becomes visible', async () => {
  const api = vi.spyOn(discoveryApi, 'tags').mockResolvedValue(page)
  const wrapper = mount(DiscoveryTags, { ...options, props: { compact: true } })
  await flushPromises()
  for (let i = 0; i < 4; i++) document.dispatchEvent(new Event('visibilitychange'))
  await flushPromises()
  expect(api).toHaveBeenCalledTimes(1)
  expect(wrapper.text()).toContain('#摄影')
  wrapper.unmount()
})
