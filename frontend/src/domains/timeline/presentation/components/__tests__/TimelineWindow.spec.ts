import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick, ref, type PropType } from 'vue'
import { mount, type VueWrapper } from '@vue/test-utils'
import TimelineFeed from '../TimelineFeed.vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { useTimelineRowFlag, useTimelineRowPin } from '@/shared/composables/useTimelineRow'
import { invalidateSessionData } from '@/shared/api/sessionScope'

const Card = defineComponent({
  props: { status: { type: Object as PropType<MastodonStatus>, required: true } },
  setup() {
    const editing = ref(false)
    const expanded = useTimelineRowFlag('testCW')
    useTimelineRowPin(() => editing.value, () => { editing.value = false })
    return { editing, expanded }
  },
  template: '<article :data-card="status.id"><button class="edit" @click="editing = !editing">编辑</button><input v-if="editing" aria-label="草稿" /><button class="cw" @click="expanded = !expanded">{{ expanded ? "收起" : "展开" }}</button></article>',
})
let scroll = 0
let resize: ResizeObserverCallback
let frames: FrameRequestCallback[]
let wrappers: VueWrapper[]
function statuses(count: number, start = 0) {
  return Array.from({ length: count }, (_, index) => ({ id: String(start + index), reblog: { id: 'shared-original' } })) as MastodonStatus[]
}
function feed(count = 2000) {
  const wrapper = mount(TimelineFeed, {
    props: { statuses: statuses(count), scopeKey: 'window-test', hasMore: false },
    attachTo: document.body,
    global: { stubs: { PostCard: Card, 't-loading': true, 't-icon': true } },
  })
  wrappers.push(wrapper)
  return wrapper
}
async function flush() {
  await nextTick()
  const pending = frames.splice(0)
  for (const frame of pending) frame(0)
  await nextTick()
}
async function scrollTo(top: number) {
  scroll = top
  window.dispatchEvent(new Event('scroll'))
  await flush()
}
beforeEach(() => {
  scroll = 0; frames = []; wrappers = []
  invalidateSessionData()
  vi.spyOn(window, 'scrollY', 'get').mockImplementation(() => scroll)
  vi.spyOn(window, 'scrollTo').mockImplementation((options: ScrollToOptions | number) => {
    if (typeof options === 'object') scroll = options.top ?? scroll
  })
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { frames.push(callback); return frames.length })
  vi.stubGlobal('cancelAnimationFrame', vi.fn())
  vi.stubGlobal('IntersectionObserver', class { observe() {} disconnect() {} })
  vi.stubGlobal('ResizeObserver', class {
    constructor(callback: ResizeObserverCallback) { resize = callback }
    observe() {} unobserve() {} disconnect() {}
  })
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    return { top: -scroll, bottom: 1 - scroll, height: 240, width: 600 } as DOMRect
  })
})
afterEach(() => {
  for (const wrapper of wrappers) wrapper.unmount()
  document.body.innerHTML = ''
  vi.restoreAllMocks(); vi.unstubAllGlobals()
})

