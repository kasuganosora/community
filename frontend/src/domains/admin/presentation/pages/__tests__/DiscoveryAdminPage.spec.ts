import { mount, flushPromises, RouterLinkStub } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DiscoveryAdminPage from '../DiscoveryAdminPage.vue'
const api = vi.hoisted(() => ({ status: vi.fn(), feedback: vi.fn(), suppress: vi.fn(), restore: vi.fn(), approve: vi.fn() }))
vi.mock('../../../infrastructure/discoveryAdminApi', () => ({ discoveryAdminApi: api }))
vi.mock('@/domains/discovery/presentation/composables/discoveryState', () => ({ invalidateDiscovery: vi.fn() }))
const base = { enabled: true, trends_enabled: true, configured_mode: 'auto', algorithm_version: 'rules-v1', approved_version: '', require_manual_approval: false,
  index: { ready: true, reason: 'ready', generation: 'g', version: 'discovery-v1-new', mode: 'meili_lexical', document_count: 3, data_cutoff: '', checked_at: '' } }
const opts = { global: { stubs: { RouterLink: RouterLinkStub } } }
beforeEach(() => { vi.resetAllMocks(); api.feedback.mockResolvedValue([]) })
describe('discovery index approval', () => {
  it('shows automatic mode without asking for a version', async () => {
    api.status.mockResolvedValue({ ...base })
    const wrapper = mount(DiscoveryAdminPage, opts); await flushPromises()
    expect(wrapper.get('[data-testid="discovery-approval"]').text()).toContain('自动')
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
})
