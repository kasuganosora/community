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
  it('finishes a successful send without waiting for slow read-back, and blocks duplicate in-flight sends', async () => {
    const { thread, wrapper } = setup(); await flushPromises()
    let resolve!: (value: { event_id: string; event_ids: string[]; delivery: 'queued' }) => void
    vi.mocked(chatApi.send).mockReturnValue(new Promise(done => { resolve = done }))
    vi.mocked(chatApi.get).mockReturnValue(new Promise(() => {}))
    const send = thread.send({ transaction_id: 'tx', body: 'sent once' })
    expect(thread.pending.value).toBe('send')
    expect(await thread.send({ transaction_id: 'tx', body: 'sent once' })).toBe(false)
    resolve({ event_id: '$sent', event_ids: ['$sent'], delivery: 'queued' })
    expect(await send).toBe(true)
    expect(chatApi.send).toHaveBeenCalledTimes(1)
    expect(thread.pending.value).toBe('')
    expect(thread.messages.value.at(-1)?.body).toBe('sent once')
    wrapper.unmount()
  })
  it('reports a failed mutation immediately even while membership recheck is slow', async () => {
    const { thread, wrapper } = setup(); await flushPromises()
    vi.mocked(chatApi.send).mockRejectedValue(new Error('offline'))
    vi.mocked(chatApi.get).mockReturnValue(new Promise(() => {}))
    expect(await thread.send({ transaction_id: 'tx', body: 'keep draft' })).toBe(false)
    expect(thread.error.value).toContain('草稿已保留')
    expect(thread.pending.value).toBe('')
    wrapper.unmount()
  })
  it('keeps manual retry visibly pending and shares its read with repeated calls and background polling', async () => {
    const { thread, wrapper } = setup(); await flushPromises()
    let resolve!: (value: typeof conversation) => void
    vi.mocked(chatApi.get).mockReturnValueOnce(new Promise(done => { resolve = done }))
    thread.error.value = '上次请求失败'
    const retry = thread.reload()
    expect(thread.refreshPending.value).toBe(true)
    expect(thread.loading.value).toBe(false)
    await thread.reload()
    const sameRead = thread.refresh()
    await vi.advanceTimersByTimeAsync(11000)
    expect(chatApi.get).toHaveBeenCalledTimes(2)
    expect(chatApi.messages).toHaveBeenCalledTimes(2)
    expect(thread.refreshPending.value).toBe(true)
    resolve({ ...conversation, title: 'Updated' }); await Promise.all([retry, sameRead])
    expect(thread.conversation.value?.title).toBe('Updated')
    expect(thread.refreshPending.value).toBe(false)
    expect(thread.error.value).toBe('')
    wrapper.unmount()
  })
  it('recovers the retry button after failure while preserving readable history', async () => {
    const { thread, wrapper } = setup(); await flushPromises()
    vi.mocked(chatApi.get).mockRejectedValueOnce(new Error('暂时离线'))
    await thread.reload()
    expect(thread.refreshPending.value).toBe(false)
    expect(thread.error.value).toBe('暂时离线')
    expect(thread.messages.value).toHaveLength(1)
    await thread.reload()
    expect(thread.error.value).toBe('')
    expect(thread.loading.value).toBe(false)
    wrapper.unmount()
  })
  it('clears an acknowledged operation error when manual retry reuses successful read-back', async () => {
    const { thread, wrapper } = setup(); await flushPromises()
    let resolve!: (value: typeof conversation) => void
    vi.mocked(chatApi.get).mockReturnValueOnce(new Promise(done => { resolve = done }))
    vi.mocked(chatApi.send).mockRejectedValueOnce(new Error('发送失败'))
    expect(await thread.send({ transaction_id: 'tx', body: 'keep draft' })).toBe(false)
    expect(thread.error.value).toContain('草稿已保留')
    const retry = thread.reload()
    expect(chatApi.get).toHaveBeenCalledTimes(2)
    expect(thread.error.value).toContain('草稿已保留')
    resolve(conversation); await retry
    expect(thread.error.value).toBe('')
    expect(thread.refreshPending.value).toBe(false)
    wrapper.unmount()
  })
  it('preserves a new read error when manual retry reuses failed operation read-back', async () => {
    const { thread, wrapper } = setup(); await flushPromises()
    let reject!: (error: Error) => void
    vi.mocked(chatApi.get).mockReturnValueOnce(new Promise((_, fail) => { reject = fail }))
    vi.mocked(chatApi.send).mockRejectedValueOnce(new Error('发送失败'))
    expect(await thread.send({ transaction_id: 'tx', body: 'keep draft' })).toBe(false)
    const retry = thread.reload()
    expect(chatApi.get).toHaveBeenCalledTimes(2)
    reject(new Error('新的读取失败')); await retry
    expect(thread.error.value).toBe('新的读取失败')
    expect(thread.refreshPending.value).toBe(false)
    wrapper.unmount()
  })
  it('waits for a current background read before loading older history and does not let polling interrupt either read', async () => {
    vi.mocked(chatApi.messages).mockResolvedValueOnce({ messages: [message], next_cursor: 'before-one' })
    const { thread, wrapper } = setup(); await flushPromises()
    let resolveRefresh!: (value: typeof conversation) => void
    let resolveHistory!: (page: { messages: ChatMessage[]; next_cursor: string | null }) => void
    vi.mocked(chatApi.get).mockReturnValueOnce(new Promise(done => { resolveRefresh = done }))
    vi.mocked(chatApi.messages).mockResolvedValueOnce({ messages: [{ ...message, event_id: '$new', created_at: 2 }], next_cursor: 'ignored-refresh-cursor' })
      .mockReturnValueOnce(new Promise(done => { resolveHistory = done }))
    const refreshing = thread.refresh()
    const history = thread.older()
    expect(thread.loadingOlder.value).toBe(true)
    await vi.advanceTimersByTimeAsync(6000)
    expect(chatApi.get).toHaveBeenCalledTimes(2)
    expect(chatApi.messages).toHaveBeenCalledTimes(2)
    resolveRefresh(conversation); await refreshing; await flushPromises()
    expect(chatApi.messages).toHaveBeenLastCalledWith('one', 'before-one')
    await thread.reload()
    expect(thread.refreshPending.value).toBe(false)
    await vi.advanceTimersByTimeAsync(11000)
    expect(chatApi.get).toHaveBeenCalledTimes(2)
    expect(chatApi.messages).toHaveBeenCalledTimes(3)
    resolveHistory({ messages: [{ ...message, event_id: '$older', created_at: 0 }], next_cursor: 'before-older' }); await history
    expect(thread.messages.value.map(item => item.event_id)).toEqual(['$older', '$one', '$new'])
    expect(thread.cursor.value).toBe('before-older')
    expect(thread.loadingOlder.value).toBe(false)
    wrapper.unmount()
  })
  it('does not let an old account retry clear the new account pending state', async () => {
    const { thread, wrapper } = setup(); await flushPromises()
    let oldResolve!: (value: typeof conversation) => void
    vi.mocked(chatApi.get).mockReturnValueOnce(new Promise(done => { oldResolve = done }))
    const oldRetry = thread.reload()
    sessionEpoch.value++; await flushPromises()
    expect(thread.refreshPending.value).toBe(false)
    let newResolve!: (value: typeof conversation) => void
    vi.mocked(chatApi.get).mockReturnValueOnce(new Promise(done => { newResolve = done }))
    const newRetry = thread.reload()
    oldResolve({ ...conversation, title: 'Wrong account' }); await oldRetry
    expect(thread.refreshPending.value).toBe(true)
    expect(thread.conversation.value?.title).toBe('QA')
    newResolve(conversation); await newRetry
    expect(thread.refreshPending.value).toBe(false)
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