describe('variable height timeline window', () => {
  it.each([2000, 5000])('bounds mounted cards while traversing %i wrapper IDs', async (count) => {
    const wrapper = feed(count)
    await flush()
    for (const index of [0, 150, 600, count - 10]) {
      await scrollTo(index * 240)
      const cards = wrapper.findAll('[data-card]')
      expect(cards.length).toBeLessThanOrEqual(60)
      expect(wrapper.find(`[data-card="${index}"]`).exists()).toBe(true)
      expect(new Set(cards.map(card => card.attributes('data-card'))).size).toBe(cards.length)
    }
  })
  it('retains an editing card and its draft outside the viewport, then releases it on close', async () => {
    const wrapper = feed()
    await flush()
    await wrapper.get('[data-card="0"] .edit').trigger('click')
    await wrapper.get('[data-card="0"] input').setValue('未提交的回复')
    await scrollTo(100 * 240)
    expect((wrapper.get('[data-card="0"] input').element as HTMLInputElement).value).toBe('未提交的回复')
    expect(wrapper.findAll('[data-card]').length).toBeLessThanOrEqual(68)
    await wrapper.get('[data-card="0"] .edit').trigger('click')
    expect(wrapper.find('[data-card="0"]').exists()).toBe(false)
  })
  it('limits simultaneous interaction pins without discarding an earlier draft', async () => {
    const wrapper = feed()
    await flush()
    for (let index = 0; index < 9; index++) {
      await scrollTo(index * 240)
      await wrapper.get(`[data-card="${index}"] .edit`).trigger('click')
    }
    expect(wrapper.findAll('input')).toHaveLength(8)
    expect(wrapper.find('[data-card="0"] input').exists()).toBe(true)
    expect(wrapper.find('[data-card="8"] input').exists()).toBe(false)
  })
  it('compensates media height changes above the visible anchor', async () => {
    const wrapper = feed()
    await flush()
    await scrollTo(50 * 240 + 30)
    const target = wrapper.get('[data-timeline-id="48"]').element
    resize([{ target, borderBoxSize: [{ blockSize: 540 }] } as unknown as ResizeObserverEntry], {} as ResizeObserver)
    await flush()
    expect(scroll).toBe(50 * 240 + 30 + 300)
    expect(wrapper.find('[data-card="50"]').exists()).toBe(true)
  })
  it('restores CW state after recycling and preserves the anchor when a page is evicted/reloaded', async () => {
    const wrapper = feed(160)
    await flush()
    await wrapper.get('[data-card="0"] .cw').trigger('click')
    await scrollTo(100 * 240)
    await scrollTo(0)
    expect(wrapper.get('[data-card="0"] .cw').text()).toBe('收起')
    await scrollTo(100 * 240 + 16)
    await wrapper.setProps({ statuses: statuses(160, 20), hasPrevious: true })
    await flush()
    expect(scroll).toBe(100 * 240 + 16)
    await wrapper.setProps({ statuses: statuses(160), hasPrevious: false })
    await flush()
    expect(scroll).toBe(100 * 240 + 16)
  })
  it('releases interaction state on account/scope changes even when public status IDs overlap', async () => {
    const wrapper = feed(160)
    await flush()
    await wrapper.get('[data-card="0"] .edit').trigger('click')
    await wrapper.get('[data-card="0"] input').setValue('account A private draft')
    invalidateSessionData()
    await flush()
    expect(wrapper.findAll('input')).toHaveLength(0)
    await wrapper.get('[data-card="0"] .cw').trigger('click')
    await wrapper.setProps({ scopeKey: 'another-feed' })
    expect(wrapper.get('[data-card="0"] .cw').text()).toBe('展开')
  })
  it('offers a bounded restart instead of fetching every evicted page after a distant jump', async () => {
    const wrapper = feed(160)
    await flush()
    await scrollTo(140 * 240)
    await wrapper.setProps({ statuses: statuses(160, 100), hasPrevious: true })
    await flush()
    await scrollTo(0)
    const count = wrapper.emitted('loadPrevious')?.length ?? 0
    expect(wrapper.text()).toContain('较早页面已释放')
    await wrapper.get('.timeline-released button').trigger('click')
    expect(wrapper.emitted('reloadHead')).toHaveLength(1)
    expect(wrapper.emitted('loadPrevious')?.length ?? 0).toBe(count)
  })
  it('keeps keyboard focus attached to the same wrapper when scrolling, and drops revoked rows', async () => {
    const wrapper = feed(160)
    await flush()
    const button = wrapper.get('[data-card="0"] .cw').element as HTMLButtonElement
    button.focus()
    await flush()
    await scrollTo(100 * 240)
    expect(document.activeElement).toBe(button)
    expect(wrapper.find('[data-card="0"]').exists()).toBe(true)
    await wrapper.setProps({ statuses: statuses(159, 1) })
    await flush()
    expect(wrapper.find('[data-card="0"]').exists()).toBe(false)
    expect(wrapper.emitted('pinnedIds')?.at(-1)).toEqual([[]])
  })
  it('restores each tab independently without reusing another tab’s CW or draft', async () => {
    const wrapper = feed(160)
    await flush()
    await wrapper.get('[data-card="0"] .cw').trigger('click')
    await wrapper.setProps({ scopeKey: 'second-tab', statuses: statuses(160, 200) })
    await flush()
    expect(wrapper.get('[data-card="200"] .cw').text()).toBe('展开')
    await wrapper.setProps({ scopeKey: 'window-test', statuses: statuses(160) })
    await flush()
    expect(wrapper.get('[data-card="0"] .cw').text()).toBe('收起')
  })
  it('restores measured navigation geometry without retaining post content', async () => {
    const first = feed(160)
    await flush()
    resize([{ target: first.get('[data-timeline-id="0"]').element, borderBoxSize: [{ blockSize: 500 }] } as unknown as ResizeObserverEntry], {} as ResizeObserver)
    await flush()
    await first.get('[data-card="0"] .cw').trigger('click')
    first.unmount(); wrappers = []
    const second = feed(160)
    await flush()
    expect(second.get('[data-card="0"] .cw').text()).toBe('收起')
    await scrollTo(50 * 240 + 260)
    expect(second.find('[data-card="50"]').exists()).toBe(true)
  })
})
