import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import MessagesPage from '../MessagesPage.vue'
import ConversationMembersForm from '../../components/ConversationMembersForm.vue'
import { chatApi } from '../../../infrastructure/chatApi'
const mock = vi.hoisted(() => ({ push: vi.fn() }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ push: mock.push }) }))
vi.mock('@/domains/auth/presentation/stores/authStore', () => ({ useAuthStore: () => ({ currentUser: { id: 'me' } }) }))
vi.mock('@/shared/composables/usePaginatedList', () => ({ usePaginatedList: () => ({ items: ref([]), loading: ref(false), error: ref(''), hasMore: ref(false), refresh: vi.fn() }) }))
vi.mock('../../../infrastructure/chatApi', () => ({ chatApi: { list: vi.fn(), create: vi.fn() } }))
const setup = () => mount(MessagesPage, { global: { stubs: { 't-icon': true, ConversationMembersForm: true } } })
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(chatApi.list).mockResolvedValue({ conversations: [], next_cursor: null })
})
describe('unified new message entry', () => {
  it('uses one entry for account and Matrix recipients, and opens the returned conversation', async () => {
    let complete!: (value: never) => void
    vi.mocked(chatApi.create).mockReturnValue(new Promise(resolve => { complete = resolve }))
    const w = setup(); await flushPromises()
    expect(w.findAll('.messages-toolbar button')).toHaveLength(1)
    await w.get('.messages-toolbar button').trigger('click')
    const form = w.getComponent(ConversationMembersForm)
    const members = [{ account_id: '12' }, { matrix_id: '@bob:matrix.example' }]
    form.vm.$emit('submit', members); await flushPromises()
    expect(chatApi.create).toHaveBeenCalledWith(expect.any(String), members)
    expect(form.props('pending')).toBe(true)
    form.vm.$emit('submit', members); await flushPromises()
    expect(chatApi.create).toHaveBeenCalledTimes(1)
    complete({ id: 'new-room' } as never); await flushPromises()
    expect(mock.push).toHaveBeenCalledWith('/messages/new-room')
    expect(w.findComponent(ConversationMembersForm).exists()).toBe(false)
    w.unmount()
  })
  it('keeps the form on failure and reuses the same transaction when retrying', async () => {
    vi.mocked(chatApi.create).mockRejectedValue(new Error('暂时失败'))
    const w = setup(); await flushPromises()
    await w.get('.messages-toolbar button').trigger('click')
    const form = w.getComponent(ConversationMembersForm)
    form.vm.$emit('submit', [{ account_id: '12' }]); await flushPromises()
    expect(form.props('error')).toBe('暂时失败')
    expect(form.props('pending')).toBe(false)
    form.vm.$emit('submit', [{ account_id: '12' }]); await flushPromises()
    const calls = vi.mocked(chatApi.create).mock.calls
    expect(calls[0][0]).toBe(calls[1][0])
    expect(mock.push).not.toHaveBeenCalled()
    w.unmount()
  })
})
