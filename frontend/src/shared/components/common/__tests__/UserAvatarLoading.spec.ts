import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it } from 'vitest'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { resetAvatarLoadCache } from '../avatarLoadCache'
import UserAvatar from '../UserAvatar.vue'

const account = { id: 'a', username: 'alice', display_name: 'Alice', avatar: '/avatar-a.png', avatar_default: '/api/v1/avatars/default/' + 'cd'.repeat(32) + '.png' } as MastodonAccount

describe('avatar loading state', () => {
  beforeEach(() => { resetAvatarLoadCache() })

  it('shows a placeholder until the image loads, then fades it in', async () => {
    const wrapper = mount(UserAvatar, { props: { account } })
    await flushPromises()
    expect(wrapper.get('.user-avatar').classes()).toContain('user-avatar--loading')
    expect(wrapper.get('.user-avatar').attributes('aria-busy')).toBe('true')
    expect(wrapper.get('img').classes()).not.toContain('is-loaded')
    await wrapper.get('img').trigger('load')
    expect(wrapper.get('.user-avatar').classes()).not.toContain('user-avatar--loading')
    expect(wrapper.get('img').classes()).toContain('is-loaded')
    expect(wrapper.get('img').classes()).not.toContain('is-instant')
  })

  it('treats an already-complete (cached) image as loaded without a fade', async () => {
    const complete = Object.getOwnPropertyDescriptor(HTMLImageElement.prototype, 'complete')
    const natural = Object.getOwnPropertyDescriptor(HTMLImageElement.prototype, 'naturalWidth')
    Object.defineProperty(HTMLImageElement.prototype, 'complete', { configurable: true, get: () => true })
    Object.defineProperty(HTMLImageElement.prototype, 'naturalWidth', { configurable: true, get: () => 48 })
    try {
      const wrapper = mount(UserAvatar, { props: { account } })
      await flushPromises()
      expect(wrapper.get('.user-avatar').classes()).not.toContain('user-avatar--loading')
      expect(wrapper.get('img').classes()).toEqual(expect.arrayContaining(['is-loaded', 'is-instant']))
    } finally {
      if (complete) Object.defineProperty(HTMLImageElement.prototype, 'complete', complete)
      if (natural) Object.defineProperty(HTMLImageElement.prototype, 'naturalWidth', natural)
    }
  })

  it('falls back to the default avatar on error and shows loading again for a new src', async () => {
    const wrapper = mount(UserAvatar, { props: { account } })
    await wrapper.get('img').trigger('load')
    await wrapper.setProps({ account: { ...account, avatar: '/avatar-b.png' } })
    await flushPromises()
    expect(wrapper.get('img').attributes('src')).toBe('/avatar-b.png')
    expect(wrapper.get('.user-avatar').classes()).toContain('user-avatar--loading')
    await wrapper.get('img').trigger('error')
    expect(wrapper.get('img').attributes('src')).toBe(account.avatar_default)
    expect(wrapper.get('.user-avatar').classes()).toContain('user-avatar--loading')
    await wrapper.get('img').trigger('load')
    expect(wrapper.get('.user-avatar').classes()).not.toContain('user-avatar--loading')
  })

  it('does not show the loading placeholder again once the same avatar URL has loaded', async () => {
    const first = mount(UserAvatar, { props: { account } })
    await first.get('img').trigger('load')
    const second = mount(UserAvatar, { props: { account: { ...account } } })
    await flushPromises()
    expect(second.get('.user-avatar').classes()).not.toContain('user-avatar--loading')
    expect(second.get('.user-avatar').attributes('aria-busy')).toBeUndefined()
    expect(second.get('img').attributes('loading')).toBe('eager')
    expect(second.get('img').classes()).toEqual(expect.arrayContaining(['is-loaded', 'is-instant']))
    first.unmount()
    second.unmount()
  })

  it('clears a sibling placeholder when another row finishes loading the same avatar URL', async () => {
    const first = mount(UserAvatar, { props: { account } })
    const second = mount(UserAvatar, { props: { account: { ...account } } })
    await flushPromises()
    expect(second.get('.user-avatar').classes()).toContain('user-avatar--loading')
    await first.get('img').trigger('load')
    await flushPromises()
    expect(second.get('.user-avatar').classes()).not.toContain('user-avatar--loading')
    expect(second.get('img').classes()).toContain('is-loaded')
    first.unmount()
    second.unmount()
  })

  it('still shows a placeholder for an avatar URL that has not loaded', async () => {
    const seen = mount(UserAvatar, { props: { account } })
    await seen.get('img').trigger('load')
    const other = mount(UserAvatar, { props: { account: { ...account, id: 'b', avatar: '/avatar-b.png' } } })
    await flushPromises()
    expect(other.get('.user-avatar').classes()).toContain('user-avatar--loading')
    seen.unmount()
    other.unmount()
  })
})
