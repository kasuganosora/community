import { mount, enableAutoUnmount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ModerationSettingsAdminPage from '../ModerationSettingsAdminPage.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), instanceAction: vi.fn() }))
vi.mock('../../../infrastructure/moderationSettingsApi', async (orig) => ({ ...(await orig<object>()), moderationSettingsApi: api }))

const base = {
  enable_registration: true, email_required_for_signup: true, auto_close_registration: true, moderator_inactivity_limit_days: 7,
  ugc_visibility_for_visitor: 'all', preserved_usernames: [], sensitive_words: [], prohibited_words: [], prohibited_words_for_name_of_user: [],
  hidden_tags: [], blocked_hosts: [], silenced_hosts: [], media_silenced_hosts: [], environment_locked: [],
}
const opts = {}
enableAutoUnmount(afterEach)
beforeEach(() => { vi.clearAllMocks() })

describe('registration auto-close toggle', () => {
  it('sits right under 开放注册 and is on by default', async () => {
    api.get.mockResolvedValue({ ...base })
    const wrapper = mount(ModerationSettingsAdminPage, opts); await flushPromises()
    const section = wrapper.get('.mod-basic').html()
    expect(section.indexOf('开放注册')).toBeLessThan(section.indexOf('管理员不活跃时自动关闭注册'))
    const sub = wrapper.get('[data-testid="auto-close-registration"]')
    expect(sub.text()).toContain('7 天')
    expect((sub.get('input').element as HTMLInputElement).checked).toBe(true)
  })
  it('saves auto_close_registration=false and explains it never closes', async () => {
    api.get.mockResolvedValue({ ...base })
    api.save.mockResolvedValue({ ...base, auto_close_registration: false })
    const wrapper = mount(ModerationSettingsAdminPage, opts); await flushPromises()
    await wrapper.get('[data-testid="auto-close-registration"] input').setValue(false); await flushPromises()
    expect(api.save).toHaveBeenCalledWith({ auto_close_registration: false })
    expect(wrapper.get('[data-testid="auto-close-registration"]').text()).toContain('都不会自动关闭注册')
  })
  it('does not embed a second server-rules editor', async () => {
    api.get.mockResolvedValue({ ...base })
    const wrapper = mount(ModerationSettingsAdminPage, opts); await flushPromises()
    expect(wrapper.text()).not.toContain('服务器规则')
    expect(wrapper.find('a[href="/admin/rules"]').exists()).toBe(false)
  })
})
