import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { useLinkPreview } from '../useLinkPreview'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { sessionEpoch } from '@/shared/api/sessionScope'
import type { MastodonCard, MastodonStatus } from '@/shared/types/mastodon'

const observers: { callback: IntersectionObserverCallback; node?: Element }[] = []
const wrappers: VueWrapper[] = []
const preview = { url: 'https://news.test/a', title: 'Remote title', description: '', image: null, type: 'link', provider_name: 'News' } as MastodonCard
const Host = defineComponent({
  props: { status: { type: Object, required: true }, revealed: { type: Boolean, default: true } },
  setup(props) {
    const el = ref<HTMLElement>()
    const card = useLinkPreview(() => props.status as MastodonStatus, el, () => props.revealed)
    return { el, card }
  },
  template: '<div ref="el">{{ card?.title }}</div>',
})
function create(id = 'a', extra = {}) {
  const wrapper = mount(Host, { props: { status: { id, content: '<p><a href="https://news.test/a">News</a></p>', visibility: 'public', card: null, ...extra } } })
  wrappers.push(wrapper)
  return wrapper
}
function visibility(index: number, visible: boolean) {
  const observer = observers[index]!
  observer.callback([{ target: observer.node, isIntersecting: visible } as IntersectionObserverEntry], {} as IntersectionObserver)
}
beforeEach(() => {
  vi.useFakeTimers(); sessionEpoch.value++; observers.length = 0
  vi.stubGlobal('IntersectionObserver', class {
    index: number
    constructor(callback: IntersectionObserverCallback) { this.index = observers.push({ callback }) - 1 }
    observe(node: Element) { observers[this.index]!.node = node }
    disconnect() {}
  })
})
afterEach(async () => {
  for (const wrapper of wrappers.splice(0)) wrapper.unmount()
  sessionEpoch.value++; await flushPromises()
  vi.clearAllTimers(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals()
})

describe('visible asynchronous link previews', () => {
  it('does not fetch offscreen, hidden warnings, direct messages or posts with media', async () => {
    const fetch = vi.spyOn(postApi, 'getCard').mockResolvedValue(null)
    const hidden = create('cw'); await hidden.setProps({ revealed: false }); visibility(0, true)
    create('offscreen')
    create('direct', { visibility: 'direct' }); visibility(2, true)
    create('media', { media_attachments: [{ id: '1' }] }); visibility(3, true)
    await vi.advanceTimersByTimeAsync(20_000)
    expect(fetch).not.toHaveBeenCalled()
    await hidden.setProps({ revealed: true }); await vi.advanceTimersByTimeAsync(3000)
    expect(fetch).toHaveBeenCalledTimes(1)
  })
  it('deduplicates copies and virtual remounts while retrying null at most three times', async () => {
    const fetch = vi.spyOn(postApi, 'getCard').mockResolvedValue(null)
    const first = create(); const second = create(); visibility(0, true); visibility(1, true)
    await vi.advanceTimersByTimeAsync(16_000)
    expect(fetch).toHaveBeenCalledTimes(3)
    first.unmount(); second.unmount(); create(); visibility(2, true)
    await vi.advanceTimersByTimeAsync(30_000)
    expect(fetch).toHaveBeenCalledTimes(3)
  })
  it('limits concurrent work and removes rows scrolled away before requests start', async () => {
    const complete: ((value: MastodonCard | null) => void)[] = []
    const fetch = vi.spyOn(postApi, 'getCard').mockImplementation(() => new Promise(resolve => complete.push(resolve)))
    for (let i = 0; i < 5; i++) { create(String(i)); visibility(i, true) }
    await vi.advanceTimersByTimeAsync(3000)
    expect(fetch).toHaveBeenCalledTimes(2)
    for (let i = 2; i < 5; i++) visibility(i, false)
    for (const resolve of complete) resolve(preview)
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(wrappers[0]!.text()).toBe('Remote title')
  })
  it('discards late account responses and does not reuse the previous session cache', async () => {
    const complete: ((value: MastodonCard | null) => void)[] = []
    const fetch = vi.spyOn(postApi, 'getCard').mockImplementation(() => new Promise(resolve => complete.push(resolve)))
    const wrapper = create(); visibility(0, true); await vi.advanceTimersByTimeAsync(3000)
    sessionEpoch.value++; complete[0]!(preview); await flushPromises()
    expect(wrapper.text()).toBe('')
    await vi.advanceTimersByTimeAsync(3000)
    expect(fetch).toHaveBeenCalledTimes(2)
    complete[1]!({ ...preview, title: 'Current session' }); await flushPromises()
    expect(wrapper.text()).toBe('Current session')
  })
  it('keeps a decoded card when scrolled out and back without collapsing row height', async () => {
    const fetch = vi.spyOn(postApi, 'getCard').mockResolvedValue(preview)
    const wrapper = create(); visibility(0, true); await vi.advanceTimersByTimeAsync(3000)
    expect(wrapper.text()).toBe('Remote title')
    visibility(0, false)
    expect(wrapper.text()).toBe('Remote title')
    visibility(0, true); await vi.advanceTimersByTimeAsync(3000)
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toBe('Remote title')
  })
  it('does not let an old failed request stop the next account lookup', async () => {
    let fail!: (error: unknown) => void
    const fetch = vi.spyOn(postApi, 'getCard')
      .mockImplementationOnce(() => new Promise((_, reject) => { fail = reject }))
      .mockResolvedValue(preview)
    const wrapper = create(); visibility(0, true); await vi.advanceTimersByTimeAsync(3000)
    sessionEpoch.value++
    fail({ response: { status: 403 } }); await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toBe('Remote title')
  })
  it('bounds metadata across distinct rows even before the cache TTL expires', async () => {
    const fetch = vi.spyOn(postApi, 'getCard').mockResolvedValue(preview)
    for (let i = 0; i < 128; i++) { create(String(i)); visibility(i, true) }
    await vi.advanceTimersByTimeAsync(3000)
    expect(fetch).toHaveBeenCalledTimes(128)
    for (const wrapper of wrappers) wrapper.unmount()
    const next = create('128'); visibility(128, true)
    await vi.advanceTimersByTimeAsync(3000); next.unmount()
    create('0'); visibility(129, true)
    await vi.advanceTimersByTimeAsync(3000)
    // The oldest entry must be evicted even though its 60-second TTL is live.
    expect(fetch).toHaveBeenCalledTimes(130)
  })
})
