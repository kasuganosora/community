import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import RolesAdminPage from '../RolesAdminPage.vue'
import InvitesAdminPage from '../InvitesAdminPage.vue'
import { accessAdminApi as api } from '../../../infrastructure/accessAdminApi'
afterEach(() => vi.restoreAllMocks())
describe('roles and invitations management', () => {
  it('retains a failed role draft with supported permissions', async () => {
    vi.spyOn(api, 'roles').mockResolvedValue({ items: [], total: 0, available_permissions: ['dashboard', 'invites'] })
    const save = vi.spyOn(api, 'saveRole').mockRejectedValue(new Error('denied'))
    const page = mount(RolesAdminPage); await flushPromises()
    await page.get('input[maxlength="20"]').setValue('邀请协作者'); await page.get('input[value="invites"]').setValue(true)
    await page.get('form[aria-label="角色编辑"]').trigger('submit'); await flushPromises()
    expect(save).toHaveBeenCalledWith(null, { name: '邀请协作者', color: '', highlighted: false, permissions: ['invites'] })
    expect(page.get('input[maxlength="20"]').element).toHaveProperty('value', '邀请协作者')
    expect(page.get('[role=alert]').text()).toContain('输入内容已保留')
    expect(page.find('input[value="administrator"]').exists()).toBe(false); page.unmount()
  })
  it('requires confirmation and retains a role on deletion failure', async () => {
    vi.spyOn(api, 'roles').mockResolvedValue({ items: [{ id: '3', name: '受限角色', color: '', highlighted: false, permissions: ['dashboard'], protected: false }], total: 1, available_permissions: ['dashboard'] })
    const remove = vi.spyOn(api, 'deleteRole').mockRejectedValue(new Error('denied')); const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const page = mount(RolesAdminPage); await flushPromises(); const button = page.findAll('button').find(b => b.text() === '删除')!
    await button.trigger('click'); expect(remove).not.toHaveBeenCalled(); confirm.mockReturnValue(true); await button.trigger('click'); await flushPromises()
    expect(page.text()).toContain('受限角色'); expect(page.get('[role=alert]').text()).toContain('删除失败'); page.unmount()
  })
  it('creates finite invites and requires confirmation to disable', async () => {
    vi.spyOn(api, 'invites').mockResolvedValue({ items: [{ id: '1', code: 'test-invite', account_id: '4', uses: 0, max_uses: 1, expires_at: null, created_at: '', disabled: false, available: true }], total: 1 })
    const create = vi.spyOn(api, 'createInvites').mockRejectedValue(new Error('offline')); const disable = vi.spyOn(api, 'setInviteDisabled')
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    const page = mount(InvitesAdminPage); await flushPromises(); await page.get('form[aria-label="生成邀请"]').trigger('submit'); await flushPromises()
    expect(create).toHaveBeenCalledWith({ count: 1, max_uses: 1, expires_in: 604800 }); expect(page.get('[role=alert]').text()).toContain('设置已保留')
    await page.findAll('button').find(b => b.text() === '停用')!.trigger('click'); expect(disable).not.toHaveBeenCalled(); page.unmount()
  })
})
