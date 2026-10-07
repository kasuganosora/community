import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SettingsCenterPage from '../SettingsCenterPage.vue'
import { settingsAdminApi as api, type SettingsSection } from '../../../infrastructure/settingsAdminApi'
const section: SettingsSection = { section: 'email', title: '邮件服务器', description: '配置', fields: [
  { key: 'config.email.server', label: '服务器', type: 'text', value: 'smtp.example.org', read_only: false, source: 'database', restart_required: true },
  { key: 'config.email.password', label: '密码', type: 'secret', secret_set: true, read_only: false, source: 'database', restart_required: true },
] }
beforeEach(() => { vi.spyOn(api, 'get').mockResolvedValue(structuredClone(section)) })
afterEach(() => vi.restoreAllMocks())
describe('settings center', () => {
  it('saves only modified values and retains hidden secrets, with restart notice', async () => {
    const save = vi.spyOn(api, 'save').mockResolvedValue(structuredClone(section))
    const w = mount(SettingsCenterPage, { props: { section: 'email' } }); await flushPromises()
    expect(w.get('input[type=password]').element).toHaveProperty('value', '')
    await w.get('input[type=text]').setValue('smtp.new.example')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(save).toHaveBeenCalledWith('email', { 'config.email.server': 'smtp.new.example' }, [])
    expect(w.text()).toContain('重启服务')
    w.unmount()
  })
  it('requires explicit confirmation before clearing secrets and preserves draft on failure', async () => {
    const save = vi.spyOn(api, 'save').mockRejectedValue(new Error('offline'))
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const w = mount(SettingsCenterPage, { props: { section: 'email' } }); await flushPromises()
    await w.get('input[type=checkbox]').setValue(true)
    await w.get('form').trigger('submit'); expect(save).not.toHaveBeenCalled()
    confirm.mockReturnValue(true)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(save).toHaveBeenCalledWith('email', {}, ['config.email.password'])
    expect(w.get('[role=alert]').text()).toContain('输入已保留')
    expect(w.get('input[type=checkbox]').element).toHaveProperty('checked', true)
    w.unmount()
  })
  it('does not send connections automatically and blocks testing unsaved targets', async () => {
    const test = vi.spyOn(api, 'test').mockResolvedValue({ success: true })
    const w = mount(SettingsCenterPage, { props: { section: 'email' } }); await flushPromises()
    expect(test).not.toHaveBeenCalled()
    await w.get('input[type=text]').setValue('unsaved.invalid')
    const button = w.findAll('button').find(b => b.text() === '测试已保存配置')!
    expect(button.attributes('disabled')).toBeDefined()
    await button.trigger('click'); expect(test).not.toHaveBeenCalled()
    w.unmount()
  })
  it('ignores late previous-section responses so they cannot overwrite the active form', async () => {
    let resolve!: (s: SettingsSection) => void
    vi.mocked(api.get).mockImplementationOnce(() => new Promise(r => { resolve = r }))
    const w = mount(SettingsCenterPage, { props: { section: 'email' } })
    vi.mocked(api.get).mockResolvedValueOnce({ section: 'database', title: '数据库', description: '', fields: [{ key: 'open', label: '连接数', type: 'text', value: 0, read_only: true, source: 'runtime', restart_required: false }] })
    await w.setProps({ section: 'database' }); await flushPromises()
    resolve(section); await flushPromises()
    expect(w.get('h1').text()).toBe('数据库')
    expect(w.text()).toContain('0')
    expect(w.find('input[type=password]').exists()).toBe(false)
    w.unmount()
  })
})
