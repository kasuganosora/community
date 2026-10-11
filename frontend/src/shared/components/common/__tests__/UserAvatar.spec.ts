import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { MastodonAccount } from '@/shared/types/mastodon'
import UserAvatar from '../UserAvatar.vue'

const account = { id: 'a', username: 'alice', display_name: 'Alice', avatar: '/avatar-a.png' } as MastodonAccount

describe('fixed-size account avatars', () => {
  it('keeps the accessible name, size and media URL policy without automatic character measurement', () => {
    const wrapper = mount(UserAvatar, { props: { account: { ...account, avatar: 'https://canonical.test/media/4/a.png' }, size: 48 } })
    expect(wrapper.get('[role="img"]').attributes('aria-label')).toBe("Alice's avatar")
    expect(wrapper.get('.avatar-frame').attributes('style')).toContain('width: 48px')
    expect(wrapper.get('.user-avatar').attributes('style')).toContain('font-size: 24px')
    expect(wrapper.get('img').attributes('src')).toBe('/media/4/a.png')
    wrapper.unmount()
  })
  it('falls back on image failure and loads a replacement URL without retaining the old error', async () => {
    const wrapper = mount(UserAvatar, { props: { account } })
    await wrapper.get('img').trigger('error')
    expect(wrapper.find('.user-avatar__image').exists()).toBe(false)
    expect(wrapper.get('.user-avatar').text()).toBe('A')
    await wrapper.setProps({ account: { ...account, avatar: '/avatar-b.png' } })
    expect(wrapper.get('.user-avatar__image').attributes('src')).toBe('/avatar-b.png')
    wrapper.unmount()
  })
  it('keeps decoration failures independent and rejects unsupported decoration URLs', async () => {
    const wrapper = mount(UserAvatar, { props: { account: { ...account, avatar_decoration_url: '/api/v1/avatar_decorations/7/image' } } })
    await wrapper.get('.avatar-decoration').trigger('error')
    expect(wrapper.get('.avatar-decoration').attributes('style')).toContain('visibility: hidden')
    expect(wrapper.find('.user-avatar__image').exists()).toBe(true)
    await wrapper.setProps({ account: { ...account, avatar: '', avatar_decoration_url: 'https://remote.test/decor.png' } })
    expect(wrapper.find('.avatar-decoration').exists()).toBe(false)
    expect(wrapper.get('.user-avatar').text()).toBe('A')
    wrapper.unmount()
  })
})
