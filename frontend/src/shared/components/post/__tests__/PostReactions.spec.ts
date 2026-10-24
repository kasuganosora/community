import { afterEach, describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createPinia, type Pinia } from 'pinia'
import PostReactions from '../PostReactions.vue'
import EmojiPicker from '../EmojiPicker.vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useEmojiStore } from '@/domains/emoji/presentation/stores/emojiStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { editedStatuses } from '@/domains/post/presentation/composables/useStatusActions'
import type { MastodonStatus, MastodonAccount, MastodonEmoji } from '@/shared/types/mastodon'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import client from '@/shared/api/client'

const kinoko: MastodonEmoji = { shortcode: 'Kinoko7_27', url: 'https://cdn.test/kinoko.png', static_url: 'https://cdn.test/kinoko.png', visible_in_picker: true }
afterEach(() => vi.restoreAllMocks())
const status = { id: 'reaction-post', emoji_reactions: [{ name: '👍', count: 3, me: true }, { name: '🎉', count: 2, me: false }] } as MastodonStatus
function mountReaction(props: { status: MastodonStatus } = { status }, pinia: Pinia = createPinia()) {
  useAuthStore(pinia).currentUser = { id: 'me' } as MastodonAccount
  return shallowMount(PostReactions, { props, global: { plugins: [pinia], renderStubDefaultSlot: true } })
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
  it('mounts responder UI only when opened and refreshes access after closing and reopening', async () => {
    const account = { id: 'alice', username: 'alice', acct: 'alice', display_name: 'Alice' } as MastodonAccount
    const request = vi.spyOn(postApi, 'reactions')
      .mockResolvedValueOnce([{ name: '👍', count: 1, me: false, accounts: [account] }])
      .mockRejectedValueOnce(new Error('403'))
    const wrapper = mountReaction()
    expect(wrapper.find('t-dialog-stub').exists()).toBe(false)
    expect(request).not.toHaveBeenCalled()
    await wrapper.find('.post-reactions__details').trigger('click')
    await flushPromises()
    expect(wrapper.find('.responders__handle').text()).toBe('@alice')
    wrapper.findComponent({ name: 'TDialog' }).vm.$emit('update:visible', false)
    await flushPromises()
    expect(wrapper.find('t-dialog-stub').exists()).toBe(false)
    expect(wrapper.find('.responders__account').exists()).toBe(false)
    await wrapper.find('.post-reactions__details').trigger('click')
    await flushPromises()
    expect(request).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('无法读取回应者')
    expect(wrapper.find('.responders__account').exists()).toBe(false)
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

  it('shows the picked custom emoji image before the reaction request returns', async () => {
    vi.spyOn(postApi, 'react').mockImplementation(() => new Promise(() => {}))
    const wrapper = mountReaction()
    await byLabel(wrapper, '添加表情回应').trigger('click')
    await wrapper.findComponent(EmojiPicker).vm.$emit('select', kinoko)
    await nextTick()
    expect(wrapper.get('.reaction img').attributes('src')).toBe(kinoko.url)
    expect(wrapper.text()).not.toContain(':Kinoko7_27:')
    wrapper.unmount()
  })

  it('replaces a shortcode reaction pill when the emoji catalogue finishes loading', async () => {
    const pending = new Promise(() => {})
    vi.spyOn(client, 'get').mockReturnValue(pending as never)
    const pinia = createPinia()
    const store = useEmojiStore(pinia)
    const custom = { ...status, id: 'custom-reaction', emoji_reactions: [{ name: ':Kinoko7_27:', count: 1, me: true }] } as MastodonStatus
    const wrapper = mountReaction({ status: custom }, pinia)
    expect(wrapper.text()).toContain(':Kinoko7_27:')
    expect(wrapper.find('.reaction img').exists()).toBe(false)
    store.emojis = [kinoko]
    await nextTick()
    expect(wrapper.get('.reaction img').attributes('src')).toBe(kinoko.url)
    expect(wrapper.text()).not.toContain(':Kinoko7_27:')
    wrapper.unmount()
  })

})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

describe('reaction request lifetime isolation', () => {
  it.each([
    ['account', 'success'], ['account', 'failure'], ['post', 'success'], ['post', 'failure'],
  ] as const)('ignores an old reaction %s/%s while the new request is pending', async (scope, outcome) => {
    const old = deferred<MastodonStatus>(), current = deferred<MastodonStatus>()
    const request = vi.spyOn(postApi, 'react').mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    editedStatuses.delete(status.id)
    const wrapper = mountReaction()
    await byLabel(wrapper, '🎉 2 个回应').trigger('click')
    if (scope === 'account') sessionEpoch.value++
    else await wrapper.setProps({ status: { ...status, id: 'new-reaction-post' } })
    await flushPromises()
    expect(byLabel(wrapper, '添加表情回应').attributes('disabled')).toBeUndefined()
    await byLabel(wrapper, '🎉 2 个回应').trigger('click')
    expect(request).toHaveBeenCalledTimes(2)
    if (outcome === 'success') old.resolve({ ...status, emoji_reactions: [] })
    else old.reject(new Error('old access denied'))
    await flushPromises()
    expect(editedStatuses.has(status.id)).toBe(false)
    expect(wrapper.find('[role=alert]').exists()).toBe(false)
    expect(byLabel(wrapper, '🎉 3 个回应，撤销我的回应').attributes('disabled')).toBeDefined()
    expect(wrapper.find('.post-reactions__row').attributes('aria-busy')).toBe('true')
    current.reject(new Error('current access denied')); await flushPromises()
    expect(wrapper.text()).toContain('已恢复原状态')
    expect(wrapper.find('.post-reactions__row').attributes('aria-busy')).toBe('false')
    wrapper.unmount()
  })

  it.each(['success', 'failure'] as const)('does not let an old responder %s end the reopened dialog loading state', async outcome => {
    type Rows = Awaited<ReturnType<typeof postApi.reactions>>
    const old = deferred<Rows>(), current = deferred<Rows>()
    vi.spyOn(postApi, 'reactions').mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = mountReaction()
    await wrapper.find('.post-reactions__details').trigger('click')
    wrapper.findComponent({ name: 'TDialog' }).vm.$emit('update:visible', false)
    await flushPromises()
    await wrapper.find('.post-reactions__details').trigger('click')
    if (outcome === 'success') old.resolve([{ name: '👍', count: 1, me: false, accounts: [{ id: 'old', acct: 'old' } as MastodonAccount] }])
    else old.reject(new Error('old failure'))
    await flushPromises()
    expect(wrapper.text()).toContain('正在加载回应者')
    expect(wrapper.find('[role=alert]').exists()).toBe(false)
    expect(wrapper.find('.responders__account').exists()).toBe(false)
    current.resolve([{ name: '🎉', count: 1, me: false, accounts: [{ id: 'current', acct: 'current' } as MastodonAccount] }])
    await flushPromises()
    expect(wrapper.find('.responders__handle').text()).toBe('@current')
    expect(wrapper.text()).not.toContain('正在加载回应者')
    wrapper.unmount()
  })

  it.each(['success', 'failure'] as const)('does not overwrite the newer completed responder request with old %s', async outcome => {
    type Rows = Awaited<ReturnType<typeof postApi.reactions>>
    const old = deferred<Rows>(), current = deferred<Rows>()
    vi.spyOn(postApi, 'reactions').mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = mountReaction()
    await wrapper.find('.post-reactions__details').trigger('click')
    wrapper.findComponent({ name: 'TDialog' }).vm.$emit('update:visible', false); await flushPromises()
    await wrapper.find('.post-reactions__details').trigger('click')
    current.resolve([{ name: '🎉', count: 1, me: false, accounts: [{ id: 'current', acct: 'current' } as MastodonAccount] }]); await flushPromises()
    if (outcome === 'success') old.resolve([{ name: '👍', count: 1, me: false, accounts: [{ id: 'old', acct: 'old' } as MastodonAccount] }])
    else old.reject(new Error('old failure'))
    await flushPromises()
    expect(wrapper.find('.responders__handle').text()).toBe('@current')
    expect(wrapper.find('[role=alert]').exists()).toBe(false)
    wrapper.unmount()
  })
})

it.each(['account', 'post'] as const)('resets responder loading on %s switch and ignores the old error/finally', async scope => {
  type Rows = Awaited<ReturnType<typeof postApi.reactions>>
  const old = deferred<Rows>(), current = deferred<Rows>()
  vi.spyOn(postApi, 'reactions').mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
  const wrapper = mountReaction()
  await wrapper.find('.post-reactions__details').trigger('click')
  if (scope === 'account') sessionEpoch.value++
  else await wrapper.setProps({ status: { ...status, id: 'new-details-post' } })
  await flushPromises()
  expect(wrapper.find('t-dialog-stub').exists()).toBe(false)
  await wrapper.find('.post-reactions__details').trigger('click')
  old.reject(new Error('old permission failure')); await flushPromises()
  expect(wrapper.text()).toContain('正在加载回应者')
  expect(wrapper.find('[role=alert]').exists()).toBe(false)
  current.resolve([]); await flushPromises()
  expect(wrapper.text()).toContain('还没有回应')
  wrapper.unmount()
})
