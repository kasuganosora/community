import { describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { MessagePlugin } from '@/shared/ui/feedback'
import PostCard from '../PostCard.vue'
import ReplyContext from '../ReplyContext.vue'
import PostComposer from '../PostComposer.vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { deletedStatusIds } from '@/domains/post/presentation/composables/useStatusActions'
import type { MastodonAccount, MastodonStatus } from '@/shared/types/mastodon'

describe('PostCard replacement', () => {
  it('hides every original card and opens the new post only after replacement succeeds', async () => {
    deletedStatusIds.clear()
    vi.spyOn(MessagePlugin, 'success').mockImplementation(() => Promise.resolve({ close: vi.fn() }))
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/@:username/:statusId', name: 'post-detail', component: { template: '<div />' } },
    ] })
    await router.push('/')
    const pinia = createPinia()
    const account = { id: 'owner', username: 'alice', acct: 'alice' } as MastodonAccount
    useAuthStore(pinia).currentUser = account
    const status = { id: 'old-card', account, visibility: 'public', content: '<p>Original</p>', media_attachments: [], created_at: new Date().toISOString() } as unknown as MastodonStatus
    const options = { props: { status }, global: { plugins: [pinia, router], renderStubDefaultSlot: true } }
    const first = shallowMount(PostCard, options)
    const elsewhere = shallowMount(PostCard, options)
    first.findComponent({ name: 'TDropdown' }).vm.$emit('click', { value: 'edit' })
    await flushPromises()
    expect(deletedStatusIds.has(status.id)).toBe(false)
    expect(first.findComponent(PostComposer).exists()).toBe(true)
    first.findComponent(PostComposer).vm.$emit('posted', { ...status, id: 'new-card' })
    await flushPromises()
    expect(deletedStatusIds.has(status.id)).toBe(true)
    expect(first.find('article').exists()).toBe(false)
    expect(elsewhere.find('article').exists()).toBe(false)
    expect(router.currentRoute.value.params.statusId).toBe('new-card')
    first.unmount()
    elsewhere.unmount()
    deletedStatusIds.clear()
  })
})


describe('PostCard reply context', () => {
  it('shows the parent by default and can omit duplicate context in a thread', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }] })
    await router.push('/')
    const account = { id: 'author', username: 'alice', acct: 'alice' } as MastodonAccount
    const status = { id: 'child', account, in_reply_to_id: 'parent', visibility: 'public', content: 'reply', created_at: new Date().toISOString(), reply: { id: 'parent', account, content: 'original' } } as unknown as MastodonStatus
    const wrapper = shallowMount(PostCard, { props: { status }, global: { plugins: [createPinia(), router] } })
    expect(wrapper.findComponent(ReplyContext).exists()).toBe(true)
    await wrapper.setProps({ showReplyContext: false })
    expect(wrapper.findComponent(ReplyContext).exists()).toBe(false)
    wrapper.unmount()
  })
})

it('shows when federation is paused and removes the label after release', async () => {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }] })
  await router.push('/')
  const account = { id: 'author', username: 'alice', acct: 'alice' } as MastodonAccount
  const status = { id: 'held', account, local_only: true, visibility: 'public', content: 'hello', media_attachments: [], created_at: new Date().toISOString() } as unknown as MastodonStatus
  const wrapper = shallowMount(PostCard, { props: { status }, global: { plugins: [createPinia(), router] } })
  expect(wrapper.text()).toContain('仅本站')
  await wrapper.setProps({ status: { ...status, local_only: false } })
  expect(wrapper.text()).not.toContain('仅本站')
  wrapper.unmount()
})

function sendTouch(el: Element, type: string, clientX: number, clientY: number) {
  const evt = new Event(type, { bubbles: true, cancelable: true })
  const touches = type === 'touchend' || type === 'touchcancel' ? [] : [{ clientX, clientY }]
  Object.defineProperty(evt, 'touches', { value: touches })
  el.dispatchEvent(evt)
}

