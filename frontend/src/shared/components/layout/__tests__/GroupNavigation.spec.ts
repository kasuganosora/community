import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'
import LeftSidebar from '../LeftSidebar.vue'
import MobileTabBar from '../MobileTabBar.vue'
import AccountSheet from '@/shared/components/sheets/AccountSheet.vue'

vi.mock('@/domains/auth/presentation/stores/authStore', () => ({
  useAuthStore: () => ({ currentUser: { id: '1', username: 'alice', display_name: 'Alice' }, logout: vi.fn() }),
}))
vi.mock('@/shared/composables/useUiChrome', async () => {
  const { ref } = await import('vue')
  return { useUiChrome: () => ({ openCompose: vi.fn(), openAccount: vi.fn(), accountOpen: ref(true) }) }
})
vi.mock('@/domains/admin/domain/navigation', () => ({ adminEntry: () => null }))
vi.mock('@/shared/ui/feedback', () => ({ MessagePlugin: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() }, DialogPlugin: { confirm: vi.fn(), alert: vi.fn() } }))
vi.mock('tdesign-vue-next', () => ({
  MessagePlugin: { error: vi.fn() },
  Dropdown: { template: '<div />' },
  Popup: { template: '<div />' },
  DropdownMenu: { template: '<div />' },
  DropdownItem: { template: '<div />' },
}))

async function createTestRouter() {
  const page = { template: '<div />' }
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/', component: page },
    { path: '/groups', component: page },
    { path: '/groups/:groupId', component: page },
    { path: '/messages', component: page },
  ] })
  await router.push('/')
  await router.isReady()
  return router
}

const stubs = { TIcon: true, TDropdown: true, TDropdownMenu: true, TDropdownItem: true, TPopup: true, UserAvatar: true, LogoIcon: true, EmojiText: true }

describe('independent group navigation', () => {
  it('opens the groups module from desktop navigation and highlights it without highlighting messages', async () => {
    const router = await createTestRouter()
    const wrapper = mount(LeftSidebar, { global: { plugins: [router], stubs } })
    await wrapper.get('button[aria-label="群组"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/groups')
    expect(wrapper.get('button[aria-label="群组"]').attributes('aria-current')).toBe('page')
    expect(wrapper.get('button[aria-label="消息"]').attributes('aria-current')).toBeUndefined()
    await router.push('/groups/room')
    expect(wrapper.get('button[aria-label="群组"]').attributes('aria-current')).toBe('page')
    wrapper.unmount()
  })

  it('keeps groups available in the mobile account menu without presenting them as messages', async () => {
    const router = await createTestRouter()
    const menu = mount(AccountSheet, { global: { plugins: [router], stubs: { ...stubs, BottomSheet: { template: '<div><slot /></div>' } } } })
    const groupEntry = menu.findAll('button').find(button => button.text() === '群组')!
    await groupEntry.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/groups')
    const tabbar = mount(MobileTabBar, { global: { plugins: [router], stubs } })
    const messages = tabbar.findAll('button').find(button => button.text() === '消息')!
    expect(messages.attributes('aria-current')).toBeUndefined()
    await router.push('/messages')
    expect(messages.attributes('aria-current')).toBe('page')
    menu.unmount()
    tabbar.unmount()
  })
})
