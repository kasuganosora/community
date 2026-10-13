import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GroupAddressEditor from '../GroupAddressEditor.vue'
import { groupApi } from '../../../infrastructure/groupApi'
import type { Group } from '../../../domain/group'

vi.mock('../../../infrastructure/groupApi', () => ({ groupApi: { address: vi.fn() } }))
const group: Group = {
  id: 'room', name: 'Friends', topic: '', membership: 'join', encryption: 'unencrypted', join_rule: 'invite',
  listed: false, member_count: 2, unread_count: 0, client_url: 'https://matrix.to/#/!room:home.test',
  canonical_alias: '', address_server: 'home.test',
  capabilities: { send_message: true, invite: true, kick: true, ban: true, redact: true, set_name: true, set_avatar: true, manage_permissions: true, manage_address: true },
}
beforeEach(() => { vi.resetAllMocks() })

describe('group address editing', () => {
  it('preserves a conflicting draft, then saves the corrected short address', async () => {
    vi.mocked(groupApi.address).mockRejectedValueOnce(new Error('该短地址已被其他群组使用'))
    const updated = { ...group, canonical_alias: '#our-friends:home.test', client_url: 'https://matrix.to/#/%23our-friends:home.test' }
    vi.mocked(groupApi.address).mockResolvedValueOnce(updated)
    const wrapper = mount(GroupAddressEditor, { props: { group } })
    await wrapper.get('button').trigger('click')
    await wrapper.get('input').setValue('Friends')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('已被其他群组使用')
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('Friends')
    expect(groupApi.address).toHaveBeenLastCalledWith('room', 'friends')
    await wrapper.get('input').setValue('our-friends')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.emitted('updated')).toEqual([[updated]])
    expect(wrapper.find('input').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps external groups read-only and copies the exact server-provided address', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    const external = { ...group, canonical_alias: '#friends:remote.test', client_url: 'https://matrix.to/#/%23friends:remote.test', capabilities: { ...group.capabilities, manage_address: false } }
    const wrapper = mount(GroupAddressEditor, { props: { group: external } })
    expect(wrapper.text()).not.toContain('修改短地址')
    expect(wrapper.text()).not.toContain('设置短地址')
    await wrapper.get('button').trigger('click'); await flushPromises()
    expect(writeText).toHaveBeenCalledWith(external.client_url)
    expect(wrapper.get('a').attributes('href')).toBe(external.client_url)
    expect(groupApi.address).not.toHaveBeenCalled()
    wrapper.unmount(); vi.unstubAllGlobals()
  })

  it('does not apply an old save response after switching groups', async () => {
    let resolve!: (group: Group) => void
    vi.mocked(groupApi.address).mockReturnValue(new Promise(done => { resolve = done }))
    const wrapper = mount(GroupAddressEditor, { props: { group } })
    await wrapper.get('button').trigger('click'); await wrapper.get('input').setValue('old-group')
    await wrapper.get('form').trigger('submit')
    await wrapper.setProps({ group: { ...group, id: 'new-room' } })
    resolve({ ...group, canonical_alias: '#old-group:home.test' }); await flushPromises()
    expect(wrapper.emitted('updated')).toBeUndefined()
    expect(wrapper.text()).not.toContain('群地址已保存')
    wrapper.unmount()
  })
})
