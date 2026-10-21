import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccessTokenSettings from '../AccessTokenSettings.vue'
import WebhookSettings from '../WebhookSettings.vue'
import { connectionsApi, runConsole } from '../../../infrastructure/connectionsApi'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { tokenService } from '@/shared/api/client'
import { createPinia, setActivePinia } from 'pinia'

beforeEach(() => { setActivePinia(createPinia()); window.matchMedia ??= ((query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia })
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('lets users select individual triggers, retains an existing secret, and clears drafts on account switch', async () => {
  vi.spyOn(connectionsApi, 'hooks').mockResolvedValue([{ id: 'hook-1', name: 'My hook', url: 'https://receiver.example/hook', events: ['note'], enabled: true, created_at: '' }])
  const save = vi.spyOn(connectionsApi, 'saveHook').mockResolvedValue({ item: { id: 'hook-1', name: 'My hook', url: 'https://receiver.example/hook', events: ['note', 'reply'], enabled: true, created_at: '' }, secret: '' })
  const wrapper = mount(WebhookSettings)
  await flushPromises()
  await wrapper.findAll('button').find(button => button.text() === '编辑')!.trigger('click')
  expect((wrapper.get('input[type="password"]').element as HTMLInputElement).value).toBe('')
  await wrapper.get('input[value="reply"]').setValue(true)
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(save).toHaveBeenCalledWith(expect.objectContaining({ secret: '', events: ['note', 'reply'] }), 'hook-1')
  expect(wrapper.find('form').exists()).toBe(false)
  await wrapper.findAll('button').find(button => button.text() === '创建 Webhook')!.trigger('click')
  await wrapper.get('input[type="password"]').setValue('draft-secret')
  sessionEpoch.value++
  await flushPromises()
  expect(wrapper.find('form').exists()).toBe(false)
  expect(wrapper.text()).not.toContain('My hook')
  wrapper.unmount()
})

it('does not reveal a token that finishes creating after switching accounts', async () => {
  vi.spyOn(connectionsApi, 'tokens').mockResolvedValue([])
  let resolve!: (value: Awaited<ReturnType<typeof connectionsApi.createToken>>) => void
  const create = vi.spyOn(connectionsApi, 'createToken').mockReturnValue(new Promise(done => { resolve = done }))
  const wrapper = mount(AccessTokenSettings, { attachTo: document.body }); await flushPromises()
  await wrapper.findAll('button').find(button => button.text() === '生成访问令牌')!.trigger('click')
  await flushPromises()
  const input = document.querySelector<HTMLInputElement>('#token-form input:not([type="checkbox"])')!
  input.value = 'My app'; input.dispatchEvent(new Event('input'))
  await flushPromises()
  document.querySelector<HTMLFormElement>('#token-form')!.dispatchEvent(new Event('submit'))
  await flushPromises()
  expect(create).toHaveBeenCalledWith(expect.objectContaining({ name: 'My app', scopes: ['read:accounts', 'read:statuses'] }))
  sessionEpoch.value++
  resolve({ token: 'must-not-leak', item: { id: 'token-1', name: 'My app', scopes: ['read'], created_at: '', expires_at: null } })
  await flushPromises()
  expect(wrapper.find('textarea').exists()).toBe(false)
  expect(wrapper.text()).not.toContain('My app')
  wrapper.unmount()
})

it('collapses complete scope sets, keeps admin scopes out of enable-all, and asks for re-auth on admin writes', async () => {
  const { collapseScopes, needsReauth, userScopeRows } = await import('../../../domain/tokenScopes')
  expect(collapseScopes(userScopeRows.map(r => r.scope))).toEqual(['push', 'read', 'write'])
  expect(collapseScopes(['write:statuses', 'read:statuses'])).toEqual(['read:statuses', 'write:statuses'])
  expect(userScopeRows.some(r => r.scope.startsWith('admin:'))).toBe(false)
  expect(needsReauth(['admin:read:reports'])).toBe(false)
  expect(needsReauth(['admin:write:reports'])).toBe(true)
})

it('restricts the API console to this server and validates JSON before sending credentials', async () => {
  const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher)
  for (const path of ['https://evil.example/api/test', '//evil.example/api/test', '/api/../../outside', '/api/\\evil.example', '/api/test#fragment']) {
    await expect(runConsole({ path, method: 'GET', body: '', token: 'secret' }, new AbortController().signal)).rejects.toThrow('path')
  }
  await expect(runConsole({ path: '/api/v1/statuses', method: 'POST', body: '{broken', token: 'secret' }, new AbortController().signal)).rejects.toThrow()
  expect(fetcher).not.toHaveBeenCalled()
})

it('reports console HTTP errors without clearing the logged-in account', async () => {
  const clear = vi.spyOn(tokenService, 'clear')
  const fetcher = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 }))
  vi.stubGlobal('fetch', fetcher)
  const result = await runConsole({ path: '/api/v1/accounts/verify_credentials', method: 'GET', body: '', token: 'invalid-personal-token' }, new AbortController().signal)
  expect(result.status).toBe(401)
  expect(clear).not.toHaveBeenCalled()
  expect(fetcher).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ redirect: 'error', credentials: 'omit', headers: expect.objectContaining({ Authorization: 'Bearer invalid-personal-token' }) }))
})

it('shows Matrix scopes as opt-in toggles that enable-all and read/write never imply', async () => {
  const { collapseScopes, matrixScopeRows, userScopeRows } = await import('../../../domain/tokenScopes')
  expect(matrixScopeRows.map(r => r.scope)).toEqual(['read:matrix', 'write:matrix'])
  expect(userScopeRows.some(r => r.scope.endsWith(':matrix'))).toBe(false)
  expect(collapseScopes([...userScopeRows.map(r => r.scope), 'read:matrix'])).toEqual(['push', 'read', 'read:matrix', 'write'])
  vi.spyOn(connectionsApi, 'tokens').mockResolvedValue([])
  const create = vi.spyOn(connectionsApi, 'createToken').mockResolvedValue({ token: 't', item: { id: 'x', name: 'Bot', scopes: [], created_at: '', expires_at: null } })
  const wrapper = mount(AccessTokenSettings, { attachTo: document.body }); await flushPromises()
  await wrapper.findAll('button').find(button => button.text() === '生成访问令牌')!.trigger('click')
  await flushPromises()
  const switches = () => [...document.querySelectorAll<HTMLInputElement>('.token-perms--matrix input[type="checkbox"]')]
  expect(switches()).toHaveLength(2)
  document.querySelectorAll<HTMLButtonElement>('.token-perms .connection-actions button')[1]!.click()
  await flushPromises()
  expect(switches().every(s => !s.checked)).toBe(true)
  switches()[1]!.checked = true; switches()[1]!.dispatchEvent(new Event('change'))
  await flushPromises()
  const input = document.querySelector<HTMLInputElement>('#token-form input:not([type="checkbox"])')!
  input.value = 'Bot'; input.dispatchEvent(new Event('input'))
  await flushPromises()
  document.querySelector<HTMLFormElement>('#token-form')!.dispatchEvent(new Event('submit'))
  await flushPromises()
  expect(create).toHaveBeenCalledWith(expect.objectContaining({ scopes: ['push', 'read', 'write', 'write:matrix'] }))
  wrapper.unmount()
})
