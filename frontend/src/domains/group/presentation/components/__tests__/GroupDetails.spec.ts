import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { sessionEpoch } from '@/shared/api/sessionScope'
import GroupDetails from '../GroupDetails.vue'
import { groupApi } from '../../../infrastructure/groupApi'
import type { Group } from '../../../domain/group'

vi.mock('../../../infrastructure/groupApi', () => ({ groupApi: { update: vi.fn(), upload: vi.fn() } }))
const group = { id: 'one', name: 'Group', topic: '', encryption: 'unencrypted', capabilities: { set_name: true, set_avatar: true } } as Group
const mountDetails = () => mount(GroupDetails, { props: { group, members: [], membersCursor: null, membersLoading: false }, global: { stubs: { GroupAvatar: true, GroupAddressEditor: true, GroupMemberInput: true } } })
beforeEach(() => { vi.resetAllMocks(); sessionEpoch.value = 0 })
describe('group details asynchronous feedback', () => {
  it('shows immediate save feedback and prevents repeated submit, then permits retry on failure', async () => {
    let reject!: (error: Error) => void
    vi.mocked(groupApi.update).mockReturnValueOnce(new Promise((_, fail) => { reject = fail }))
    const wrapper = mountDetails()
    await wrapper.get('input:not([type="file"])').setValue('Changed')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.get('[role="status"]').text()).toBe('正在保存资料…')
    expect(wrapper.get('form button').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit')
    expect(groupApi.update).toHaveBeenCalledTimes(1)
    reject(new Error('保存失败')); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('保存失败')
    expect(wrapper.get('form button').attributes('disabled')).toBeUndefined()
    vi.mocked(groupApi.update).mockResolvedValue({ ...group, name: 'Changed' })
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.emitted('updated')).toHaveLength(1)
    wrapper.unmount()
  })
  it('does not apply a delayed upload to a different account or room', async () => {
    let resolve!: (value: never) => void
    vi.mocked(groupApi.upload).mockReturnValue(new Promise(done => { resolve = done }))
    const wrapper = mountDetails()
    const input = wrapper.get('input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [new File(['image'], 'qa.png', { type: 'image/png' })] })
    await input.trigger('change')
    expect(wrapper.text()).toContain('正在上传头像')
    sessionEpoch.value++
    resolve({ id: 'upload-from-previous-account' } as never); await flushPromises()
    expect(groupApi.update).not.toHaveBeenCalled()
    expect(wrapper.emitted('updated')).toBeUndefined()
    wrapper.unmount()
  })
})
