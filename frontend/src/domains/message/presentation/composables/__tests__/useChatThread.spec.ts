import { sessionEpoch } from '@/shared/api/sessionScope'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, reactive } from 'vue'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { useChatThread } from '../useChatThread'
import { chatApi } from '../../../infrastructure/chatApi'
import type { ChatMessage } from '../../../domain/chat'
const auth = reactive({ sessionEpoch: 0, currentUser: { id: 'me', acct: 'me' } })
vi.mock('@/domains/auth/presentation/stores/authStore', () => ({ useAuthStore: () => auth }))
vi.mock('../../../infrastructure/chatApi', () => ({ chatApi: { get: vi.fn(), messages: vi.fn(), send: vi.fn(), replaceMessage: vi.fn(), deleteMessage: vi.fn(), retryDelivery: vi.fn(), addMembers: vi.fn(), removeMember: vi.fn() } }))
const conversation = { id: 'one', room_id: '!room:local', revision: 1, encrypted: false as const, title: 'QA', creator_id: 'me', can_manage: true, members: [] }
const message: ChatMessage = { event_id: '$one', sender: '@alice:local', body: 'Only the first account can read this', msgtype: 'm.text', created_at: 1, own: true, status_id: null }
function setup() {
  let thread!: ReturnType<typeof useChatThread>
  const wrapper = mount(defineComponent({ setup() { thread = useChatThread(() => 'one'); return {} }, template: '<div />' }))
  return { thread, wrapper }
}
beforeEach(() => {
  vi.useFakeTimers(); vi.resetAllMocks(); sessionEpoch.value = 0
  vi.mocked(chatApi.get).mockResolvedValue(conversation)
  vi.mocked(chatApi.messages).mockResolvedValue({ messages: [message], next_cursor: null })
})
afterEach(() => { vi.useRealTimers() })
describe('unified chat session and permission boundaries', () => {
  it('clears history on revoked membership instead of retaining a readable stale thread', async () => {
    const { thread, wrapper } = setup(); await flushPromises()
    expect(thread.messages.value).toHaveLength(1)
    vi.mocked(chatApi.get).mockRejectedValue({ response: { status: 403 } })
    await thread.refresh()
    expect(thread.messages.value).toEqual([])
    expect(thread.conversation.value).toBeNull()
    expect(thread.error.value).toContain('不可访问')
    wrapper.unmount()
  })
  it('retains readable history when only a message mutation is forbidden', async () => {
    const { thread, wrapper } = setup(); await flushPromises()
    vi.mocked(chatApi.replaceMessage).mockRejectedValue({ response: { status: 403 } })
    expect(await thread.send({ transaction_id: 'edit', body: 'retained draft' }, message)).toBe(false)
    expect(thread.conversation.value?.id).toBe('one')
    expect(thread.messages.value[0]?.event_id).toBe('$one')
    expect(thread.error.value).toContain('草稿已保留')
    wrapper.unmount()
  })
  it('does not insert a delayed send or clear a new session state after account switch', async () => {
    const { thread, wrapper } = setup(); await flushPromises()
    let resolve!: (value: { event_id: string; event_ids: string[]; delivery: 'queued' }) => void
    vi.mocked(chatApi.send).mockReturnValue(new Promise(done => { resolve = done }))
    const sending = thread.send({ transaction_id: 'tx', body: 'secret draft' })
    vi.mocked(chatApi.messages).mockResolvedValue({ messages: [], next_cursor: null })
    sessionEpoch.value++
    await flushPromises()
    resolve({ event_id: '$late', event_ids: ['$late'], delivery: 'queued' })
    expect(await sending).toBe(false)
    expect(thread.messages.value).toEqual([])
    expect(thread.pending.value).toBe('')
    wrapper.unmount()
  })
  it('stops polling when unmounted while an in-flight refresh completes', async () => {
    const { wrapper } = setup(); await flushPromises()
    let resolve!: (value: typeof conversation) => void
    vi.mocked(chatApi.get).mockReturnValue(new Promise(done => { resolve = done }))
    await vi.advanceTimersByTimeAsync(5000)
    wrapper.unmount()
    resolve(conversation); await flushPromises()
    const calls = vi.mocked(chatApi.get).mock.calls.length
    await vi.advanceTimersByTimeAsync(15000)
    expect(chatApi.get).toHaveBeenCalledTimes(calls)
  })
})
