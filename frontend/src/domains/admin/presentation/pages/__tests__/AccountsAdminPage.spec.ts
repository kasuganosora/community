import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import AccountsAdminPage from '../AccountsAdminPage.vue'
import { moderationAdminApi as api, type AdminAccount } from '../../../infrastructure/moderationAdminApi'
const account: AdminAccount = { id: '17', username: 'pending_user', domain: null, display_name: '待审核用户', suspended: false, locked: true, user_status: 1 }
let wrapper: VueWrapper
function button(text: string) { return wrapper.findAll('button').find(b => b.text() === text)! }
beforeEach(() => {
  vi.spyOn(api, 'accounts').mockResolvedValue({ items: [account], total: 81 })
  vi.spyOn(api, 'account').mockResolvedValue(account)
  vi.spyOn(api, 'accountAction').mockResolvedValue(undefined)
  vi.spyOn(window, 'confirm').mockReturnValue(true)
})
afterEach(() => { wrapper?.unmount(); vi.restoreAllMocks() })
describe('account administration', () => {
  it('resets pagination on a new filter and does not confuse locked with suspended', async () => {
    wrapper = mount(AccountsAdminPage); await flushPromises()
    expect(wrapper.text()).toContain('待审核')
    await button('下一页').trigger('click'); await flushPromises()
    expect(api.accounts).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 40 }))
    await wrapper.get('input').setValue('new_name')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.accounts).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 0, username: 'new_name' }))
    await button('详情').trigger('click'); await flushPromises()
    expect(button('批准注册')).toBeDefined()
    expect(button('封禁账号')).toBeDefined()
    expect(button('解除封禁')).toBeUndefined()
  })
  it('ignores a late detail response after a new search clears the selection', async () => {
    let finish!: (value: AdminAccount) => void
    vi.mocked(api.account).mockReturnValue(new Promise(resolve => { finish = resolve }))
    wrapper = mount(AccountsAdminPage); await flushPromises()
    await button('详情').trigger('click')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    finish(account); await flushPromises()
    expect(wrapper.find('[aria-label="账号详情"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('正在加载详情')
  })
  it('requires confirmation and retains detail when the server rejects an action', async () => {
    wrapper = mount(AccountsAdminPage); await flushPromises()
    await button('详情').trigger('click'); await flushPromises()
    vi.mocked(window.confirm).mockReturnValue(false)
    await button('拒绝注册').trigger('click')
    expect(api.accountAction).not.toHaveBeenCalled()
    vi.mocked(window.confirm).mockReturnValue(true)
    vi.mocked(api.accountAction).mockRejectedValue(new Error('不能拒绝自己的账号'))
    await button('拒绝注册').trigger('click'); await flushPromises()
    expect(api.accountAction).toHaveBeenCalledWith('17', 'reject')
    expect(wrapper.get('[role="alert"]').text()).toContain('不能拒绝自己的账号')
    expect(wrapper.get('[aria-label="账号详情"]').text()).toContain('@pending_user')
  })
  it('refreshes detail and list after approval instead of optimistically claiming success', async () => {
    wrapper = mount(AccountsAdminPage); await flushPromises()
    await button('详情').trigger('click'); await flushPromises()
    vi.mocked(api.account).mockResolvedValue({ ...account, user_status: 0 })
    await button('批准注册').trigger('click'); await flushPromises()
    expect(api.accountAction).toHaveBeenCalledWith('17', 'approve')
    expect(button('批准注册')).toBeUndefined()
    expect(button('停用登录')).toBeDefined()
    expect(api.accounts).toHaveBeenCalledTimes(2)
  })
})
