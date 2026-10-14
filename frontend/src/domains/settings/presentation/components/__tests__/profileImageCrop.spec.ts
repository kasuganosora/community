import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { userApi } from '@/domains/user/infrastructure/userApi'
import type { MastodonAccount, MastodonAttachment } from '@/shared/types/mastodon'
import { sessionEpoch } from '@/shared/api/sessionScope'
import ProfileSettingsForm from '../ProfileSettingsForm.vue'
import ImageCropDialog from '@/shared/components/common/ImageCropDialog.vue'

const account = { id: 'crop-user', username: 'crop', display_name: 'Crop', avatar: '/original.png', header: '/original-cover.png', source: { note: '' } } as MastodonAccount
let wrappers: VueWrapper[] = []
beforeEach(() => { vi.spyOn(postApi, 'uploadMedia').mockResolvedValue({ url: '/cropped.png' } as MastodonAttachment) })
afterEach(() => { wrappers.forEach(w => w.unmount()); wrappers = []; vi.restoreAllMocks() })
async function setup() {
  const pinia = createPinia()
  const auth = useAuthStore(pinia)
  auth.currentUser = { ...account }
  vi.spyOn(auth, 'fetchCurrentUser').mockResolvedValue(true)
  const wrapper = mount(ProfileSettingsForm, { global: { plugins: [pinia], stubs: { UserAvatar: true, ImageCropDialog: true } } })
  wrappers.push(wrapper)
  await flushPromises()
  return { wrapper, auth }
}
async function pick(wrapper: VueWrapper, kind: 'avatar' | 'header') {
  const file = new File(['source'], 'original.png', { type: 'image/png' })
  const input = wrapper.findAll('input[type=file]')[kind === 'avatar' ? 0 : 1]!
  Object.defineProperty(input.element, 'files', { configurable: true, value: [file] })
  await input.trigger('change')
  return file
}

it.each(['avatar', 'header'] as const)('does not upload or replace %s until the crop is applied', async kind => {
  const { wrapper } = await setup()
  const selected = await pick(wrapper, kind)
  expect(wrapper.getComponent(ImageCropDialog).props()).toMatchObject({ kind, file: selected })
  expect(postApi.uploadMedia).not.toHaveBeenCalled()
  wrapper.getComponent(ImageCropDialog).vm.$emit('cancel')
  await flushPromises()
  expect(wrapper.findComponent(ImageCropDialog).exists()).toBe(false)
  expect(postApi.uploadMedia).not.toHaveBeenCalled()
  expect(wrapper.getComponent({ name: 'UserAvatar' }).props('account').avatar).toBe(account.avatar)
  expect(wrapper.get('.settings-hero__cover img').attributes('src')).toBe(account.header)

  await pick(wrapper, kind)
  const cropped = new File(['crop'], 'cropped.png', { type: 'image/png' })
  const update = vi.spyOn(userApi, 'updateCredentials').mockResolvedValue({ ...account, [kind]: '/cropped.png' })
  wrapper.getComponent(ImageCropDialog).vm.$emit('apply', cropped)
  await flushPromises()
  expect(postApi.uploadMedia).toHaveBeenCalledExactlyOnceWith(cropped)
  expect(update).not.toHaveBeenCalled()
  await wrapper.get('form').trigger('submit')
  expect(update).toHaveBeenCalledWith(expect.objectContaining({ [kind]: '/cropped.png' }))
})

it('ignores an upload completed after logout and closes a pending crop on session change', async () => {
  const { wrapper, auth } = await setup()
  let resolve!: (result: MastodonAttachment) => void
  vi.mocked(postApi.uploadMedia).mockImplementationOnce(() => new Promise(r => { resolve = r }))
  await pick(wrapper, 'avatar')
  wrapper.getComponent(ImageCropDialog).vm.$emit('apply', new File(['crop'], 'crop.png', { type: 'image/png' }))
  await flushPromises()
  auth.currentUser = null
  sessionEpoch.value++
  await flushPromises()
  resolve({ url: '/previous-account.png' } as MastodonAttachment)
  await flushPromises()
  expect(wrapper.html()).not.toContain('/previous-account.png')
  auth.currentUser = { ...account }
  await flushPromises()
  await pick(wrapper, 'header')
  sessionEpoch.value++
  await flushPromises()
  expect(wrapper.findComponent(ImageCropDialog).exists()).toBe(false)
})

it('shows upload failure without replacing the existing image', async () => {
  const { wrapper } = await setup()
  vi.mocked(postApi.uploadMedia).mockRejectedValueOnce(new Error('上传中断'))
  await pick(wrapper, 'header')
  wrapper.getComponent(ImageCropDialog).vm.$emit('apply', new File(['crop'], 'crop.png', { type: 'image/png' }))
  await flushPromises()
  expect(wrapper.get('[role=alert]').text()).toBe('上传中断')
  expect(wrapper.get('.settings-hero__cover img').attributes('src')).toBe(account.header)
})

it('does not install an old account profile when a save resolves after an account switch', async () => {
  const { wrapper, auth } = await setup()
  let resolve!: (result: MastodonAccount) => void
  vi.spyOn(userApi, 'updateCredentials').mockImplementationOnce(() => new Promise(done => { resolve = done }))
  await wrapper.get('#set-display-name').setValue('Old account draft')
  await wrapper.get('form').trigger('submit')
  sessionEpoch.value++
  auth.currentUser = { ...account, id: 'other-user', display_name: 'New account' }
  await flushPromises()
  resolve({ ...account, display_name: 'Old account saved' }); await flushPromises()
  expect(auth.currentUser.id).toBe('other-user')
  expect(wrapper.get('#set-display-name').element).toHaveProperty('value', 'New account')
  expect(wrapper.text()).not.toContain('Old account saved')
})
