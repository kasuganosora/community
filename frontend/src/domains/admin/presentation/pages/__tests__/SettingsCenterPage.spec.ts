import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { invalidateSessionData } from '@/shared/api/sessionScope'
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
    expect(w.get('input[readonly]').element).toHaveProperty('value', '0')
    expect(w.find('input[type=password]').exists()).toBe(false)
    w.unmount()
  })
})

it('renders deployed settings as labelled read-only controls and never submits their values', async () => {
  vi.mocked(api.get).mockResolvedValueOnce({ ...section, fields: [
    { key: 'config.storage.endpoint', deployment_key: 'S3_ENDPOINT', label: '存储端点', type: 'text', value: 's3.example.org', read_only: true, source: 'environment', restart_required: true, description: '兼容 S3 的地址' },
    { key: 'config.storage.secret_key', deployment_key: 'S3_SECRET_KEY', label: '密钥', type: 'secret', secret_set: true, read_only: true, source: 'environment', restart_required: true },
  ] })
  const save = vi.spyOn(api, 'save')
  const w = mount(SettingsCenterPage, { props: { section: 'storage' } }); await flushPromises()
  const inputs = w.findAll('input[readonly]')
  expect(inputs).toHaveLength(2)
  expect(inputs[0]!.element).toHaveProperty('value', 's3.example.org')
  expect(inputs[0]!.attributes('aria-describedby')).toBeTruthy()
  expect(inputs[1]!.element).toHaveProperty('value', '已配置 · 密钥已隐藏')
  expect(w.text()).toContain('S3_ENDPOINT')
  expect(w.text()).toContain('兼容 S3 的地址')
  expect(w.find('button[type=submit]').exists()).toBe(false)
  await w.get('form').trigger('submit')
  expect(save).not.toHaveBeenCalled()
  w.unmount()
})

it('clears credential drafts and ignores a late connection result when the session changes', async () => {
  let resolve!: (result: { success: boolean; message: string }) => void
  vi.spyOn(api, 'test').mockImplementation(() => new Promise(r => { resolve = r }))
  const w = mount(SettingsCenterPage, { props: { section: 'email' } }); await flushPromises()
  await w.findAll('button').find(b => b.text() === '测试已保存配置')!.trigger('click')
  invalidateSessionData(); await flushPromises()
  resolve({ success: true, message: 'old-account-result' }); await flushPromises()
  expect(w.find('form').exists()).toBe(false)
  expect(w.text()).not.toContain('old-account-result')
  w.unmount()
})

it('submits editable select, numeric, boolean and textarea values with their original types', async () => {
  const editable = { read_only: false, source: 'database' as const, restart_required: false }
  const fields: SettingsSection['fields'] = [
    { ...editable, key: 'mode', label: '模式', type: 'text', value: 'local', options: ['local', 's3'] },
    { ...editable, key: 'limit', label: '限制', type: 'number', value: 20, min: 1, max: 80 },
    { ...editable, key: 'enabled', label: '开启', type: 'boolean', value: false },
    { ...editable, key: 'description', label: '介绍', type: 'textarea', value: '' },
  ]
  vi.mocked(api.get).mockResolvedValueOnce({ ...section, fields })
  const save = vi.spyOn(api, 'save').mockResolvedValue({ ...section, fields })
  const w = mount(SettingsCenterPage, { props: { section: 'general' } }); await flushPromises()
  await w.get('select').setValue('s3'); await w.get('input[type=number]').setValue('30')
  await w.get('input[type=checkbox]').setValue(true); await w.get('textarea').setValue('A site description')
  await w.get('form').trigger('submit'); await flushPromises()
  expect(save).toHaveBeenCalledWith('general', { mode: 's3', limit: 30, enabled: true, description: 'A site description' }, [])
  expect(w.text()).toContain('新请求立即生效')
  w.unmount()
})
