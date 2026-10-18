import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountLifecycleSettings from '../AccountLifecycleSettings.vue'
import { sessionEpoch } from '@/shared/api/sessionScope'

const api = vi.hoisted(() => ({ get: vi.fn(), saveAliases: vi.fn(), move: vi.fn(), delete: vi.fn() }))
const auth = vi.hoisted(() => ({ currentUser: { id: '1', username: 'alice' }, logout: vi.fn() }))
vi.mock('../../../infrastructure/accountLifecycleApi', () => ({ accountLifecycleApi: api }))
vi.mock('@/domains/auth/presentation/stores/authStore', () => ({ useAuthStore: () => auth }))
const state = { aliases: ['https://old.example/users/alice'], moved_to: '', deletion_requested: false }
beforeEach(() => { vi.clearAllMocks(); api.get.mockResolvedValue(state); auth.logout.mockResolvedValue(false) })
afterEach(() => vi.restoreAllMocks())

describe('account lifecycle settings', () => {
  it('keeps all mutation controls disabled if loading fails, and offers retry', async () => {
    api.get.mockRejectedValueOnce(new Error('offline'))
    const w = mount(AccountLifecycleSettings)
    await flushPromises()
    expect(w.find('[role=alert]').exists()).toBe(true)
    expect(w.get('fieldset').attributes('disabled')).toBeDefined()
    await w.get('[role=alert] button').trigger('click'); await flushPromises()
    expect(api.get).toHaveBeenCalledTimes(2)
    expect(w.get('fieldset').attributes('disabled')).toBeUndefined()
    w.unmount()
  })
  it('saves removal of an alias as an empty array and prevents double submission', async () => {
    let finish!: (value: typeof state) => void
    api.saveAliases.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const w = mount(AccountLifecycleSettings); await flushPromises()
    await w.get('.lifecycle__input-row button').trigger('click')
    const form = w.findAll('form')[0]!
    await form.trigger('submit'); await form.trigger('submit')
    expect(api.saveAliases).toHaveBeenCalledExactlyOnceWith([])
    finish({ ...state, aliases: [] }); await flushPromises()
    expect(w.find('[role=status]').exists()).toBe(true)
    w.unmount()
  })
  it('requires password and explicit acknowledgement before migrating and clears the password after success', async () => {
    api.move.mockResolvedValue({ ...state, moved_to: 'https://new.example/users/alice' })
    const w = mount(AccountLifecycleSettings); await flushPromises()
    const form = w.findAll('form')[1]!
    await form.trigger('submit'); expect(api.move).not.toHaveBeenCalled()
    await w.get('#migration-target').setValue('@alice@new.example')
    await w.get('#migration-password').setValue('test-password')
    await form.get('input[type=checkbox]').setValue(true)
    await form.trigger('submit'); await flushPromises()
    expect(api.move).toHaveBeenCalledExactlyOnceWith('@alice@new.example', 'test-password', '')
    expect(w.find('#migration-password').exists()).toBe(false)
    expect(w.get('a').attributes('href')).toBe('https://new.example/users/alice')
    w.unmount()
  })
  it('requires exact username confirmation and logs out only after accepted deletion', async () => {
    api.delete.mockResolvedValue(undefined)
    const w = mount(AccountLifecycleSettings); await flushPromises()
    const form = w.findAll('form')[2]!
    await w.get('#deletion-password').setValue('test-password')
    await w.get('#deletion-confirmation').setValue('bob')
    await form.get('input[type=checkbox]').setValue(true)
    await form.trigger('submit'); expect(api.delete).not.toHaveBeenCalled()
    await w.get('#deletion-confirmation').setValue('alice')
    await form.trigger('submit'); await flushPromises()
    expect(api.delete).toHaveBeenCalledExactlyOnceWith('test-password', 'alice', '')
    expect(auth.logout).toHaveBeenCalledExactlyOnceWith(true, false)
    w.unmount()
  })
  it('never logs out a newly switched account after the previous deletion resolves', async () => {
    let finish!: () => void
    api.delete.mockImplementation(() => new Promise<void>(resolve => { finish = resolve }))
    const w = mount(AccountLifecycleSettings); await flushPromises()
    const form = w.findAll('form')[2]!
    await w.get('#deletion-password').setValue('test-password')
    await w.get('#deletion-confirmation').setValue('alice')
    await form.get('input[type=checkbox]').setValue(true)
    await form.trigger('submit')
    sessionEpoch.value++; await flushPromises()
    finish(); await flushPromises()
    expect(auth.logout).not.toHaveBeenCalled()
    expect((w.get('#deletion-password').element as HTMLInputElement).value).toBe('')
    w.unmount()
  })
})
