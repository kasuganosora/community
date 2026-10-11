import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AuthorizedApps from '../AuthorizedApps.vue'
import PasskeySettings from '../PasskeySettings.vue'
import { authApi } from '@/domains/auth/infrastructure/authApi'

vi.mock('@/shared/utils/webauthn', () => ({ passkeySupported: () => true, createPasskey: vi.fn() }))
afterEach(() => vi.restoreAllMocks())

it('distinguishes an app loading failure from an empty list and allows retry', async () => {
  const load = vi.spyOn(authApi, 'listAuthorizedApps').mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce([])
  const w = mount(AuthorizedApps)
  expect(w.get('[role=status]').text()).toContain('加载')
  await flushPromises()
  expect(w.get('[role=alert]').text()).toContain('加载失败')
  expect(w.text()).not.toContain('还没有已授权')
  await w.get('[role=alert] button').trigger('click')
  await flushPromises()
  expect(load).toHaveBeenCalledTimes(2)
  expect(w.find('[role=alert]').exists()).toBe(false)
  expect(w.text()).toContain('还没有已授权')
  w.unmount()
})

it('shows a recoverable passkey list error instead of claiming there are no keys', async () => {
  vi.spyOn(authApi, 'listPasskeys').mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce([])
  const w = mount(PasskeySettings)
  await flushPromises()
  expect(w.get('[role=alert]').text()).toContain('加载失败')
  expect(w.text()).not.toContain('还没有通行密钥')
  await w.get('[role=alert] button').trigger('click')
  await flushPromises()
  expect(w.text()).toContain('还没有通行密钥')
  expect(w.get('label').attributes('for')).toBe(w.get('input').attributes('id'))
  w.unmount()
})
