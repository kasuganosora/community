import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import AnalyticsOverview from '../AnalyticsOverview.vue'
import { analyticsAdminApi, type AdminAnalytics } from '../../../infrastructure/analyticsAdminApi'
vi.mock('../../../infrastructure/analyticsAdminApi', () => ({ analyticsAdminApi: { get: vi.fn() } }))
function fixture(): AdminAnalytics {
  return { generated_at: '2026-10-07T12:00:00Z', timezone: 'UTC', days: 30, totals: { local_accounts: 1250, remote_accounts: 42, local_posts: 90, remote_posts: 80, instances: 5, local_emojis: 3, remote_emojis: 4 }, daily: Array.from({ length: 180 }, (_, i) => ({ date: new Date(Date.UTC(2026, 3, 11 + i)).toISOString().slice(0, 10), local_posts: i, remote_posts: 2, posting_accounts: 1 })), cohorts: [{ date: '2026-09-21', size: 2, retained: 1 }], federation: { outgoing: [{ domain: 'remote.example', count: 3 }], incoming: [] }, online: { available: false, reason: 'Not recorded' } }
}
describe('analytics overview', () => {
  it('renders real totals, bounded trend selection, heatmap and accessible exact data', async () => {
    vi.mocked(analyticsAdminApi.get).mockResolvedValue(fixture())
    const page = mount(AnalyticsOverview); await flushPromises()
    expect(page.text()).toContain('1,250')
    expect(page.text()).toContain('不等于在线或登录人数')
    expect(page.text()).toContain('50.0%')
    expect(page.text()).toContain('remote.example')
    expect(page.findAll('svg rect')).toHaveLength(180)
    expect(page.findAll('svg circle')).toHaveLength(30)
    await page.findAll('select')[0]!.setValue('7')
    expect(page.findAll('svg circle')).toHaveLength(7)
    await page.findAll('svg circle')[0]!.trigger('focus')
    expect(page.get('[aria-live="polite"]').text()).toContain('本地帖子 173')
    expect(page.findAll('table').length).toBeGreaterThanOrEqual(4)
    page.unmount()
  })
  it('does not replace failed requests with fabricated zero charts and supports retry', async () => {
    vi.mocked(analyticsAdminApi.get).mockRejectedValueOnce(new Error('无法读取数据库')).mockResolvedValueOnce(fixture())
    const page = mount(AnalyticsOverview); await flushPromises()
    expect(page.get('[role="alert"]').text()).toContain('无法读取数据库')
    expect(page.find('svg').exists()).toBe(false)
    expect(page.find('.analytics-totals').exists()).toBe(false)
    await page.get('[role="alert"] button').trigger('click'); await flushPromises()
    expect(page.find('[role="alert"]').exists()).toBe(false)
    expect(page.find('svg').exists()).toBe(true)
    page.unmount()
  })
})
