import { mount } from '@vue/test-utils'
import { nextTick, reactive } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import AppLayout from '../AppLayout.vue'
const route = reactive({ name: 'home', fullPath: '/' })
vi.mock('vue-router', () => ({ useRoute: () => route }))
vi.mock('@/shared/composables/useUiChrome', () => ({ useUiChrome: () => ({ openCompose: vi.fn() }) }))
vi.mock('@/shared/composables/useScrollEdge', () => ({ useScrollEdge: vi.fn() }))
describe('message composer mobile controls', () => {
  it('keeps the global floating post button out of chat send controls and keyboard navigation', async () => {
    const w = mount(AppLayout, { global: { stubs: { LeftSidebar: true, RightSidebar: true, MobileTabBar: true, ComposeSheet: true, AccountSheet: true, RouterView: true, TIcon: true } } })
    expect(w.get('.app-fab').attributes('aria-hidden')).toBeUndefined()
    for (const name of ['messages', 'chat-conversation']) {
      route.name = name; await nextTick()
      expect(w.get('.app-fab').classes()).toContain('app-fab--hidden')
      expect(w.get('.app-fab').attributes('tabindex')).toBe('-1')
      expect(w.get('.app-fab').attributes('aria-hidden')).toBe('true')
    }
    route.name = 'home'; await nextTick()
    expect(w.get('.app-fab').classes()).not.toContain('app-fab--hidden')
    w.unmount()
  })
})
