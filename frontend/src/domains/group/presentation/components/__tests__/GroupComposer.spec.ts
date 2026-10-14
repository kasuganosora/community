import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GroupComposer from '../GroupComposer.vue'
import { groupApi } from '../../../infrastructure/groupApi'
import type { Group, GroupMessage, GroupUpload } from '../../../domain/group'
vi.mock('../../../infrastructure/groupApi', () => ({ groupApi: { upload: vi.fn(), send: vi.fn(), replace: vi.fn(), typing: vi.fn() } }))
vi.mock('@/shared/components/post/EmojiPicker.vue', () => ({ default: { template: '<div />' } }))
const group: Group = { id: 'group', name: 'Design', topic: '', membership: 'join', encryption: 'unencrypted', join_rule: 'invite', listed: false, member_count: 2, unread_count: 0, client_url: '', capabilities: { send_message: true, invite: true, kick: true, ban: true, redact: true, set_name: true, set_avatar: true, manage_permissions: true } }
function setup() { return mount(GroupComposer, { props: { group: { ...group }, members: [] }, global: { stubs: { 't-icon': true } } }) }
beforeEach(() => { vi.resetAllMocks(); vi.mocked(groupApi.typing).mockResolvedValue(); vi.mocked(groupApi.send).mockResolvedValue({ messages: [], event_ids: [], status: 'accepted' }); vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:preview'), revokeObjectURL: vi.fn() })) })
describe('group composer failure recovery and encryption', () => {
  it('retains the same transaction and payload on ambiguous network retries', async () => {
    vi.mocked(groupApi.send).mockRejectedValueOnce(new Error('网络失败'))
    const wrapper = setup(); await wrapper.get('textarea').setValue('Keep this message')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('Keep this message')
    expect(wrapper.get('textarea').attributes('readonly')).toBeDefined()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(groupApi.send).toHaveBeenCalledTimes(2)
    expect(vi.mocked(groupApi.send).mock.calls[0]).toEqual(vi.mocked(groupApi.send).mock.calls[1])
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('')
    wrapper.unmount()
  })
  it('blocks sends after encryption while preserving selectable draft text', async () => {
    const wrapper = setup(); await wrapper.get('textarea').setValue('copy this draft')
    await wrapper.setProps({ group: { ...group, encryption: 'encrypted' } })
    expect(wrapper.get('textarea').attributes('readonly')).toBeDefined()
    expect(wrapper.get('textarea').attributes('disabled')).toBeUndefined()
    await wrapper.get('form').trigger('submit'); expect(groupApi.send).not.toHaveBeenCalled()
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('copy this draft')
    wrapper.unmount()
  })
  it('waits for uploads before keyboard sending and sends the protected upload ID', async () => {
    let resolve!: (upload: GroupUpload) => void
    vi.mocked(groupApi.upload).mockReturnValue(new Promise(done => { resolve = done }))
    const wrapper = setup(), input = wrapper.get('input[type=file]')
    Object.defineProperty(input.element, 'files', { value: [new File(['bytes'], 'qa.txt', { type: 'text/plain' })] })
    await input.trigger('change'); await wrapper.get('textarea').trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(groupApi.send).not.toHaveBeenCalled()
    resolve({ id: 'protected-upload', name: 'qa.txt', size: 5, type: 'text/plain' }); await flushPromises()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(groupApi.send).toHaveBeenCalledWith('group', expect.objectContaining({ upload_ids: ['protected-upload'] }))
    wrapper.unmount()
  })
  it('aborts an in-flight upload when the room enables encryption', async () => {
    vi.mocked(groupApi.upload).mockReturnValue(new Promise(() => {}))
    const wrapper = setup(), input = wrapper.get('input[type=file]')
    Object.defineProperty(input.element, 'files', { value: [new File(['x'], 'qa.txt', { type: 'text/plain' })] })
    await input.trigger('change'); const signal = vi.mocked(groupApi.upload).mock.calls[0]![2]
    await wrapper.setProps({ group: { ...group, encryption: 'encrypted' } }); expect(signal.aborted).toBe(true)
    wrapper.unmount()
  })
  it('explains replacement and retains the exact recovery request after partial deletion', async () => {
    const original = { id: 'old', body: 'Original' } as GroupMessage
    vi.mocked(groupApi.replace).mockRejectedValue({ isAxiosError: true, response: { data: { deleted: true, messages: [] } } })
    const wrapper = setup(); await wrapper.setProps({ editing: original }); await wrapper.get('textarea').setValue('Revised')
    expect(wrapper.text()).toContain('编辑会删除原消息并重发')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.text()).toContain('原消息已删除')
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('Revised')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(vi.mocked(groupApi.replace).mock.calls[0]).toEqual(vi.mocked(groupApi.replace).mock.calls[1])
    wrapper.unmount()
  })
})
