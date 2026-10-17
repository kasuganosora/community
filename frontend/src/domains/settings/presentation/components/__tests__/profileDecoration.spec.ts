import { afterAll, beforeAll, afterEach, beforeEach, expect, it, vi } from 'vitest'
import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { communityAssetsApi } from '@/domains/admin/infrastructure/communityAssetsApi'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { sessionEpoch } from '@/shared/api/sessionScope'
import ProfileSettingsForm from '../ProfileSettingsForm.vue'

const account = { id: 'me', username: 'luna', display_name: 'luna', avatar: '/avatar.png', header: '', source: { note: '' }, avatar_decorations: [] } as unknown as MastodonAccount
let wrappers: VueWrapper[] = []
beforeAll(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value(this: HTMLDialogElement) { this.open = true } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value(this: HTMLDialogElement) { this.open = false } })
})
afterAll(() => { Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal'); Reflect.deleteProperty(HTMLDialogElement.prototype, 'close') })
beforeEach(() => {
  vi.spyOn(communityAssetsApi, 'decorations').mockResolvedValue([{ id: 'cat', name: '猫耳', image_url: '/cat.png', enabled: true }])
  vi.spyOn(communityAssetsApi, 'decorationSettings').mockResolvedValue({ limit: 1, max_limit: 16 })
  vi.spyOn(communityAssetsApi, 'selectDecorations').mockResolvedValue({ avatar_decorations: [], limit: 1 })
})
afterEach(() => { wrappers.forEach(w => w.unmount()); wrappers = []; document.body.innerHTML = ''; vi.restoreAllMocks() })
async function setup() {
  const pinia = createPinia()
  const auth = useAuthStore(pinia)
  auth.currentUser = { ...account }
  vi.spyOn(auth, 'fetchCurrentUser').mockResolvedValue(true)
  const wrapper = mount(ProfileSettingsForm, { attachTo: document.body, global: { plugins: [pinia], stubs: { UserAvatar: true, 't-icon': true, ImageCropDialog: true } } })
  wrappers.push(wrapper)
  await flushPromises()
  return { wrapper: new DOMWrapper(document.body), auth }
}
async function open(wrapper: DOMWrapper<HTMLElement>) {
  await wrapper.get('.settings-hero__actions button').trigger('click')
  await flushPromises()
}

it('loads decorations only on demand and discards unsaved choices when dismissed', async () => {
  const { wrapper } = await setup()
  expect(communityAssetsApi.decorations).not.toHaveBeenCalled()
  await open(wrapper)
  await wrapper.get('.decoration-option').trigger('click')
  expect(wrapper.find('.decoration-picker__selected').exists()).toBe(true)
  await wrapper.get('.settings-decoration-dialog__close').trigger('click')
  expect(wrapper.find('dialog').exists()).toBe(false)
  expect(communityAssetsApi.selectDecorations).not.toHaveBeenCalled()
  await open(wrapper)
  expect(wrapper.find('.decoration-picker__selected').exists()).toBe(false)
})

it('saves once before closing and keeps the profile draft', async () => {
  const { wrapper, auth } = await setup()
  await wrapper.get('#set-display-name').setValue('正在编辑的昵称')
  await open(wrapper)
  await wrapper.get('.decoration-option').trigger('click')
  await wrapper.get('.decoration-picker').trigger('submit')
  await flushPromises()
  expect(communityAssetsApi.selectDecorations).toHaveBeenCalledExactlyOnceWith([{ id: 'cat', angle: 0, flip_h: false, offset_x: 0, offset_y: 0 }])
  expect(auth.fetchCurrentUser).toHaveBeenCalledTimes(2)
  expect(wrapper.find('dialog').exists()).toBe(false)
  expect(wrapper.get('#set-display-name').element).toHaveProperty('value', '正在编辑的昵称')
})

it('prevents dismissal during a save and ignores the old result after switching accounts', async () => {
  const { wrapper, auth } = await setup()
  let resolve!: (value: { avatar_decorations: []; limit: number }) => void
  vi.mocked(communityAssetsApi.selectDecorations).mockImplementationOnce(() => new Promise(done => { resolve = done }))
  await open(wrapper)
  await wrapper.get('.decoration-option').trigger('click')
  await wrapper.get('.decoration-picker').trigger('submit')
  expect(wrapper.get('.settings-decoration-dialog__close').attributes('disabled')).toBeDefined()
  await wrapper.get('dialog').trigger('cancel')
  expect(wrapper.find('dialog').exists()).toBe(true)
  sessionEpoch.value++
  auth.currentUser = { ...account, id: 'other', display_name: 'Other account' }
  await flushPromises()
  expect(wrapper.find('dialog').exists()).toBe(false)
  resolve({ avatar_decorations: [], limit: 1 })
  await flushPromises()
  expect(auth.fetchCurrentUser).toHaveBeenCalledTimes(1)
  expect(wrapper.get('#set-display-name').element).toHaveProperty('value', 'Other account')
})

it('shows an empty decoration collection without a large preview or useless save button', async () => {
  vi.mocked(communityAssetsApi.decorations).mockResolvedValueOnce([])
  const { wrapper } = await setup()
  await open(wrapper)
  expect(wrapper.get('.decoration-picker').text()).toContain('暂无可用挂件')
  expect(wrapper.find('.decoration-picker__preview').exists()).toBe(false)
  expect(wrapper.find('.decoration-picker button[type=submit]').exists()).toBe(false)
})

it('keeps choices and permits retry after saving fails', async () => {
  vi.mocked(communityAssetsApi.selectDecorations).mockRejectedValueOnce(new Error('offline'))
  const { wrapper } = await setup()
  await open(wrapper)
  await wrapper.get('.decoration-option').trigger('click')
  await wrapper.get('.decoration-picker').trigger('submit')
  await flushPromises()
  expect(wrapper.get('.decoration-picker [role=alert]').text()).toContain('失败')
  expect(wrapper.find('.decoration-picker__selected').exists()).toBe(true)
  expect(wrapper.find('.settings-decoration-dialog__close').exists()).toBe(true)
  await wrapper.get('.decoration-picker').trigger('submit')
  await flushPromises()
  expect(wrapper.find('dialog').exists()).toBe(false)
})
