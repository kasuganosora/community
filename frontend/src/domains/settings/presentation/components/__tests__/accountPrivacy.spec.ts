import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { userApi } from '@/domains/user/infrastructure/userApi'
import type { MastodonAccount } from '@/shared/types/mastodon'
import ProfileSettingsForm from '../ProfileSettingsForm.vue'

const account = {
  id: 'privacy', username: 'neko', display_name: 'Neko', avatar: '', header: '', note: '', bot: false, locked: true, is_cat: true,
  hide_collections: true, source: { note: '', fields: [], following_visibility: 'public', followers_visibility: 'private', auto_accept_followed: true, hide_online_status: false, speak_as_cat: true },
} as unknown as MastodonAccount
const wrappers: VueWrapper[] = []
afterEach(() => { wrappers.splice(0).forEach(w => w.unmount()); vi.restoreAllMocks() })
function setup(user: MastodonAccount) {
  const pinia = createPinia()
  const auth = useAuthStore(pinia)
  auth.currentUser = structuredClone(user)
  vi.spyOn(auth, 'fetchCurrentUser').mockResolvedValue(true)
  const wrapper = mount(ProfileSettingsForm, { global: { plugins: [pinia], stubs: { UserAvatar: true, ImageCropDialog: true, AvatarDecorationPicker: true, ProfileQRCode: true } } })
  wrappers.push(wrapper)
  return wrapper
}

it('loads and saves the Misskey account privacy settings', async () => {
  const wrapper = setup(account)
  await flushPromises()
  expect(wrapper.get('#set-following-visibility').element).toHaveProperty('value', 'public')
  expect(wrapper.get('#set-followers-visibility').element).toHaveProperty('value', 'private')
  expect(wrapper.get('#set-auto-accept').element).toHaveProperty('checked', true)
  expect(wrapper.get('#set-speak-as-cat').element).toHaveProperty('checked', true)
  await wrapper.get('#set-following-visibility').setValue('followers')
  await wrapper.get('#set-hide-online').setValue(true)
  await wrapper.get('#set-speak-as-cat').setValue(false)
  const update = vi.spyOn(userApi, 'updateCredentials').mockResolvedValue(account)
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(update).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({
    following_visibility: 'followers', followers_visibility: 'private', auto_accept_followed: true, hide_online_status: true, speak_as_cat: false,
  }))
})

it('falls back to hide_collections, hides speak-as-cat for non-cats and disables auto-accept when unlocked', async () => {
  const wrapper = setup({ ...account, is_cat: false, locked: false, source: { note: '' } } as MastodonAccount)
  await flushPromises()
  expect(wrapper.get('#set-following-visibility').element).toHaveProperty('value', 'private')
  expect(wrapper.find('#set-speak-as-cat').exists()).toBe(false)
  expect(wrapper.get('#set-auto-accept').attributes('disabled')).toBeDefined()
})
