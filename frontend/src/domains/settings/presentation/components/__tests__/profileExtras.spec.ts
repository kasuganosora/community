import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { userApi } from '@/domains/user/infrastructure/userApi'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { sessionEpoch } from '@/shared/api/sessionScope'
import ProfileSettingsForm from '../ProfileSettingsForm.vue'

const account = {
  id: 'extras', username: 'alice', display_name: 'Alice', avatar: '/avatar.png', header: '',
  note: '<p>Rendered bio</p>', bot: false, source: { note: 'Raw bio', fields: [{ name: 'Website', value: 'https://example.test/?a=1&b=2' }], language: 'ja' },
  fields: [{ name: 'Website', value: '<a href="https://example.test">Rendered link</a>' }],
} as MastodonAccount
const wrappers: VueWrapper[] = []
afterEach(() => { wrappers.splice(0).forEach(w => w.unmount()); vi.restoreAllMocks() })
function setup(user = account, pendingRefresh?: () => Promise<boolean>) {
  const pinia = createPinia()
  const auth = useAuthStore(pinia)
  auth.currentUser = structuredClone(user)
  const refresh = vi.spyOn(auth, 'fetchCurrentUser')
  if (pendingRefresh) refresh.mockImplementation(pendingRefresh)
  else refresh.mockResolvedValue(true)
  const wrapper = mount(ProfileSettingsForm, { global: { plugins: [pinia], stubs: { UserAvatar: true, ImageCropDialog: true, AvatarDecorationPicker: true, ProfileQRCode: true } } })
  wrappers.push(wrapper)
  return { wrapper, auth, refresh }
}

it('saves all profile extras with raw fields and previews cat ears immediately', async () => {
  const { wrapper } = setup()
  await flushPromises()
  expect(wrapper.findAll('.profile-fields__row')).toHaveLength(4)
  expect(wrapper.get('.profile-fields__value input').element).toHaveProperty('value', 'https://example.test/?a=1&b=2')
  expect(wrapper.get('#set-profile-language').element).toHaveProperty('value', 'ja')
  await wrapper.get('#set-location').setValue('Tokyo')
  await wrapper.get('#set-birthday').setValue('2000-02-29')
  await wrapper.get('#set-profile-language').setValue('zh-CN')
  await wrapper.get('#set-followed-message').setValue('欢迎关注')
  await wrapper.get('#set-reaction-acceptance').setValue('nonSensitiveOnlyForLocalLikeOnlyForRemote')
  await wrapper.get('#set-is-cat').setValue(true)
  await wrapper.get('#set-is-bot').setValue(true)
  expect(wrapper.getComponent({ name: 'UserAvatar' }).props('account').is_cat).toBe(true)
  const update = vi.spyOn(userApi, 'updateCredentials').mockResolvedValue(account)
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(update).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({
    location: 'Tokyo', birthday: '2000-02-29', language: 'zh-CN', followed_message: '欢迎关注',
    reaction_acceptance: 'nonSensitiveOnlyForLocalLikeOnlyForRemote', is_cat: true, bot: true,
    fields_attributes: [{ name: 'Website', value: 'https://example.test/?a=1&b=2' }],
  }))
})

it('does not replace unavailable source fields with rendered HTML or an empty array', async () => {
  const { wrapper } = setup({ ...account, source: { note: 'Raw bio' } })
  await flushPromises()
  expect(wrapper.get('.profile-fields__value input').attributes('disabled')).toBeDefined()
  expect(wrapper.get('.profile-fields__value input').element).toHaveProperty('value', '')
  const update = vi.spyOn(userApi, 'updateCredentials').mockResolvedValue(account)
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(update.mock.calls[0]?.[0]).not.toHaveProperty('fields_attributes')
})

it('preserves more than four imported fields and submits removal as an empty array', async () => {
  const fields = Array.from({ length: 7 }, (_, i) => ({ name: `Label ${i}`, value: `Value ${i}` }))
  const user = { ...account, source: { note: '', fields } }
  const { wrapper } = setup(user)
  await flushPromises()
  expect(wrapper.findAll('.profile-fields__row')).toHaveLength(7)
  for (let i = 0; i < 7; i++) await wrapper.findAll('[data-action=remove]')[0]!.trigger('click')
  const update = vi.spyOn(userApi, 'updateCredentials').mockResolvedValue(user)
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(update.mock.calls[0]?.[0]).toHaveProperty('fields_attributes', [])
})

it('keeps edited fields when initial refresh completes after typing', async () => {
  let finish!: (value: boolean) => void
  const { wrapper, auth } = setup(account, () => new Promise(resolve => { finish = resolve }))
  await flushPromises()
  await wrapper.get('.profile-fields__value input').setValue('Draft value')
  auth.currentUser = { ...account, source: { note: 'Server bio', fields: [{ name: 'Website', value: 'Server value' }] } }
  finish(true); await flushPromises()
  expect(wrapper.get('.profile-fields__value input').element).toHaveProperty('value', 'Draft value')
})

it('clears extras on account switch and ignores the old account save result', async () => {
  const { wrapper, auth } = setup()
  await flushPromises()
  await wrapper.get('#set-location').setValue('Private old location')
  await wrapper.get('#set-followed-message').setValue('Old message')
  let finish!: (value: MastodonAccount) => void
  vi.spyOn(userApi, 'updateCredentials').mockImplementation(() => new Promise(resolve => { finish = resolve }))
  await wrapper.get('form').trigger('submit')
  sessionEpoch.value++
  auth.currentUser = { ...account, id: 'other', source: { note: '', fields: [] } }
  await flushPromises()
  expect(wrapper.get('#set-location').element).toHaveProperty('value', '')
  expect(wrapper.get('#set-followed-message').element).toHaveProperty('value', '')
  expect(wrapper.get('.profile-fields__value input').element).toHaveProperty('value', '')
  finish(account); await flushPromises()
  expect(auth.currentUser?.id).toBe('other')
  expect(wrapper.get('#set-location').element).toHaveProperty('value', '')
})

it('loads and saves the require sign-in to view toggle', async () => {
  const { wrapper } = setup({ ...account, require_signin_to_view: true })
  await flushPromises()
  const toggle = wrapper.get('#set-require-signin')
  expect(toggle.element).toHaveProperty('checked', true)
  await toggle.setValue(false)
  const update = vi.spyOn(userApi, 'updateCredentials').mockResolvedValue(account)
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(update).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ require_signin_to_view: false }))
})
