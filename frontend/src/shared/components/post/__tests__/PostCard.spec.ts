import { describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { MessagePlugin } from '@/shared/ui/feedback'
import PostCard from '../PostCard.vue'
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
