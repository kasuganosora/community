import { sessionEpoch } from '@/shared/api/sessionScope'
import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import ConversationMembersForm from '../ConversationMembersForm.vue'
import { userApi } from '@/domains/user/infrastructure/userApi'
const auth = reactive({ sessionEpoch: 0, currentUser: { id: 'me' } })
vi.mock('@/domains/auth/presentation/stores/authStore', () => ({ useAuthStore: () => auth }))
vi.mock('@/domains/user/infrastructure/userApi', () => ({ userApi: { lookupAccount: vi.fn() } }))
const account = { id: 'other', acct: 'alice@example.test', username: 'alice', display_name: 'Alice', avatar: '', url: '', uri: '' }
const setup = () => mount(ConversationMembersForm, { global: { stubs: { UserAvatar: true } } })
beforeEach(() => { vi.clearAllMocks(); sessionEpoch.value = 0 })
describe('conversation participant consent', () => {
  it('requires explicit plaintext consent and emits exact selected account/Matrix identities', async () => {
    vi.mocked(userApi.lookupAccount).mockResolvedValue(account as never)
    const w = setup()
    const input = w.get('input:not([type="checkbox"])')
    await input.setValue('alice@example.test')
    await w.findAll('button').find(b => b.text() === '添加')!.trigger('click')
    await flushPromises()
    await input.setValue('@bob:matrix.test')
    await w.findAll('button').find(b => b.text() === '添加')!.trigger('click')
    await flushPromises()
    await w.get('form').trigger('submit')
    expect(w.emitted('submit')).toBeUndefined()
    await w.get('input[type="checkbox"]').setValue(true)
    await w.get('form').trigger('submit')
    expect(w.emitted('submit')?.[0]).toEqual([[{ account_id: 'other' }, { matrix_id: '@bob:matrix.test' }]])
    expect(w.text()).toContain('不使用端到端加密')
    w.unmount()
  })
  it('drops delayed resolved identities when the signed-in account changes', async () => {
    let resolve!: (value: never) => void
    vi.mocked(userApi.lookupAccount).mockReturnValue(new Promise(done => { resolve = done }))
    const w = setup()
    await w.get('#conversation-recipient').setValue('alice@example.test')
    await w.findAll('button').find(b => b.text() === '添加')!.trigger('click')
    sessionEpoch.value++
    resolve(account as never)
    await flushPromises()
    expect(w.find('[aria-label="待添加的成员"]').exists()).toBe(false)
    expect((w.get('input[type="checkbox"]').element as HTMLInputElement).checked).toBe(false)
    w.unmount()
  })
})
