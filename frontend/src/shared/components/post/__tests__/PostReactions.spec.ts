import { afterEach, describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import PostReactions from '../PostReactions.vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { editedStatuses } from '@/domains/post/presentation/composables/useStatusActions'
import type { MastodonStatus, MastodonAccount } from '@/shared/types/mastodon'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
afterEach(() => vi.restoreAllMocks())
const status = { id: 'reaction-post', emoji_reactions: [{ name: '👍', count: 3, me: true }, { name: '🎉', count: 2, me: false }] } as MastodonStatus
function mountReaction() {
  const pinia = createPinia(); useAuthStore(pinia).currentUser = { id: 'me' } as MastodonAccount
  return shallowMount(PostReactions, { props: { status }, global: { plugins: [pinia], renderStubDefaultSlot: true } })
}
function byLabel(wrapper: VueWrapper, label: string) { return wrapper.findAll('button').find(b => b.attributes('aria-label') === label)! }
describe('post emoji reactions', () => {
  it('optimistically switches one reaction and restores original counts on permission failure', async () => {
    let reject!: (e: Error) => void
    const react = vi.spyOn(postApi, 'react').mockImplementation(() => new Promise((_, fail) => { reject = fail }))
    const wrapper = mountReaction()
    await byLabel(wrapper, '🎉 2 个回应').trigger('click')
    expect(byLabel(wrapper, '👍 2 个回应').attributes('aria-pressed')).toBe('false')
    expect(byLabel(wrapper, '🎉 3 个回应，撤销我的回应').attributes('disabled')).toBeDefined()
    expect(react).toHaveBeenCalledWith(status.id, '🎉', false)
    reject(new Error('403')); await flushPromises()
    expect(byLabel(wrapper, '👍 3 个回应，撤销我的回应').attributes('aria-pressed')).toBe('true')
    expect(wrapper.text()).toContain('已恢复原状态')
    wrapper.unmount()
  })
  it('removes my reaction and publishes the returned status to all views', async () => {
    const next = { ...status, emoji_reactions: [{ name: '👍', count: 2, me: false }] }
    const react = vi.spyOn(postApi, 'react').mockResolvedValue(next)
    const wrapper = mountReaction(); await byLabel(wrapper, '👍 3 个回应，撤销我的回应').trigger('click'); await flushPromises()
    expect(react).toHaveBeenCalledWith(status.id, '👍', true)
    expect(editedStatuses.get(status.id)?.emoji_reactions).toEqual(next.emoji_reactions)
    editedStatuses.delete(status.id); wrapper.unmount()
  })
  it('does not display stale responder names after a failed access check', async () => {
    vi.spyOn(postApi, 'reactions').mockRejectedValue(new Error('403'))
    const wrapper = mountReaction(); await wrapper.findAll('button').find(b => b.text() === '查看回应者')!.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('无法读取回应者')
    expect(wrapper.findAll('li')).toHaveLength(0)
    wrapper.unmount()
  })
  it('shows a selected quick reaction and returns keyboard focus when closing', async () => {
    const wrapper = mountReaction()
    const add = byLabel(wrapper, '添加表情回应')
    await add.trigger('click')
    expect(wrapper.findAll('.reaction-quickbar__item')).toHaveLength(6)
    expect(byLabel(wrapper, '回应 👍').attributes('aria-pressed')).toBe('true')
    await wrapper.find('.post-reactions').trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('.reaction-quickbar').exists()).toBe(false)
    expect(add.attributes('aria-expanded')).toBe('false')
    wrapper.unmount()
  })
  it('renders responders with actual account avatars without loading sensitive emoji images', async () => {
    const account = { id: 'alice', username: 'alice', acct: 'alice@remote.test', display_name: 'Alice', avatar: 'https://remote.test/avatar.png' } as MastodonAccount
    vi.spyOn(postApi, 'reactions').mockResolvedValue([{ name: ':sensitive:', count: 1, me: false, is_sensitive: true, url: 'https://remote.test/sensitive.png', accounts: [account] }])
    const wrapper = mountReaction()
    await wrapper.find('.post-reactions__details').trigger('click'); await flushPromises()
    expect(wrapper.findComponent(UserAvatar).props('account')).toEqual(account)
    expect(wrapper.find('.responders__handle').text()).toBe('@alice@remote.test')
    expect(wrapper.findAll('img[src="https://remote.test/sensitive.png"]')).toHaveLength(0)
    wrapper.unmount()
  })
  it('discards responder identities when the session changes during a request', async () => {
    let resolve!: (rows: Awaited<ReturnType<typeof postApi.reactions>>) => void
    vi.spyOn(postApi, 'reactions').mockImplementation(() => new Promise(done => { resolve = done }))
    const wrapper = mountReaction()
    await wrapper.find('.post-reactions__details').trigger('click')
    sessionEpoch.value++
    resolve([{ name: '👍', count: 1, me: false, accounts: [{ id: 'private', username: 'private' } as MastodonAccount] }])
    await flushPromises()
    expect(wrapper.findAll('.responders__account')).toHaveLength(0)
    wrapper.unmount()
  })

})
