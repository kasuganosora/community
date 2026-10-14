import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import TimelineFeed from '../TimelineFeed.vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { sessionEpoch } from '@/shared/api/sessionScope'
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

async function setup(y: number) {
  sessionEpoch.value++
  vi.stubGlobal('scrollY', y)
  const scroll = vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function(this: HTMLElement) {
    return { top: this.classList.contains('timeline-window') ? 300 - window.scrollY : 0, bottom: 0, height: 120 } as DOMRect
  })
  let resize!: ResizeObserverCallback
  const observed: Element[] = []
  vi.stubGlobal('ResizeObserver', class {
    constructor(callback: ResizeObserverCallback) { resize = callback }
    observe(element: Element) { observed.push(element) }
    unobserve() {} disconnect() {}
  })
  const wrapper = mount(TimelineFeed, {
    props: { statuses: ['1', '2', '3'].map(id => ({ id })) as MastodonStatus[], hasMore: false },
    global: { stubs: { PostCard: true } },
  })
  await flushPromises()
  const first = observed.find(element => (element as HTMLElement).dataset.timelineId === '1')!
  resize([{ target: first, borderBoxSize: [{ blockSize: 120 }] } as unknown as ResizeObserverEntry], {} as ResizeObserver)
  await flushPromises()
  return { wrapper, scroll }
}

it('does not jump past the header when initial rows are measured before the feed reaches the viewport top', async () => {
  const { wrapper, scroll } = await setup(0)
  expect(scroll).not.toHaveBeenCalled()
  wrapper.unmount()
})

it('still compensates for resized rows above the reading position', async () => {
  const { wrapper, scroll } = await setup(750)
  expect(scroll).toHaveBeenCalledWith({ top: 630, behavior: 'instant' })
  wrapper.unmount()
})
