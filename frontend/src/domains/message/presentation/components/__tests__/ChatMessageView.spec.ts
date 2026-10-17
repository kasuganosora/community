import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ChatMessageView from '../ChatMessageView.vue'
import type { ChatMessage } from '../../../domain/chat'
const base: ChatMessage = { event_id: '$one', sender: '@alice:local', body: 'Hello', msgtype: 'm.text', created_at: 1, own: true, status_id: null }
const setup = (message: ChatMessage) => mount(ChatMessageView, { props: { message }, global: { stubs: { PostContent: true, UserAvatar: true, AccountLink: { template: '<a><slot /></a>' }, 't-icon': true } } })
describe('chat message visibility and delivery labels', () => {
  it('distinguishes remote acknowledgement from local completion, and never claims read receipts', async () => {
    const delivery = { event_id: '$one', room_id: '!room', state: 'delivered' as const, remote_received: false, can_retry: false, updated_at: '' }
    const w = setup({ ...base, delivery })
    expect(w.text()).toContain('已投递（本地）')
    expect(w.text()).not.toContain('已读')
    await w.setProps({ message: { ...base, delivery: { ...delivery, remote_received: true } } })
    expect(w.text()).toContain('远端服务器已接收')
    w.unmount()
  })
  it('shows author retry only when current server permissions explicitly allow it', () => {
    const delivery = { event_id: '$one', room_id: '!room', state: 'failed' as const, remote_received: false, can_retry: true, updated_at: '' }
    const own = setup({ ...base, delivery }); expect(own.text()).toContain('重试投递'); own.unmount()
    const other = setup({ ...base, own: false, delivery }); expect(other.text()).not.toContain('重试投递'); other.unmount()
    const skipped = setup({ ...base, delivery: { ...delivery, state: 'skipped', can_retry: false } }); expect(skipped.text()).not.toContain('重试投递'); skipped.unmount()
  })
  it('waits for the source message to be bridged before offering delete and repost', async () => {
    const w = setup(base)
    expect(w.findAll('button').find(b => b.text() === '处理后可编辑')?.attributes('disabled')).toBeDefined()
    await w.setProps({ message: { ...base, status_id: '42' } })
    const edit = w.findAll('button').find(b => b.text() === '编辑')!
    expect(edit.attributes('disabled')).toBeUndefined()
    await edit.trigger('click')
    expect(w.emitted('edit')).toHaveLength(1)
    w.unmount()
  })
  it('explains a Matrix transport failure without claiming all peers received the message', () => {
    const w = setup({ ...base, delivery: { event_id: '$one', room_id: '!room', state: 'failed', remote_received: false, can_retry: true, updated_at: '', error_code: 'matrix_delivery_failed' } })
    expect(w.text()).toContain('Matrix 服务器投递失败')
    expect(w.text()).not.toContain('远端服务器已接收')
    w.unmount()
  })
  it('does not render redacted text/media or unsafe attachment schemes', () => {
    const deleted = setup({ ...base, body: 'private removed body', redacted: true, msgtype: 'm.image', url: 'https://media.test/secret.png' })
    expect(deleted.text()).toContain('消息已删除')
    expect(deleted.html()).not.toContain('secret.png')
    expect(deleted.html()).not.toContain('private removed body')
    expect(deleted.findAll('button')).toHaveLength(0); deleted.unmount()
    const unsafe = setup({ ...base, msgtype: 'm.image', url: 'javascript:alert(1)' }); expect(unsafe.find('img').exists()).toBe(false); unsafe.unmount()
  })
  it('does not reveal warned or deleted reply content in the compact quote', async () => {
    const w = setup({ ...base, reply_event_id: '$parent' })
    await w.setProps({ reply: { ...base, body: 'private spoiler', status: { spoiler_text: '剧透提醒', content: '<p>private spoiler</p>' } as never } })
    expect(w.get('.chat-message__reply').text()).toBe('剧透提醒')
    expect(w.text()).not.toContain('private spoiler')
    await w.setProps({ reply: { ...base, redacted: true, body: 'removed content' } })
    expect(w.get('.chat-message__reply').text()).toBe('消息已删除')
    expect(w.text()).not.toContain('removed content')
    w.unmount()
  })
})
