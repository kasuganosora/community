import { expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ProfileSettingsForm from '../ProfileSettingsForm.vue'

const auth = vi.hoisted(() => ({
  currentUser: { username: 'test', display_name: 'Before', note: '', source: { note: '' }, avatar: '', header: '', locked: false },
  fetchCurrentUser: vi.fn(),
}))
vi.mock('@/domains/auth/presentation/stores/authStore', () => ({ useAuthStore: () => auth }))

it('retains a draft when the initial profile refresh resolves after typing', async () => {
  let resolve!: () => void
  auth.fetchCurrentUser.mockImplementationOnce(() => new Promise<void>(r => { resolve = r }))
  const w = mount(ProfileSettingsForm, { global: { stubs: { UserAvatar: true } } })
  await w.get('#set-display-name').setValue('Unsaved name')
  auth.currentUser.display_name = 'Server name'
  resolve()
  await flushPromises()
  expect(w.get('#set-display-name').element).toHaveProperty('value', 'Unsaved name')
  await w.get('button[type=button]').trigger('click')
  expect(w.get('#set-display-name').element).toHaveProperty('value', 'Server name')
  w.unmount()
})
