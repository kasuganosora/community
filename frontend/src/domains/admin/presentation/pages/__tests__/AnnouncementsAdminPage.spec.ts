import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AnnouncementsAdminPage from '../AnnouncementsAdminPage.vue'
import AnnouncementsPage from '../AnnouncementsPage.vue'
import AuditLogsAdminPage from '../AuditLogsAdminPage.vue'
const api = vi.hoisted(() => ({ list: vi.fn(), save: vi.fn(), publish: vi.fn(), remove: vi.fn(), visible: vi.fn(), dismiss: vi.fn(), logs: vi.fn() }))
vi.mock('../../../infrastructure/announcementsAuditApi', () => ({ announcementsAuditApi: api }))
const announcement = { id: '90071992547409931', content: '<script>unsafe</script>', text: '<script>unsafe</script>', published: false, all_day: false, scheduled_at: null, starts_at: null, ends_at: null, updated_at: '2026-10-07T12:00:00Z', read: false }
beforeEach(() => { vi.resetAllMocks(); api.list.mockResolvedValue([{ ...announcement }]); api.visible.mockResolvedValue([{ ...announcement }]); api.logs.mockResolvedValue([]) })
describe('announcement administration', () => {
  it('saves draft without publishing and retains content on failure', async () => {
    const wrapper = mount(AnnouncementsAdminPage); await flushPromises()
    await wrapper.get('textarea').setValue('发布之前的草稿')
    api.save.mockRejectedValueOnce(new Error('storage unavailable'))
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.get('textarea').element.value).toBe('发布之前的草稿')
    expect(wrapper.get('[role="alert"]').text()).toContain('storage unavailable')
    expect(api.publish).not.toHaveBeenCalled()
    api.save.mockResolvedValueOnce(announcement)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({ content: '发布之前的草稿', scheduled_at: null }), undefined)
    expect(wrapper.get('textarea').element.value).toBe('')
  })
  it('requires confirmation for publication/deletion and renders content as text', async () => {
    const wrapper = mount(AnnouncementsAdminPage); await flushPromises()
    expect(wrapper.find('script').exists()).toBe(false)
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    await wrapper.findAll('button').find(b => b.text() === '删除')!.trigger('click')
    expect(api.remove).not.toHaveBeenCalled()
    confirm.mockReturnValue(true)
    await wrapper.findAll('button').find(b => b.text() === '发布')!.trigger('click'); await flushPromises()
    expect(api.publish).toHaveBeenCalledWith(announcement.id, true)
    confirm.mockRestore()
  })
})
describe('user announcements', () => {
  it('failed dismissal keeps unread; successful dismissal persists then removes unread entry', async () => {
    const wrapper = mount(AnnouncementsPage); await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(true); await flushPromises()
    expect(api.visible).toHaveBeenLastCalledWith(true)
    api.dismiss.mockRejectedValueOnce(new Error('retry'))
    await wrapper.findAll('button').find(b => b.text() === '标记已读')!.trigger('click'); await flushPromises()
    expect(wrapper.find('article').exists()).toBe(true)
    api.dismiss.mockResolvedValueOnce(undefined)
    await wrapper.findAll('button').find(b => b.text() === '标记已读')!.trigger('click'); await flushPromises()
    expect(api.dismiss).toHaveBeenLastCalledWith(announcement.id)
    expect(wrapper.find('article').exists()).toBe(false)
    expect(wrapper.text()).toContain('没有未读公告')
  })
})
it('audit cursor preserves large IDs and filters reset pagination', async () => {
  const rows = Array.from({ length: 40 }, (_, i) => ({ id: `${900719925474099310n + BigInt(i)}`, actor_id: '1', account_id: '2', action: 'POST /api/v1/admin/accounts/:id/action', method: 'POST', route: '/api/v1/admin/accounts/:id/action', target_id: '3', outcome: 'pending', status: 0, duration_ms: 0, created_at: '2026-10-07T00:00:00Z' }))
  api.logs.mockResolvedValue(rows)
  const wrapper = mount(AuditLogsAdminPage); await flushPromises()
  expect(wrapper.text()).toContain('结果待确认')
  await wrapper.findAll('button').find(b => b.text() === '下一页')!.trigger('click'); await flushPromises()
  expect(api.logs).toHaveBeenLastCalledWith(expect.objectContaining({ max_id: rows[39]!.id }))
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(api.logs).toHaveBeenLastCalledWith(expect.objectContaining({ max_id: '' }))
})
