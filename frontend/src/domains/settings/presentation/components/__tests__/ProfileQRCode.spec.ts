import { afterEach, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { MastodonAccount } from '@/shared/types/mastodon'
import ProfileQRCode from '../ProfileQRCode.vue'
import { sessionEpoch } from '@/shared/api/sessionScope'

const account = { id: '1', username: 'alice', display_name: 'Alice', url: 'https://local.example/@alice' } as MastodonAccount
afterEach(() => { vi.restoreAllMocks(); document.body.innerHTML = '' })

it('generates a real QR image and clears the previous identity on account switching', async () => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value: function (this: HTMLDialogElement) { this.setAttribute('open', '') } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value: function (this: HTMLDialogElement) { this.removeAttribute('open') } })
  const wrapper = mount(ProfileQRCode, { props: { account }, attachTo: document.body })
  await wrapper.get('.profile-qr-trigger').trigger('click')
  await vi.waitFor(() => {
    expect(document.querySelector('.profile-qr-content img')?.getAttribute('src')).toMatch(/^data:image\/gif;base64,/)
  })
  expect(document.querySelector('.profile-qr-content a')?.getAttribute('href')).toBe(account.url)
  await wrapper.setProps({ account: { ...account, id: '2', username: 'bob', url: 'https://local.example/@bob' } })
  expect(document.querySelector('.profile-qr-dialog')).toBeNull()
  await wrapper.get('.profile-qr-trigger').trigger('click')
  sessionEpoch.value++
  await vi.waitFor(() => expect(document.querySelector('.profile-qr-dialog')).toBeNull())
  wrapper.unmount()
})
