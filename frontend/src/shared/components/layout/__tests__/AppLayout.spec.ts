import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'
import AppLayout from '../AppLayout.vue'
vi.mock('@/shared/composables/useUiChrome', () => ({ useUiChrome: () => ({ openCompose: vi.fn() }) }))
vi.mock('@/shared/composables/useScrollEdge', () => ({ useScrollEdge: vi.fn() }))
async function setup() {
  const page = { template: '<div />' }
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/', name: 'home', component: page },
    { path: '/messages', name: 'messages', component: page },
    { path: '/messages/:conversationId', name: 'chat-conversation', component: page },
    { path: '/groups', name: 'groups', component: page },
    { path: '/groups/:groupId', name: 'group', component: page },
    { path: '/settings', name: 'settings', component: page },
    { path: '/search', name: 'search', component: page },
    { path: '/@:username/:statusId', name: 'post-detail', component: page },
  ] })
  await router.push('/'); await router.isReady()
  const wrapper = mount(AppLayout, { global: { plugins: [router], stubs: { LeftSidebar: true, RightSidebar: true, MobileTabBar: true, ComposeSheet: true, AccountSheet: true, AccountsManagerDialog: true, NotificationWatcher: true, RouterView: true, TIcon: true } } })
  return { wrapper, router }
}
describe('message composer mobile controls', () => {
  it('keeps the global post button out of private and group chat controls and keyboard navigation', async () => {
    const { wrapper, router } = await setup()
    expect(wrapper.get('.app-fab').attributes('aria-hidden')).toBeUndefined()
    for (const path of ['/messages', '/messages/conversation', '/groups', '/groups/room', '/@alice/123']) {
      await router.push(path); await nextTick()
      expect(wrapper.get('.app-fab').classes()).toContain('app-fab--hidden')
      expect(wrapper.get('.app-fab').attributes('tabindex')).toBe('-1')
      expect(wrapper.get('.app-fab').attributes('aria-hidden')).toBe('true')
    }
    await router.push('/'); await nextTick()
    expect(wrapper.get('.app-fab').classes()).not.toContain('app-fab--hidden')
    expect(wrapper.get('.app-fab').attributes('tabindex')).toBeUndefined()
    wrapper.unmount()
  })
  it('hides the post button for group search while retaining it for post search', async () => {
    const { wrapper, router } = await setup()
    await router.push('/search?type=groups&q=design'); await nextTick()
    expect(wrapper.get('.app-fab').attributes('aria-hidden')).toBe('true')
    await router.push('/search?type=statuses&q=design'); await nextTick()
    expect(wrapper.get('.app-fab').attributes('aria-hidden')).toBeUndefined()
    expect(wrapper.get('.app-fab').attributes('tabindex')).toBeUndefined()
    wrapper.unmount()
  })
  it('keeps the same column layout on groups, settings and the home feed', async () => {
    const { wrapper, router } = await setup()
    expect(wrapper.find('.app-layout__right').exists()).toBe(true)
    for (const path of ['/groups', '/groups/room', '/settings']) {
      await router.push(path); await nextTick()
      expect(wrapper.find('.app-layout__right').exists()).toBe(true)
    }
    await router.push('/'); await nextTick()
    expect(wrapper.find('.app-layout__right').exists()).toBe(true)
    wrapper.unmount()
  })
})
