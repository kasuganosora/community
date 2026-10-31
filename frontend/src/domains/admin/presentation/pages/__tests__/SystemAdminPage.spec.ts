import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SystemAdminPage from '../SystemAdminPage.vue'

const api = vi.hoisted(() => ({
  health: vi.fn(),
  matrixVersions: vi.fn(),
  sitemap: vi.fn(),
  refreshSitemap: vi.fn(),
}))
vi.mock('../../../infrastructure/operationsAdminApi', () => ({ operationsAdminApi: api }))

beforeEach(() => {
  vi.clearAllMocks()
  api.health.mockResolvedValue({ status: 'healthy', timestamp: 't', uptime: '1s', services: { database: { status: 'healthy', latency: '1ms' }, storage: { status: 'healthy' } } })
  api.matrixVersions.mockResolvedValue({ versions: ['1.1'] })
  api.sitemap.mockResolvedValue({ profiles: 0, tags: 0, urls: 0 })
})

describe('system sitemap control', () => {
  it('refreshes the public sitemap from the system page', async () => {
    api.refreshSitemap.mockResolvedValue({ generated_at: '2026-10-31T00:00:00Z', profiles: 2, tags: 3, urls: 7 })
    const wrapper = mount(SystemAdminPage)
    await flushPromises()
    expect(wrapper.get('[data-testid="sitemap-card"]').text()).toContain('尚未生成')
    await wrapper.get('[data-testid="sitemap-refresh"]').trigger('click')
    await flushPromises()
    expect(api.refreshSitemap).toHaveBeenCalledOnce()
    const text = wrapper.get('[data-testid="sitemap-card"]').text()
    expect(text).toContain('站点地图已刷新')
    expect(text).toContain('用户 2')
    expect(text).toContain('话题 3')
    expect(text).toContain('共 7')
    wrapper.unmount()
  })
})
