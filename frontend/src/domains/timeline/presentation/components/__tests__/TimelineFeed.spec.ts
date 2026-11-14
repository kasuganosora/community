import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import TimelineFeed from '../TimelineFeed.vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })
describe('visible sentinel pagination', () => {
  it('retries a failed refresh even when the retained timeline has no next page', async () => {
    const wrapper = mount(TimelineFeed, {
      props: { statuses: [{ id: '1' }] as MastodonStatus[], error: 'offline', hasMore: false },
      global: { stubs: { PostCard: true, 't-loading': true } },
    })
    await wrapper.get('.timeline-inline button').trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
    expect(wrapper.emitted('loadMore')).toBeUndefined()
    await wrapper.setProps({ loading: true })
    expect(wrapper.get('.timeline-inline button').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
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

function dispatchTouch(el: Element, type: string, clientX: number, clientY: number) {
  const evt = new Event(type, { bubbles: true, cancelable: true })
  const touches = type === 'touchend' || type === 'touchcancel' ? [] : [{ clientX, clientY }]
  Object.defineProperty(evt, 'touches', { value: touches })
  el.dispatchEvent(evt)
}

describe('mobile touch pull-to-refresh', () => {
  it('keeps zero resting chrome, arms past threshold, invokes pullRefresh, and yields to horizontal swipes', async () => {
    window.matchMedia = vi.fn().mockReturnValue({ matches: true }) as unknown as typeof window.matchMedia
    vi.spyOn(window, 'scrollY', 'get').mockReturnValue(0)
    let resolveRefresh!: () => void
    const pullRefresh = vi.fn(() => new Promise<void>(r => { resolveRefresh = r }))

    const wrapper = mount(TimelineFeed, {
      props: {
        statuses: [{ id: '1' }] as MastodonStatus[],
        hasMore: false,
        pullRefresh,
      },
      global: { stubs: { PostCard: true, 't-loading': true, 't-icon': true } },
    })

    // Resting state has zero PTR DOM or inline transform.
    expect(wrapper.find('[data-testid="timeline-ptr"]').exists()).toBe(false)
    expect(wrapper.get('.timeline-window').attributes('style')).toBeUndefined()

    const root = wrapper.get('.timeline-feed').element

    // Horizontal swipe yields immediately so PostCard swipe gestures work cleanly.
    dispatchTouch(root, 'touchstart', 100, 100)
    dispatchTouch(root, 'touchmove', 160, 105)
    dispatchTouch(root, 'touchend', 160, 105)
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-testid="timeline-ptr"]').exists()).toBe(false)
    expect(pullRefresh).not.toHaveBeenCalled()

    // Vertical pull down past threshold arms and triggers pullRefresh on release.
    dispatchTouch(root, 'touchstart', 120, 80)
    dispatchTouch(root, 'touchmove', 120, 96) // enters pulling
    dispatchTouch(root, 'touchmove', 120, 230) // raw > 110 -> offset >= 48 (armed)
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-testid="timeline-ptr"]').exists()).toBe(true)
    expect(wrapper.find('.timeline-ptr__arrow--armed').exists()).toBe(true)

    dispatchTouch(root, 'touchend', 120, 230)
    await wrapper.vm.$nextTick()
    expect(pullRefresh).toHaveBeenCalledTimes(1)

    resolveRefresh()
    await Promise.resolve()
    await Promise.resolve()
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-testid="timeline-ptr"]').exists()).toBe(false)
    wrapper.unmount()
  })
})

