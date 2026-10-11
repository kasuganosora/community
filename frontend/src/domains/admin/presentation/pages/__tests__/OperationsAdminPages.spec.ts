import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SettingsAdminPage from '../SettingsAdminPage.vue'
import FederationAdminPage from '../FederationAdminPage.vue'
import SystemAdminPage from '../SystemAdminPage.vue'
import { operationsAdminApi as api } from '../../../infrastructure/operationsAdminApi'
beforeEach(() => {
  vi.spyOn(api, 'health').mockResolvedValue({ status: 'healthy', timestamp: '2026-10-07T13:00:00Z', uptime: '1h', services: { database: { status: 'healthy', latency: '1ms' }, storage: { status: 'degraded', latency: '2ms' } } })
  vi.spyOn(api, 'matrixVersions').mockResolvedValue({ versions: ['v1.11'] })
})
afterEach(() => vi.restoreAllMocks())
describe('administrative operation recovery', () => {
  it('keeps a failed rule draft and never renders or submits a stored secret', async () => {
    vi.spyOn(api, 'rules').mockResolvedValue([])
    vi.spyOn(api, 'captcha').mockResolvedValue({ enabled: false, provider: 'hcaptcha', site_key: 'public' })
    const save = vi.spyOn(api, 'saveRule').mockRejectedValue(new Error('unavailable'))
    const captcha = vi.spyOn(api, 'saveCaptcha').mockResolvedValue()
    const w = mount(SettingsAdminPage); await flushPromises()
    expect(w.get('input[type=password]').element).toHaveProperty('value', '')
    await w.get('textarea').setValue('待保存的规则')
    await w.findAll('form')[0]!.trigger('submit'); await flushPromises()
    expect(save).toHaveBeenCalledWith(null, '待保存的规则')
    expect(w.get('textarea').element).toHaveProperty('value', '待保存的规则')
    expect(w.get('[role=alert]').text()).toContain('输入内容已保留')
    await w.get('input[type=checkbox]').setValue(true)
    await w.findAll('form')[1]!.trigger('submit'); await flushPromises()
    expect(captcha).toHaveBeenCalledWith({ enabled: true, provider: 'hcaptcha', site_key: 'public' }, '')
    w.unmount()
  })
  it('does not remove a relay without confirmation and preserves it on server failure', async () => {
    vi.spyOn(api, 'relays').mockResolvedValue([{ id: 2, inbox_url: 'https://relay.example/inbox', state: 'accepted', created_at: '', updated_at: '' }])
    const action = vi.spyOn(api, 'relayAction').mockRejectedValue(new Error('offline'))
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const w = mount(FederationAdminPage); await flushPromises()
    const remove = w.findAll('button').find(x => x.text() === '删除')!
    await remove.trigger('click'); expect(action).not.toHaveBeenCalled()
    confirm.mockReturnValue(true); await remove.trigger('click'); await flushPromises()
    expect(w.get('[role=alert]').text()).toContain('删除失败')
    expect(w.text()).toContain('https://relay.example/inbox')
    w.unmount()
  })
  it('loads native Matrix and dependency health without the optional external bridge', async () => {
    const status = vi.spyOn(api, 'matrixStatus').mockRejectedValue(new Error('bridge unavailable'))
    const config = vi.spyOn(api, 'matrixConfig').mockRejectedValue(new Error('bridge unavailable'))
    const w = mount(SystemAdminPage); await flushPromises()
    expect(w.get('[aria-label="内置 Matrix 服务"]').text()).toContain('客户端接口可访问')
    expect(w.get('[aria-label="内置 Matrix 服务"]').text()).toContain('v1.11')
    expect(w.get('[aria-label="系统依赖"]').text()).toContain('降级')
    expect(status).not.toHaveBeenCalled()
    expect(config).not.toHaveBeenCalled()
    expect(w.find('[role=alert]').exists()).toBe(false)
    expect(w.text()).not.toMatch(/Homeserver|桥接账号|未配置或未启用|测试用户连接/)
    await w.findAll('button').find(button => button.text() === '检查客户端接口')!.trigger('click')
    await flushPromises()
    expect(api.matrixVersions).toHaveBeenCalledTimes(2)
    w.unmount()
  })

  it('does not require rule privileges to load captcha security, or settings privileges to load rules', async () => {
    const rules = vi.spyOn(api, 'rules').mockResolvedValue([])
    const captcha = vi.spyOn(api, 'captcha').mockResolvedValue({ enabled: false, provider: 'none', site_key: '' })
    const security = mount(SettingsAdminPage, { props: { mode: 'security' } }); await flushPromises()
    expect(rules).not.toHaveBeenCalled()
    expect(captcha).toHaveBeenCalledOnce()
    expect(security.find('textarea').exists()).toBe(false)
    security.unmount()
    captcha.mockClear()
    const rulesPage = mount(SettingsAdminPage, { props: { mode: 'rules' } }); await flushPromises()
    expect(rules).toHaveBeenCalledOnce()
    expect(captcha).not.toHaveBeenCalled()
    expect(rulesPage.find('textarea').exists()).toBe(true)
    expect(rulesPage.find('input[type=password]').exists()).toBe(false)
    rulesPage.unmount()
  })

})
