import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import AccessTokenSettings from '../AccessTokenSettings.vue'
import { connectionsApi } from '../../../infrastructure/connectionsApi'

beforeEach(() => { setActivePinia(createPinia()); window.matchMedia ??= ((query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia })
afterEach(() => { vi.restoreAllMocks(); document.body.innerHTML = '' })

it('edits a token in the prefilled sheet without changing the token', async () => {
  const row = { id: 't1', name: 'tbtest', scopes: ['read'], created_at: '', expires_at: null, last_used_at: null }
  vi.spyOn(connectionsApi, 'tokens').mockResolvedValue([row])
  const create = vi.spyOn(connectionsApi, 'createToken')
  const update = vi.spyOn(connectionsApi, 'updateToken').mockResolvedValue({ ...row, name: 'tbtest', scopes: ['read', 'write:statuses'] })
  const wrapper = mount(AccessTokenSettings, { attachTo: document.body }); await flushPromises()
  await wrapper.get('[data-action=edit]').trigger('click'); await flushPromises()
  const name = document.querySelector<HTMLInputElement>('#token-form input:not([type="checkbox"])')!
  expect(name.value).toBe('tbtest')
  expect(document.querySelector('#token-form select')).toBeNull()
  expect(document.querySelector('.token-expiry-readonly')).not.toBeNull()
  const toggle = (scope: string) => [...document.querySelectorAll<HTMLLabelElement>('#token-form .connection-toggle')].find(l => l.textContent?.includes(scope))!.querySelector('input')!
  expect(toggle('read:notifications').checked).toBe(true)
  toggle('write:statuses').click(); await flushPromises()
  document.querySelector<HTMLFormElement>('#token-form')!.dispatchEvent(new Event('submit')); await flushPromises()
  expect(create).not.toHaveBeenCalled()
  expect(update).toHaveBeenCalledWith('t1', { name: 'tbtest', scopes: ['read', 'write:statuses'] })
  expect(wrapper.text()).toContain('tbtest')
  wrapper.unmount()
})
