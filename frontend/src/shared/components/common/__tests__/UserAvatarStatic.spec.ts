import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { MastodonAccount } from '@/shared/types/mastodon'
import UserAvatar from '../UserAvatar.vue'

const account = {
  id: 'f1', username: 'fictional', display_name: 'Fictional',
  avatar: '/proxy/media/anim', avatar_static: '/proxy/media/anim?static=1',
} as MastodonAccount

describe('animated avatars', () => {
  it('uses the small still in lists and the animated original only when asked', () => {
    const list = mount(UserAvatar, { props: { account, size: 40 } })
    expect(list.get('img').attributes('src')).toBe('/proxy/media/anim?static=1&size=48')
    list.unmount()
    const header = mount(UserAvatar, { props: { account, size: 128, animated: true } })
    expect(header.get('img').attributes('src')).toMatch(/^\/proxy\/media\/anim\?size=/)
    header.unmount()
  })
  it('falls back to the original when the still fails', async () => {
    const w = mount(UserAvatar, { props: { account, size: 40 } })
    await w.get('img').trigger('error')
    expect(w.get('img').attributes('src')).toBe('/proxy/media/anim?size=48')
    w.unmount()
  })
})