describe('Tweetbot swipe gestures and inline quick reply', () => {
  it('renders zero swipe DOM/classes at rest and handles short-right like, long-right reply, left detail, and vertical scroll', async () => {
    window.matchMedia = vi.fn().mockReturnValue({ matches: true }) as unknown as typeof window.matchMedia
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: { template: '<div />' } },
        { path: '/@:username/:statusId', name: 'post-detail', component: { template: '<div />' } },
      ],
    })
    await router.push('/')
    const pinia = createPinia()
    const viewer = { id: 'viewer', username: 'bob', acct: 'bob' } as MastodonAccount
    useAuthStore(pinia).currentUser = viewer

    const author = { id: 'author', username: 'alice', acct: 'alice' } as MastodonAccount
    const status = {
      id: 'post-99',
      account: author,
      visibility: 'public',
      content: '<p>Hello Tweetbot</p>',
      media_attachments: [],
      favourited: false,
      replies_count: 2,
      created_at: new Date().toISOString(),
    } as unknown as MastodonStatus

    const { useUiChrome } = await import('@/shared/composables/useUiChrome')
    const chrome = useUiChrome()
    chrome.composeOpen.value = false
    chrome.replyTarget.value = null

    const wrapper = shallowMount(PostCard, {
      props: { status },
      global: { plugins: [pinia, router], renderStubDefaultSlot: true },
    })

    const article = wrapper.get('article')
    const surface = wrapper.get('.post-card__surface')

    // Resting state: zero visual mutation.
    expect(article.classes()).not.toContain('post-card--swiping')
    expect(wrapper.find('.post-card__swipe-bg').exists()).toBe(false)
    expect(surface.attributes('style')).toBeUndefined()

    // Vertical drag stays idle and does not trigger swipe chrome.
    sendTouch(article.element, 'touchstart', 150, 100)
    sendTouch(article.element, 'touchmove', 154, 150)
    sendTouch(article.element, 'touchend', 154, 150)
    await flushPromises()
    expect(wrapper.find('.post-card__swipe-bg').exists()).toBe(false)
    expect(wrapper.emitted('favourite')).toBeUndefined()

    // Short right swipe -> Favourite (Like), then snaps cleanly back to rest.
    sendTouch(article.element, 'touchstart', 80, 100)
    sendTouch(article.element, 'touchmove', 96, 100) // enters swiping (origin = 96)
    sendTouch(article.element, 'touchmove', 160, 100) // rawDx = 64 -> offset = 54.4 (like)
    await flushPromises()
    expect(article.classes()).toContain('post-card--swiping')
    expect(wrapper.find('.post-card__swipe-icon--like').exists()).toBe(true)

    sendTouch(article.element, 'touchend', 160, 100)
    await flushPromises()
    expect(wrapper.emitted('favourite')).toEqual([[status]])
    expect(article.classes()).not.toContain('post-card--swiping')
    expect(wrapper.find('.post-card__swipe-bg').exists()).toBe(false)

    // Long right swipe -> Quick Reply in ComposeSheet without route navigation.
    sendTouch(article.element, 'touchstart', 40, 100)
    sendTouch(article.element, 'touchmove', 56, 100) // enters swiping (origin = 56)
    sendTouch(article.element, 'touchmove', 260, 100) // rawDx = 204 -> offset >= 92 (reply)
    await flushPromises()
    expect(wrapper.find('.post-card__swipe-icon--reply').exists()).toBe(true)

    sendTouch(article.element, 'touchend', 260, 100)
    await flushPromises()
    expect(useUiChrome().composeOpen.value).toBe(true)
    expect(useUiChrome().replyTarget.value?.id).toBe('post-99')
    expect(router.currentRoute.value.path).toBe('/')

    chrome.composeOpen.value = false
    chrome.replyTarget.value = null

    // Swipe left -> Navigate to Post Detail / Conversation.
    sendTouch(article.element, 'touchstart', 240, 100)
    sendTouch(article.element, 'touchmove', 224, 100) // enters swiping (origin = 224)
    sendTouch(article.element, 'touchmove', 140, 100) // rawDx = -84 -> offset < -52 (detail)
    await flushPromises()
    expect(wrapper.find('.post-card__swipe-icon--detail').exists()).toBe(true)

    sendTouch(article.element, 'touchend', 140, 100)
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('post-detail')
    expect(router.currentRoute.value.params.statusId).toBe('post-99')

    wrapper.unmount()
  })

  it('opens inline quick reply via Reply button and updates reply count without scrolling on ComposeSheet submit', async () => {
    vi.spyOn(MessagePlugin, 'success').mockImplementation(() => Promise.resolve({ close: vi.fn() }))
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: { template: '<div />' } },
        { path: '/@:username/:statusId', name: 'post-detail', component: { template: '<div />' } },
      ],
    })
    await router.push('/')
    const pinia = createPinia()
    useAuthStore(pinia).currentUser = { id: 'viewer', username: 'bob', acct: 'bob' } as MastodonAccount

    const author = { id: 'author', username: 'alice', acct: 'alice' } as MastodonAccount
    const status = {
      id: 'target-post',
      account: author,
      visibility: 'public',
      content: '<p>Tap reply</p>',
      media_attachments: [],
      replies_count: 4,
      created_at: new Date().toISOString(),
    } as unknown as MastodonStatus

    const { useUiChrome } = await import('@/shared/composables/useUiChrome')
    const { useTimelineStore } = await import('@/domains/timeline/presentation/stores/timelineStore')
    const ComposeSheet = (await import('@/shared/components/sheets/ComposeSheet.vue')).default
    const PostActions = (await import('../PostActions.vue')).default

    const chrome = useUiChrome()
    chrome.composeOpen.value = false
    chrome.replyTarget.value = null

    const card = shallowMount(PostCard, {
      props: { status },
      global: { plugins: [pinia, router], renderStubDefaultSlot: true },
    })
    const sheet = shallowMount(ComposeSheet, {
      global: { plugins: [pinia, router], renderStubDefaultSlot: true },
    })

    // Emitting reply from PostActions opens ComposeSheet in-place without leaving '/'
    card.findComponent(PostActions).vm.$emit('reply')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/')
    expect(useUiChrome().composeOpen.value).toBe(true)
    expect(useUiChrome().replyTarget.value?.id).toBe('target-post')
    expect(sheet.findComponent(ReplyContext).exists()).toBe(true)
    expect(sheet.findComponent(PostComposer).props('replyTo')).toMatchObject({ id: 'target-post' })

    const timelineStore = useTimelineStore(pinia)
    expect(timelineStore.pendingReveal).toBeNull()
    sheet.findComponent(PostComposer).vm.$emit('posted', {
      id: 'new-reply',
      in_reply_to_id: 'target-post',
      visibility: 'public',
      account: useAuthStore(pinia).currentUser,
    } as MastodonStatus)
    await flushPromises()

    expect(status.replies_count).toBe(5)
    expect(useUiChrome().composeOpen.value).toBe(false)
    expect(useUiChrome().replyTarget.value).toBeNull()
    expect(timelineStore.pendingReveal).toBeNull()

    card.unmount()
    sheet.unmount()
  })
})

