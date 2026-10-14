import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import TimelineFeed from '../TimelineFeed.vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })
describe('visible sentinel pagination', () => {
  it('continues after loading completes without requiring scroll and pauses on errors', async () => {
    let intersect!: () => void
    vi.stubGlobal('IntersectionObserver', class { constructor(callback: () => void) { intersect = callback } observe() {} disconnect() {} })
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ top: 100, bottom: 101 } as DOMRect)
    const wrapper = mount(TimelineFeed, { props: { statuses: [{ id: '1' }] as MastodonStatus[], loading: true, hasMore: true }, global: { stubs: { PostCard: true, 't-loading': true } } })
    intersect()
    expect(wrapper.emitted('loadMore')).toBeUndefined()
    await wrapper.setProps({ loading: false })
    expect(wrapper.emitted('loadMore')).toHaveLength(1)
    await wrapper.setProps({ loading: true }); await wrapper.setProps({ loading: false, error: 'offline' })
    expect(wrapper.emitted('loadMore')).toHaveLength(1)
    await wrapper.setProps({ error: null, hasMore: false })
    intersect()
    expect(wrapper.emitted('loadMore')).toHaveLength(1)
    wrapper.unmount()
  })
  it('pauses automatic discovery paging without disabling the explicit continue button', async () => {
    let intersect!: () => void
    vi.stubGlobal('IntersectionObserver', class { constructor(callback: () => void) { intersect = callback } observe() {} disconnect() {} })
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ top: 100, bottom: 101 } as DOMRect)
    const wrapper = mount(TimelineFeed, { props: { statuses: [{ id: '1' }] as MastodonStatus[], loading: false, hasMore: true, autoLoad: false }, global: { stubs: { PostCard: true } } })
    intersect()
    expect(wrapper.emitted('loadMore')).toBeUndefined()
    await wrapper.get('.timeline-inline button').trigger('click')
    expect(wrapper.emitted('loadMore')).toHaveLength(1)
    wrapper.unmount()
  })
})
