import { afterEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import NotificationsPage from '../NotificationsPage.vue'
import { notificationApi } from '../../../infrastructure/notificationApi'
import type { MastodonAccount, MastodonNotification, MastodonStatus } from '@/shared/types/mastodon'

const actor = {
  id: 'actor',
  username: 'genius',
  acct: 'genius@example.test',
  display_name: 'Genius',
  emojis: [],
} as unknown as MastodonAccount
const author = {
  id: 'author',
  username: 'alice',
  acct: 'alice',
  display_name: 'Alice',
  emojis: [],
} as unknown as MastodonAccount
const status = {
  id: '9',
  account: author,
  content: '<p>the post</p>',
  spoiler_text: '',
  visibility: 'public',
  created_at: '2026-10-14T00:00:00Z',
  emojis: [],
  media_attachments: [],
  mentions: [],
  replies_count: 0,
  reblogs_count: 0,
  favourites_count: 0,
} as unknown as MastodonStatus
const PROFILE = '/@genius@example.test'
const STATUS = '/@alice/status/9'

function notification(type: string, withStatus = true): MastodonNotification {
  return {
    id: `${type}-1`,
    type,
    created_at: '2026-10-14T00:00:00Z',
    account: actor,
    ...(withStatus ? { status } : {}),
  } as MastodonNotification
}

const stubs = {
  TabBar: { props: ['modelValue', 'options'], template: '<div class="tab-bar" />' },
  RelativeTime: { template: '<time class="notification-time" />' },
  ReactionLabel: true,
  UserAvatar: { template: '<span class="actor-avatar" />' },
  EmojiText: { props: ['text'], template: '<span class="emoji-text">{{ text }}</span>' },
  PostContent: true,
  PostActions: true,
  PostReactions: true,
  PostComposer: true,
  ReportDialog: true,
  't-icon': true,
  't-loading': true,
  't-dropdown': true,
  't-dialog': true,
}

async function mountPage(items: MastodonNotification[]) {
  vi.stubGlobal('IntersectionObserver', class { observe() {} disconnect() {} unobserve() {} })
  vi.stubGlobal('WebSocket', class { close() {} })
  setActivePinia(createPinia())
  vi.spyOn(notificationApi, 'list').mockResolvedValue(items)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { name: 'profile', path: '/@:username', component: { template: '<div />' } },
      { name: 'post-detail', path: '/@:username/status/:statusId', component: { template: '<div />' } },
    ],
  })
  await router.push('/')
  await router.isReady()
  const wrapper = mount(NotificationsPage, { global: { plugins: [router], stubs } })
  await flushPromises()
  return { router, wrapper }
}

function linkHref(wrapper: VueWrapper, selector: string) {
  return wrapper.get(selector).element.closest('a')?.getAttribute('href') ?? ''
}

async function go(router: Router, path: string) {
  await router.push(path)
  await flushPromises()
}

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('notification actor links', () => {
  it('opens the actor profile from the avatar and display name, and the status from the rest of a like', async () => {
    const { router, wrapper } = await mountPage([notification('favourite')])

    expect(linkHref(wrapper, '.actor-avatar')).toBe(PROFILE)
    expect(linkHref(wrapper, '.notification-text .emoji-text')).toBe(PROFILE)
    expect(wrapper.get('.notification-preview').attributes('href')).toBe(STATUS)
    expect(linkHref(wrapper, '.notification-preview .emoji-text')).toBe(STATUS)

    await wrapper.get('.actor-avatar').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe(PROFILE)

    await go(router, '/')
    await wrapper.get('.notification-text .emoji-text').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe(PROFILE)

    await go(router, '/')
    await wrapper.get('.notification-preview').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe(STATUS)

    await go(router, '/')
    await wrapper.get('.notification-icon').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe(STATUS)
    wrapper.unmount()
  })

  it.each(['reblog', 'emoji_reaction', 'poll', 'update', 'status'] as const)(
    'keeps the %s avatar and name on the actor profile while the row still opens the status',
    async (type) => {
      const { router, wrapper } = await mountPage([notification(type)])
      expect(linkHref(wrapper, '.actor-avatar')).toBe(PROFILE)
      expect(linkHref(wrapper, '.notification-text .emoji-text')).toBe(PROFILE)
      expect(wrapper.get('.notification-preview').attributes('href')).toBe(STATUS)
      await wrapper.get('.notification-icon').trigger('click')
      await flushPromises()
      expect(router.currentRoute.value.path).toBe(STATUS)
      wrapper.unmount()
    },
  )

  it.each(['follow', 'follow_request'] as const)(
    'opens the %s actor profile from the avatar, the name, and the row',
    async (type) => {
      const { router, wrapper } = await mountPage([notification(type, false)])
      expect(linkHref(wrapper, '.actor-avatar')).toBe(PROFILE)
      expect(linkHref(wrapper, '.notification-text .emoji-text')).toBe(PROFILE)
      await wrapper.get('.notification-icon').trigger('click')
      await flushPromises()
      expect(router.currentRoute.value.path).toBe(PROFILE)
      wrapper.unmount()
    },
  )

  it('shows the post a mention replies to, while the actor avatar and name still open that person', async () => {
    const mention = notification('mention')
    const parentAuthor = { id: 'parent-author', username: 'luna', acct: 'luna', display_name: 'Luna', emojis: [] }
    mention.status = {
      ...status,
      account: actor,
      content: '<p>确实，我也去禁用</p>',
      in_reply_to_id: 'parent',
      reply: {
        id: 'parent',
        account: parentAuthor,
        content: '<p>原来的帖子</p>',
        spoiler_text: '',
        sensitive: false,
        emojis: [],
        created_at: '2026-10-14T00:00:00Z',
      },
    }
    const { wrapper } = await mountPage([mention])
    expect(wrapper.text()).toContain('原来的帖子')
    expect(wrapper.get('.reply-context__text').attributes('href')).toBe('/@luna/status/parent')
    expect(linkHref(wrapper, '.reply-context__avatar')).toBe('/@luna')
    expect(linkHref(wrapper, '.reply-context__name')).toBe('/@luna')
    expect(linkHref(wrapper, '.post-card__avatar')).toBe(PROFILE)
    expect(linkHref(wrapper, '.post-card__name')).toBe(PROFILE)
    expect(linkHref(wrapper, '.post-card__avatar')).not.toContain('/status/')
    wrapper.unmount()
  })
})
