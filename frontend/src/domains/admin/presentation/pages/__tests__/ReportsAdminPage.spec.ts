import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import type { MastodonStatus } from '@/shared/types/mastodon'
import ReportsAdminPage from '../ReportsAdminPage.vue'
import { moderationAdminApi as api, type AdminReport } from '../../../infrastructure/moderationAdminApi'
const report: AdminReport = { id: '90071992547409931', category: 'spam', comment: '<img src=x onerror=alert(1)>', action_taken: false, account: null, target_account: null, assigned_account: null, status_ids: ['7'], statuses: [] }
let wrapper: VueWrapper
function button(text: string) { return wrapper.findAll('button').find(b => b.text() === text)! }
beforeEach(() => {
  vi.spyOn(api, 'reports').mockResolvedValue([report])
  vi.spyOn(api, 'report').mockResolvedValue(report)
  vi.spyOn(api, 'reportAction').mockResolvedValue(undefined)
  vi.spyOn(api, 'deleteStatus').mockResolvedValue(undefined)
  vi.spyOn(window, 'confirm').mockReturnValue(true)
})
afterEach(() => { wrapper?.unmount(); vi.restoreAllMocks() })
describe('report administration', () => {
  it('paginates by exact report ID and resets cursor when filters change', async () => {
    const full = Array.from({ length: 40 }, (_, i) => ({ ...report, id: `${9000 - i}` }))
    full[39] = report
    vi.mocked(api.reports).mockResolvedValue(full)
    wrapper = mount(ReportsAdminPage); await flushPromises()
    await button('下一页').trigger('click'); await flushPromises()
    expect(api.reports).toHaveBeenLastCalledWith(expect.objectContaining({ max_id: report.id }))
    await wrapper.get('select').setValue('true')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.reports).toHaveBeenLastCalledWith(expect.objectContaining({ resolved: 'true', max_id: undefined }))
  })
  it('renders report comments as text and missing posts honestly', async () => {
    wrapper = mount(ReportsAdminPage); await flushPromises()
    await button('详情').trigger('click'); await flushPromises()
    expect(wrapper.get('.report-comment').text()).toBe(report.comment)
    expect(wrapper.find('.report-comment img').exists()).toBe(false)
    expect(wrapper.text()).toContain('关联帖子已删除或当前不可读取')
  })
  it('does not resolve without confirmation and preserves state after permission failure', async () => {
    wrapper = mount(ReportsAdminPage); await flushPromises()
    await button('详情').trigger('click'); await flushPromises()
    vi.mocked(window.confirm).mockReturnValue(false)
    await button('标记已处理').trigger('click')
    expect(api.reportAction).not.toHaveBeenCalled()
    vi.mocked(window.confirm).mockReturnValue(true)
    vi.mocked(api.reportAction).mockRejectedValue(new Error('没有处理举报的权限'))
    await button('标记已处理').trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('没有处理举报的权限')
    expect(button('标记已处理')).toBeDefined()
  })
  it('keeps the report and post after cancellation or a failed irreversible delete', async () => {
    const status = { id: '7', content: '<p>待审核内容</p>', visibility: 'public', emojis: [] } as unknown as MastodonStatus
    vi.mocked(api.report).mockResolvedValue({ ...report, statuses: [status] })
    wrapper = mount(ReportsAdminPage, { global: { stubs: { RichText: true } } }); await flushPromises()
    await button('详情').trigger('click'); await flushPromises()
    vi.mocked(window.confirm).mockReturnValue(false)
    await button('删除帖子').trigger('click')
    expect(api.deleteStatus).not.toHaveBeenCalled()
    vi.mocked(window.confirm).mockReturnValue(true)
    vi.mocked(api.deleteStatus).mockRejectedValue(new Error('删除失败'))
    await button('删除帖子').trigger('click'); await flushPromises()
    expect(window.confirm).toHaveBeenLastCalledWith(expect.stringContaining('不可撤销'))
    expect(wrapper.get('[role="alert"]').text()).toContain('删除失败')
    expect(button('删除帖子')).toBeDefined()
    expect(api.reportAction).not.toHaveBeenCalled()
  })
  it('refreshes associated posts after deletion without resolving or removing the report', async () => {
    const status = { id: '7', content: '<p>待审核内容</p>', visibility: 'public', emojis: [] } as unknown as MastodonStatus
    vi.mocked(api.report).mockResolvedValueOnce({ ...report, statuses: [status] }).mockResolvedValue(report)
    wrapper = mount(ReportsAdminPage, { global: { stubs: { RichText: true } } }); await flushPromises()
    await button('详情').trigger('click'); await flushPromises()
    await button('删除帖子').trigger('click'); await flushPromises()
    expect(api.deleteStatus).toHaveBeenCalledWith('7')
    expect(api.reportAction).not.toHaveBeenCalled()
    expect(wrapper.get('[aria-label="举报详情"]').text()).toContain(report.id)
    expect(button('删除帖子')).toBeUndefined()
    expect(button('标记已处理')).toBeDefined()
  })
  it('reloads authoritative state after resolving a report and exposes reopen', async () => {
    wrapper = mount(ReportsAdminPage); await flushPromises()
    await button('详情').trigger('click'); await flushPromises()
    vi.mocked(api.report).mockResolvedValue({ ...report, action_taken: true })
    await button('标记已处理').trigger('click'); await flushPromises()
    expect(api.reportAction).toHaveBeenCalledWith(report.id, 'resolve')
    expect(button('重新打开')).toBeDefined()
  })
})
