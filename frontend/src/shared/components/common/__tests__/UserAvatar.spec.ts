import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { MastodonAccount } from '@/shared/types/mastodon'
import UserAvatar from '../UserAvatar.vue'

const account = { id: 'a', username: 'alice', display_name: 'Alice', avatar: '/avatar-a.png' } as MastodonAccount

describe('fixed-size account avatars', () => {
  const defaultUrl = '/api/v1/avatars/default/' + 'ab'.repeat(32) + '.png'
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
    expect(wrapper.find('.user-avatar__identicon').exists()).toBe(true)
    expect(wrapper.get('.user-avatar').text()).toBe('')
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
    expect(wrapper.find('.user-avatar__identicon').exists()).toBe(true)
    expect(wrapper.get('.user-avatar').text()).toBe('')
    wrapper.unmount()
  })
  it('prefers real photos and falls back to the same generated identity if both image requests fail', async () => {
    const wrapper = mount(UserAvatar, { props: { account: { ...account, avatar_default: 'https://canonical.test' + defaultUrl } } })
    expect(wrapper.get('.user-avatar__image').attributes('src')).toBe('/avatar-a.png')
    await wrapper.get('.user-avatar__image').trigger('error')
    expect(wrapper.get('.user-avatar__image').attributes('src')).toBe(defaultUrl)
    await wrapper.get('.user-avatar__image').trigger('error')
    expect(wrapper.find('.user-avatar__identicon').exists()).toBe(true)
    expect(wrapper.findAll('.user-avatar__identicon rect').length).toBeGreaterThan(1)
    expect(wrapper.get('.user-avatar').text()).toBe('')
    await wrapper.setProps({ account: { ...account, avatar: '/new-photo.png', avatar_default: defaultUrl } })
    expect(wrapper.get('.user-avatar__image').attributes('src')).toBe('/new-photo.png')
    wrapper.unmount()
  })
  it('does not use the shared brand logo from legacy responses and keeps identity across display-name changes', async () => {
    const legacy = { ...account, avatar: '/default-assets/avatar.png', acct: 'alice@remote.test' }
    const wrapper = mount(UserAvatar, { props: { account: legacy } })
    const initial = wrapper.get('svg').html()
    expect(wrapper.find('.user-avatar__image').exists()).toBe(false)
    await wrapper.setProps({ account: { ...legacy, display_name: 'New display name' } })
    expect(wrapper.get('svg').html()).toBe(initial)
    await wrapper.setProps({ account: { ...legacy, acct: 'alice@another.test' } })
    expect(wrapper.get('svg').html()).not.toBe(initial)
    wrapper.unmount()
  })

})
