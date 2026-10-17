import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { nextTick } from 'vue'
import NotificationsPage from '../NotificationsPage.vue'
import { notificationApi } from '../../../infrastructure/notificationApi'
import type { MastodonNotification } from '@/shared/types/mastodon'

vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
vi.mock('@/domains/post/presentation/composables/useStatusActions', () => ({ useStatusActions: () => ({}), applyStatusUpdate: vi.fn(), editedStatuses: new Map() }))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const notification = (id: string, type: 'follow' | 'mention' = 'follow') => ({
  id, type, created_at: '2026-10-14T00:00:00Z', account: { id, username: id, acct: id },
} as MastodonNotification)
function mountPage() {
  vi.stubGlobal('IntersectionObserver', class { observe() {} disconnect() {} })
  return shallowMount(NotificationsPage)
}
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('notification filter loading', () => {
  beforeEach(() => { setActivePinia(createPinia()) })

  it('prevents duplicate clears and restores the confirmation action after a failed request', async () => {
    const pending = deferred<void>()
    vi.spyOn(notificationApi, 'list').mockResolvedValue([notification('current-follow')])
    const clear = vi.spyOn(notificationApi, 'clear').mockReturnValueOnce(pending.promise).mockResolvedValue(undefined)
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('.notifications-header button').trigger('click')
    const button = wrapper.get('.btn--danger')
    await button.trigger('click')
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.text()).toBe('清空中…')
    await button.trigger('click')
    expect(clear).toHaveBeenCalledTimes(1)
    pending.reject(new Error('clear failed'))
    await flushPromises()
    expect(button.attributes('disabled')).toBeUndefined()
    expect(wrapper.findAll('.notification-item')).toHaveLength(1)
    await button.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.notification-item')).toHaveLength(0)
    expect(wrapper.text()).toContain('暂时没有通知')
    wrapper.unmount()
  })
  it('clears the previous filter immediately and shows retry instead of an empty result on failure', async () => {
    const pending = deferred<MastodonNotification[]>()
    const list = vi.spyOn(notificationApi, 'list')
      .mockResolvedValueOnce([notification('old-follow')])
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValueOnce([notification('new-mention', 'mention')])
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.findAll('.notification-item')).toHaveLength(1)
    wrapper.findComponent({ name: 'TabBar' }).vm.$emit('update:modelValue', 'mention')
    await nextTick()
    expect(wrapper.findAll('.notification-item')).toHaveLength(0)
    expect(wrapper.find('.page-loading').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('暂时没有通知')
    pending.reject(new Error('mentions unavailable'))
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('mentions unavailable')
    expect(wrapper.text()).not.toContain('暂时没有通知')
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ 'types[]': ['mention'] }))
    expect(wrapper.findAll('.notification-item')).toHaveLength(1)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('does not let an old mention response overwrite the current all filter', async () => {
    const pending = deferred<MastodonNotification[]>()
    vi.spyOn(notificationApi, 'list')
      .mockResolvedValueOnce([notification('old-follow')])
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValueOnce([notification('current-follow')])
    const wrapper = mountPage()
    await flushPromises()
    wrapper.findComponent({ name: 'TabBar' }).vm.$emit('update:modelValue', 'mention')
    await nextTick()
    wrapper.findComponent({ name: 'TabBar' }).vm.$emit('update:modelValue', 'all')
    await flushPromises()
    pending.resolve([notification('stale-mention', 'mention')])
    await flushPromises()
    expect(wrapper.get('.notification-body').attributes('aria-label')).toContain('current-follow')
    expect(wrapper.text()).not.toContain('stale-mention')
    wrapper.unmount()
  })
})
