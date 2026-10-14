import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import ChatThreadPage from '../ChatThreadPage.vue'
import ChatMessageView from '../../components/ChatMessageView.vue'
import { postApi } from '@/domains/post/infrastructure/postApi'
const mock = vi.hoisted(() => ({ send: vi.fn(), state: {} as Record<string, unknown> }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { conversationId: 'room' } }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('@/domains/auth/presentation/stores/authStore', () => ({ useAuthStore: () => ({ currentUser: { id: 'me' } }) }))
vi.mock('../../composables/useChatThread', () => ({ useChatThread: () => mock.state }))
vi.mock('@/domains/post/infrastructure/postApi', () => ({ postApi: { uploadMedia: vi.fn() } }))
const message = { event_id: '$one', sender: '@me:local', body: 'Original', msgtype: 'm.text', created_at: 1, own: true, status_id: null }
function setup() {
  return mount(ChatThreadPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' }, 't-icon': true, 't-loading': true, ChatMessageView: true, ConversationMembersForm: true } } })
}
beforeEach(() => {
  vi.clearAllMocks()
  mock.state = { conversation: ref({ title: 'QA', members: [], can_manage: true }), messages: ref([message]), cursor: ref(null), loading: ref(false), loadingOlder: ref(false), refreshPending: ref(false), pending: ref(''), busy: ref(false), error: ref(''), send: mock.send, refresh: vi.fn(), reload: vi.fn() }
})
describe('chat composer failure recovery', () => {
  it('shows retry feedback without hiding the readable conversation behind a page spinner', async () => {
    const refreshPending = mock.state.refreshPending as { value: boolean }
    ;(mock.state.error as { value: string }).value = '请求失败'
    mock.state.reload = vi.fn(() => { refreshPending.value = true })
    const w = setup()
    await w.get('.chat-thread__error button').trigger('click')
    expect(mock.state.reload).toHaveBeenCalledTimes(1)
    expect(w.get('.chat-thread__error button').text()).toBe('加载中…')
    expect(w.get('.chat-thread__error button').attributes('disabled')).toBeDefined()
    await w.get('.chat-thread__error button').trigger('click')
    expect(mock.state.reload).toHaveBeenCalledTimes(1)
    expect(w.find('.page-loading').exists()).toBe(false)
    expect(w.find('textarea').exists()).toBe(true)
    refreshPending.value = false; await flushPromises()
    expect(w.get('.chat-thread__error button').text()).toBe('重试')
    expect(w.get('.chat-thread__error button').attributes('disabled')).toBeUndefined()
    w.unmount()
  })
  it('blocks keyboard submit until uploads finish, then sends the actual attachment ID', async () => {
    let resolve!: (file: never) => void
    vi.mocked(postApi.uploadMedia).mockReturnValue(new Promise(done => { resolve = done }))
    mock.send.mockResolvedValue(true)
    const w = setup()
    await w.get('textarea').setValue('Attachment')
    const input = w.get('input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [new File(['bytes'], 'qa.png', { type: 'image/png' })] })
    await input.trigger('change')
    await w.get('textarea').trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(mock.send).not.toHaveBeenCalled()
    resolve({ id: 'media-1', description: '' } as never); await flushPromises()
    await w.get('form.chat-compose').trigger('submit'); await flushPromises()
    expect(mock.send).toHaveBeenCalledWith(expect.objectContaining({ body: 'Attachment', media_ids: ['media-1'] }), undefined)
    expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('')
    w.unmount()
  })
  it('makes delete-and-repost explicit and retains edited content when the server rejects replacement', async () => {
    mock.send.mockResolvedValue(false)
    const w = setup()
    w.findComponent(ChatMessageView).vm.$emit('edit'); await flushPromises()
    expect(w.text()).toContain('编辑会删除原消息并重新发送')
    await w.get('textarea').setValue('Keep this edited draft')
    await w.get('form.chat-compose').trigger('submit'); await flushPromises()
    expect(mock.send).toHaveBeenCalledWith(expect.objectContaining({ body: 'Keep this edited draft' }), message)
    expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('Keep this edited draft')
    w.unmount()
  })
  it('does not offer an unsupported leave action for legacy conversations', async () => {
    const conversation = mock.state.conversation as { value: Record<string, unknown> }
    conversation.value = { title: 'Legacy', can_manage: false, can_leave: false, members: [{ id: '1', account_id: 'me', matrix_id: '@me:local', kind: 'local', active: true }] }
    const w = setup()
    await w.findAll('button').find(button => button.text() === '成员')!.trigger('click')
    expect(w.findAll('button').some(button => button.text() === '离开')).toBe(false)
    conversation.value.can_leave = true; await flushPromises()
    expect(w.findAll('button').some(button => button.text() === '离开')).toBe(true)
    w.unmount()
  })

})
