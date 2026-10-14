import { mount, enableAutoUnmount, flushPromises, RouterLinkStub } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import DiscoveryAdminPage from '../DiscoveryAdminPage.vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { invalidateDiscovery } from '@/domains/discovery/presentation/composables/discoveryState'
const api = vi.hoisted(() => ({ status: vi.fn(), feedback: vi.fn(), suppress: vi.fn(), restore: vi.fn(), approve: vi.fn() }))
vi.mock('../../../infrastructure/discoveryAdminApi', () => ({ discoveryAdminApi: api }))
vi.mock('@/domains/discovery/presentation/composables/discoveryState', () => ({ invalidateDiscovery: vi.fn() }))
const base = { enabled: true, trends_enabled: true, configured_mode: 'auto', algorithm_version: 'rules-v1', approved_version: '', require_manual_approval: false,
  index: { ready: true, reason: 'ready', generation: 'g', version: 'discovery-v1-new', mode: 'meili_lexical', document_count: 3, data_cutoff: '', checked_at: '' } }
const opts = { global: { stubs: { RouterLink: RouterLinkStub } } }
enableAutoUnmount(afterEach)
beforeEach(() => { sessionEpoch.value++; vi.resetAllMocks(); api.feedback.mockResolvedValue([]) })
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
describe('discovery index approval', () => {
  it('shows automatic mode without asking for a version', async () => {
    api.status.mockResolvedValue({ ...base })
    const wrapper = mount(DiscoveryAdminPage, opts); await flushPromises()
    expect(wrapper.get('[data-testid="discovery-approval"]').text()).toContain('自动')
    expect(wrapper.find('[data-testid="discovery-approve"]').exists()).toBe(false)
    expect(api.approve).not.toHaveBeenCalled()
  })
  it('approves the active version with one click under manual review', async () => {
    api.status.mockResolvedValue({ ...base, require_manual_approval: true, approved_version: 'discovery-v1-old', index: { ...base.index, ready: false, reason: 'awaiting_manual_approval' } })
    const wrapper = mount(DiscoveryAdminPage, opts); await flushPromises()
    expect(wrapper.text()).toContain('等待批准当前索引版本')
    expect(wrapper.get('[data-testid="discovery-approval"]').text()).toContain('旧版本')
    api.approve.mockResolvedValue({ approved_version: 'discovery-v1-new' })
    api.status.mockResolvedValue({ ...base, require_manual_approval: true, approved_version: 'discovery-v1-new' })
    await wrapper.get('[data-testid="discovery-approve"]').trigger('click'); await flushPromises()
    expect(api.approve).toHaveBeenCalledWith('discovery-v1-new')
    expect(wrapper.find('[data-testid="discovery-approve"]').exists()).toBe(false)
    expect(wrapper.get('[role="status"]').text()).toContain('已批准')
  })

  it('does not infer automatic approval mode while status is loading or unavailable', async () => {
    const status = deferred<typeof base>()
    api.status.mockReturnValue(status.promise)
    const wrapper = mount(DiscoveryAdminPage, opts)
    expect(wrapper.text()).not.toContain('默认自动模式')
    status.reject(new Error('状态暂不可用')); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('状态暂不可用')
    expect(wrapper.text()).not.toContain('默认自动模式')
  })

  it('keeps a successful approval when the status refresh fails and prevents duplicate approval', async () => {
    api.status.mockResolvedValueOnce({ ...base, require_manual_approval: true }).mockRejectedValueOnce(new Error('刷新失败'))
    const pending = deferred<{ approved_version: string }>()
    api.approve.mockReturnValue(pending.promise)
    const wrapper = mount(DiscoveryAdminPage, opts); await flushPromises()
    const button = wrapper.get('[data-testid="discovery-approve"]')
    await button.trigger('click')
    expect(button.text()).toBe('批准中…')
    expect(button.attributes('disabled')).toBeDefined()
    await button.trigger('click')
    expect(api.approve).toHaveBeenCalledTimes(1)
    pending.resolve({ approved_version: base.index.version }); await flushPromises()
    expect(wrapper.get('[role="status"]').text()).toContain('已批准')
    expect(wrapper.get('[role="alert"]').text()).toContain('刷新失败')
    expect(wrapper.get('[data-testid="discovery-approval"]').text()).toContain('当前版本已批准')
    expect(wrapper.find('[data-testid="discovery-approve"]').exists()).toBe(false)
  })

  it.each(['account', 'unmount'] as const)('does not start follow-up requests when approval completes after %s changes', async change => {
    api.status.mockResolvedValueOnce({ ...base, require_manual_approval: true }).mockResolvedValue({ ...base })
    const pending = deferred<{ approved_version: string }>()
    api.approve.mockReturnValue(pending.promise)
    const wrapper = mount(DiscoveryAdminPage, opts); await flushPromises()
    await wrapper.get('[data-testid="discovery-approve"]').trigger('click')
    if (change === 'account') sessionEpoch.value++
    else wrapper.unmount()
    await flushPromises()
    const reads = api.status.mock.calls.length
    pending.resolve({ approved_version: base.index.version }); await flushPromises()
    expect(api.status).toHaveBeenCalledTimes(reads)
    expect(invalidateDiscovery).not.toHaveBeenCalled()
    if (change === 'account') expect(wrapper.find('[role="status"]').exists()).toBe(false)
  })

  it('does not show the previous account approval after its status refresh finishes', async () => {
    const refreshed = deferred<typeof base>()
    api.status.mockResolvedValueOnce({ ...base, require_manual_approval: true }).mockReturnValueOnce(refreshed.promise).mockResolvedValue({ ...base })
    api.approve.mockResolvedValue({ approved_version: base.index.version })
    const wrapper = mount(DiscoveryAdminPage, opts); await flushPromises()
    await wrapper.get('[data-testid="discovery-approve"]').trigger('click'); await flushPromises()
    expect(api.status).toHaveBeenCalledTimes(2)
    sessionEpoch.value++; await flushPromises()
    refreshed.resolve({ ...base, require_manual_approval: true, approved_version: base.index.version }); await flushPromises()
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="discovery-approval"]').text()).toContain('自动')
  })
})
