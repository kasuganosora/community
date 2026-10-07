import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import OverviewAdminPage from '../OverviewAdminPage.vue'
import { dashboardApi } from '../../../infrastructure/dashboardApi'
import { instanceApi } from '@/domains/instance/infrastructure/instanceApi'
vi.mock('../../../infrastructure/dashboardApi', () => ({ dashboardApi: { measures: vi.fn() } }))
vi.mock('@/domains/instance/infrastructure/instanceApi', () => ({ instanceApi: { getInstance: vi.fn() } }))
const options = { global: { stubs: { RouterLink: { template: '<a><slot /></a>' }, TIcon: true } } }
describe('administrator overview', () => {
  it('uses actual totals and subtracts resolved reports', async () => {
    vi.mocked(dashboardApi.measures).mockResolvedValue([
      { key: 'user_count', total: '24', unit: 'users' },
      { key: 'opened_reports', total: '10', unit: 'reports' },
      { key: 'resolved_reports', total: '6', unit: 'reports' },
    ])
    vi.mocked(instanceApi.getInstance).mockResolvedValue({ title: 'Example community', domain: 'social.example', registrations: { enabled: false } })
    const page = mount(OverviewAdminPage, options)
    await flushPromises()
    expect(page.text()).toContain('4 条举报等待处理')
    expect(page.text()).toContain('Example community')
    expect(page.text()).toContain('已关闭')
    expect(page.findAll('.overview-metric > strong').map(node => node.text())).toEqual(['24', '—', '—', '4'])
    page.unmount()
  })
  it('shows unavailable statistics and retries without losing instance information', async () => {
    vi.mocked(dashboardApi.measures).mockRejectedValueOnce(new Error('统计暂时不可用')).mockResolvedValueOnce([{ key: 'user_count', total: '3', unit: 'users' }])
    vi.mocked(instanceApi.getInstance).mockResolvedValue({ title: 'Healthy instance' })
    const page = mount(OverviewAdminPage, options)
    await flushPromises()
    expect(page.get('[role="alert"]').text()).toContain('统计暂时不可用')
    expect(page.text()).toContain('Healthy instance')
    expect(page.findAll('.overview-metric > strong').map(node => node.text())).toEqual(['—', '—', '—', '—'])
    await page.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(page.find('[role="alert"]').exists()).toBe(false)
    expect(page.get('.overview-metric > strong').text()).toBe('3')
    page.unmount()
  })
})
