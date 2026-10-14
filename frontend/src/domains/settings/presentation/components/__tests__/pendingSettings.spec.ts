import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import AuthorizedApps from '../AuthorizedApps.vue'
import PasskeySettings from '../PasskeySettings.vue'
import DomainBlockSettings from '../DomainBlockSettings.vue'
import FilterSettings from '../FilterSettings.vue'
import { authApi, type AuthorizedApp } from '@/domains/auth/infrastructure/authApi'
import { moderationApi, type MastodonFilter } from '../../../infrastructure/moderationApi'
import { invalidateSessionData } from '@/shared/api/sessionScope'
import { createPasskey } from '@/shared/utils/webauthn'

vi.mock('@/shared/utils/webauthn', () => ({ passkeySupported: () => true, createPasskey: vi.fn() }))
vi.mock('tdesign-vue-next', () => ({ MessagePlugin: { error: vi.fn(), success: vi.fn() } }))
let wrappers: VueWrapper[] = []
afterEach(() => { wrappers.forEach(w => w.unmount()); wrappers = []; vi.restoreAllMocks(); vi.clearAllMocks() })
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason: Error) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const filter: MastodonFilter = { id: 'f1', title: 'Filtered keyword', context: ['home'], expires_at: null, filter_action: 'hide', keywords: [] }

it.each(['domains', 'filters'] as const)('distinguishes a slow or failed %s load from an empty list and retries', async kind => {
  const pending = deferred<never>()
  const load = kind === 'domains' ? vi.spyOn(moderationApi, 'listDomainBlocks') : vi.spyOn(moderationApi, 'listFilters')
  load.mockImplementationOnce(() => pending.promise).mockResolvedValueOnce([])
  const w = mount(kind === 'domains' ? DomainBlockSettings : FilterSettings); wrappers.push(w)
  expect(w.get('[role=status]').text()).toContain('正在加载')
  expect(w.text()).not.toContain('还没有')
  pending.reject(new Error('暂时不可用')); await flushPromises()
  expect(w.get('[role=alert]').text()).toContain('暂时不可用')
  expect(w.text()).not.toContain('还没有')
  await w.get('[role=alert] button').trigger('click'); await flushPromises()
  expect(load).toHaveBeenCalledTimes(2)
  expect(w.text()).toContain('还没有')
})

it.each(['domains', 'filters'] as const)('shows pending %s removal and sends a single request', async kind => {
  const pending = deferred<void>()
  vi.spyOn(moderationApi, 'listDomainBlocks').mockResolvedValue(['example.test'])
  vi.spyOn(moderationApi, 'listFilters').mockResolvedValue([filter])
  const remove = kind === 'domains' ? vi.spyOn(moderationApi, 'unblockDomain') : vi.spyOn(moderationApi, 'deleteFilter')
  remove.mockImplementation(() => pending.promise)
  const w = mount(kind === 'domains' ? DomainBlockSettings : FilterSettings); wrappers.push(w); await flushPromises()
  const button = w.get('li button')
  await button.trigger('click'); await button.trigger('click')
  expect(button.attributes('disabled')).toBeDefined()
  expect(button.attributes('aria-busy')).toBe('true')
  expect(remove).toHaveBeenCalledTimes(1)
  pending.resolve(); await flushPromises()
  expect(w.find('li').exists()).toBe(false)
})

it('keeps a deferred app revoke visibly pending without repeating it', async () => {
  vi.spyOn(authApi, 'listAuthorizedApps').mockResolvedValue([{ id: 'a1', name: 'Example app' }])
  const pending = deferred<void>()
  const revoke = vi.spyOn(authApi, 'revokeAuthorizedApp').mockReturnValue(pending.promise)
  const w = mount(AuthorizedApps); wrappers.push(w); await flushPromises()
  await w.get('li button').trigger('click'); await w.get('li button').trigger('click')
  expect(w.get('li button').text()).toBe('撤销中…')
  expect(revoke).toHaveBeenCalledTimes(1)
  pending.resolve(); await flushPromises()
  expect(w.text()).not.toContain('Example app')
})

it('discards a previous account app list instead of repopulating private settings', async () => {
  const pending = deferred<AuthorizedApp[]>()
  vi.spyOn(authApi, 'listAuthorizedApps').mockReturnValue(pending.promise)
  const w = mount(AuthorizedApps); wrappers.push(w)
  invalidateSessionData(); pending.resolve([{ id: 'old', name: 'Previous private app' }]); await flushPromises()
  expect(w.text()).not.toContain('Previous private app')
})

it('never completes passkey registration in a different account after the browser prompt', async () => {
  const pending = deferred<Record<string, unknown>>()
  vi.spyOn(authApi, 'listPasskeys').mockResolvedValue([])
  vi.spyOn(authApi, 'passkeyRegisterBegin').mockResolvedValue({ publicKey: {} })
  vi.mocked(createPasskey).mockReturnValue(pending.promise)
  const finish = vi.spyOn(authApi, 'passkeyRegisterFinish')
  const w = mount(PasskeySettings); wrappers.push(w); await flushPromises()
  await w.get('form').trigger('submit'); await w.get('form').trigger('submit'); await flushPromises()
  expect(authApi.passkeyRegisterBegin).toHaveBeenCalledTimes(1)
  invalidateSessionData(); pending.resolve({ id: 'credential-from-old-session' }); await flushPromises()
  expect(finish).not.toHaveBeenCalled()
})
