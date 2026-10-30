import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import AppealsAdminPage from '../AppealsAdminPage.vue'
import { appealAdminApi, type Appeal } from '../../../infrastructure/appealAdminApi'

const appeal: Appeal = {
  id: '4', account_id: '9', status_id: '15', text: 'this was a mistake', state: 'open', created_at: '2026-01-02T03:04:05Z',
}
let wrapper: VueWrapper
function button(text: string) { return wrapper.findAll('button').find(b => b.text() === text)! }

beforeEach(() => {
  vi.spyOn(appealAdminApi, 'list').mockResolvedValue([appeal])
  vi.spyOn(appealAdminApi, 'resolve').mockResolvedValue({ ...appeal, state: 'accepted', admin_note: 'looks fine' })
  vi.spyOn(window, 'confirm').mockReturnValue(true)
})
afterEach(() => { wrapper?.unmount(); vi.restoreAllMocks() })

describe('suspension appeal administration', () => {
  it('filters open appeals and pages with an offset', async () => {
    const full = Array.from({ length: 40 }, (_, i) => ({ ...appeal, id: String(100 - i) }))
    vi.mocked(appealAdminApi.list).mockResolvedValue(full)
    wrapper = mount(AppealsAdminPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    expect(appealAdminApi.list).toHaveBeenCalledWith({ state: 'open', limit: 40, offset: 0 })
    await button('下一页').trigger('click')
    await flushPromises()
    expect(appealAdminApi.list).toHaveBeenLastCalledWith({ state: 'open', limit: 40, offset: 40 })
    await wrapper.get('select').setValue('accepted')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(appealAdminApi.list).toHaveBeenLastCalledWith({ state: 'accepted', limit: 40, offset: 0 })
  })

  it('accepts only after confirmation and keeps the decision separate from unsuspend', async () => {
    wrapper = mount(AppealsAdminPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    await button('详情').trigger('click')
    vi.mocked(window.confirm).mockReturnValue(false)
    await button('接受').trigger('click')
    expect(appealAdminApi.resolve).not.toHaveBeenCalled()
    vi.mocked(window.confirm).mockReturnValue(true)
    await wrapper.get('textarea').setValue('looks fine')
    await button('接受').trigger('click')
    await flushPromises()
    expect(window.confirm).toHaveBeenLastCalledWith(expect.stringContaining('不会解除封禁'))
    expect(appealAdminApi.resolve).toHaveBeenCalledWith('4', true, 'looks fine')
    expect(wrapper.text()).toContain('要恢复登录，请到用户管理里解除封禁。')
    expect(wrapper.text()).toContain('已接受')
  })

  it('shows a rejection failure without dropping the appeal', async () => {
    wrapper = mount(AppealsAdminPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    await button('详情').trigger('click')
    vi.mocked(appealAdminApi.resolve).mockRejectedValue(new Error('没有处理权限'))
    await button('拒绝').trigger('click')
    await flushPromises()
    expect(appealAdminApi.resolve).toHaveBeenCalledWith('4', false, '')
    expect(wrapper.get('[role="alert"]').text()).toContain('没有处理权限')
    expect(button('拒绝')).toBeDefined()
  })
})
