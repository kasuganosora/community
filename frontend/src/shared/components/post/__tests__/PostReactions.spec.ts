import { describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import PostReactions from '../PostReactions.vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { editedStatuses } from '@/domains/post/presentation/composables/useStatusActions'
import type { MastodonStatus, MastodonAccount } from '@/shared/types/mastodon'
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
})
