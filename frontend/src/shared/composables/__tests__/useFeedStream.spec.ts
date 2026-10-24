import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STREAM_GAP_RESYNC_MS, streamTransport, useFeedStream, type FeedStreamStatus } from '../useFeedStream'
import { invalidateSessionData } from '@/shared/api/sessionScope'

let push: ReadableStreamDefaultController<Uint8Array>
const mounted: ReturnType<typeof mount>[] = []
const fetcher = vi.fn()
const encode = (value: string) => new TextEncoder().encode(value)
function setup(callback = vi.fn().mockResolvedValue(true), query = 'stream=user', onStatus?: (status: FeedStreamStatus) => void) {
  const target = ref(query)
  const wrapper = mount(defineComponent({ setup() { useFeedStream(target, callback, { onStatus }); return () => null } }))
  mounted.push(wrapper)
  return { callback, target }
}
const hint = (epoch: string, revision: string) => encode(`event: feed.changed\ndata: {"epoch":"${epoch}","revision":"${revision}"}\n\n`)
beforeEach(() => {
  vi.useFakeTimers(); vi.stubGlobal('fetch', fetcher)
  streamTransport.sseUntil = Number.POSITIVE_INFINITY // these cases cover the SSE transport
  Object.defineProperty(document, 'hidden', { configurable: true, value: false })
  localStorage.setItem('auth_token', 'test-secret')
  fetcher.mockReset().mockImplementation(async () => ({ ok: true, headers: new Headers({ 'Content-Type': 'text/event-stream' }), body: new ReadableStream<Uint8Array>({ start(controller) { push = controller } }) }))
})
afterEach(() => { streamTransport.sseUntil = 0; streamTransport.wsFailures = 0; mounted.splice(0).forEach(w => w.unmount()); vi.useRealTimers(); vi.unstubAllGlobals() })

describe('feed invalidation stream', () => {
  it('uses header auth and does not revalidate a healthy home stream on a hint burst', async () => {
    const { callback } = setup()
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(fetcher.mock.calls[0]![0]).not.toContain('test-secret')
    expect(fetcher.mock.calls[0]![1].headers.Authorization).toBe('Bearer test-secret')
    expect(callback).toHaveBeenCalledTimes(1)
    callback.mockClear()
    for (let i = 0; i < 10; i++) push.enqueue(encode(`event: feed.changed\ndata: {"epoch":"a","revision":"${i}"}\n\n`))
    push.enqueue(encode(': heartbeat\n\nevent: feed.changed\ndata: {"epoch":"a","revision":"9"}\n\n'))
    await flushPromises(); await vi.advanceTimersByTimeAsync(20_000)
    expect(callback).not.toHaveBeenCalled()
  })
  it('does not reread home on a short flap, and does after the socket was down', async () => {
    const { callback } = setup()
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    push.enqueue(hint('subscribed', 'r1'))
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    callback.mockClear()
    push.close(); await flushPromises(); await vi.advanceTimersByTimeAsync(1_250)
    push.enqueue(hint('subscribed', 'r2'))
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(callback).not.toHaveBeenCalled()
    push.close(); await flushPromises(); await vi.advanceTimersByTimeAsync(STREAM_GAP_RESYNC_MS)
    push.enqueue(hint('subscribed', 'r3'))
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(1)
    expect(callback).toHaveBeenLastCalledWith('gap')
  })
  it('aborts old scopes and suppresses hidden page polling, then rechecks on resume', async () => {
    const { callback, target } = setup()
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    const old = fetcher.mock.calls[0]![1].signal as AbortSignal
    target.value = 'stream=list&list=2'; await flushPromises()
    expect(old.aborted).toBe(true)
    expect(fetcher.mock.calls.at(-1)![0]).toContain('list=2')
    const next = fetcher.mock.calls.at(-1)![1].signal as AbortSignal
    invalidateSessionData(); await flushPromises(); expect(next.aborted).toBe(true)
    Object.defineProperty(document, 'hidden', { configurable: true, value: true })
    document.dispatchEvent(new Event('visibilitychange')); callback.mockClear()
    await vi.advanceTimersByTimeAsync(120000); expect(callback).not.toHaveBeenCalled()
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    document.dispatchEvent(new Event('visibilitychange')); await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(1)
  })

  it('does not revalidate a healthy public stream on a hint firehose, and still polls', async () => {
    const { callback } = setup(vi.fn().mockResolvedValue(true), 'stream=public')
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(1)
    expect(callback).toHaveBeenLastCalledWith('poll')
    for (let i = 0; i < 40; i++) push.enqueue(hint('public', `p${i}`))
    await flushPromises(); await vi.advanceTimersByTimeAsync(20_000)
    expect(callback).toHaveBeenCalledTimes(1)
    callback.mockClear()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(callback).toHaveBeenCalledTimes(1)
    expect(callback).toHaveBeenLastCalledWith('poll')
  })

  it('does not refetch a healthy public stream when the network reports online', async () => {
    const { callback } = setup(vi.fn().mockResolvedValue(true), 'stream=public')
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(1)
    push.enqueue(hint('subscribed', 'ready'))
    await flushPromises()
    const streams = fetcher.mock.calls.length
    callback.mockClear()
    // Stay under the 45s socket stall. Events are closer than the 15s online gate.
    for (let i = 0; i < 3; i++) {
      window.dispatchEvent(new Event('online'))
      await flushPromises()
      await vi.advanceTimersByTimeAsync(10_000)
    }
    expect(callback).not.toHaveBeenCalled()
    expect(fetcher.mock.calls.length).toBe(streams)
  })

  it('reconnects a public stream after a short drop without walking the window', async () => {
    const { callback } = setup(vi.fn().mockResolvedValue(true), 'stream=public')
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    push.enqueue(hint('subscribed', 'ready'))
    await flushPromises()
    callback.mockClear()
    const streams = fetcher.mock.calls.length
    push.close()
    await flushPromises()
    window.dispatchEvent(new Event('online'))
    await flushPromises(); await vi.advanceTimersByTimeAsync(1_000)
    expect(callback).not.toHaveBeenCalled()
    expect(fetcher.mock.calls.length).toBeGreaterThan(streams)
  })

  it('follows an in-flight head poll with one gap walk, and does not queue a second window', async () => {
    let release!: () => void
    const callback = vi.fn(() => new Promise<boolean>(resolve => { release = () => resolve(true) }))
    setup(callback, 'stream=public')
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(1)
    expect(callback).toHaveBeenLastCalledWith('poll')
    push.enqueue(hint('subscribed', 'r1'))
    await flushPromises()
    push.close(); await flushPromises(); await vi.advanceTimersByTimeAsync(STREAM_GAP_RESYNC_MS)
    push.enqueue(hint('subscribed', 'r2'))
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(1)
    release()
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(2)
    expect(callback).toHaveBeenLastCalledWith('gap')
    push.close(); await flushPromises(); await vi.advanceTimersByTimeAsync(STREAM_GAP_RESYNC_MS)
    push.enqueue(hint('subscribed', 'r3'))
    await flushPromises(); await vi.advanceTimersByTimeAsync(1_000)
    expect(callback).toHaveBeenCalledTimes(2)
    release()
    await flushPromises(); await vi.advanceTimersByTimeAsync(1_000)
    expect(callback).toHaveBeenCalledTimes(2)
  })

  it('does not open another public read while one is in flight, and does not chain one per hint', async () => {
    let release!: () => void
    const callback = vi.fn(() => new Promise<void>(resolve => { release = resolve }))
    setup(callback, 'stream=public:local')
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(1)
    for (let i = 0; i < 20; i++) push.enqueue(hint('public', `live${i}`))
    await flushPromises(); await vi.advanceTimersByTimeAsync(2_000)
    expect(callback).toHaveBeenCalledTimes(1)
    release()
    await flushPromises(); await vi.advanceTimersByTimeAsync(20_000)
    expect(callback).toHaveBeenCalledTimes(1)
  })

  it('ignores a visibility flicker instead of reconnecting and rereading the public timeline', async () => {
    const { callback } = setup(vi.fn().mockResolvedValue(true), 'stream=public')
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    const streams = fetcher.mock.calls.length
    callback.mockClear()
    for (let i = 0; i < 6; i++) {
      Object.defineProperty(document, 'hidden', { configurable: true, value: true })
      document.dispatchEvent(new Event('visibilitychange'))
      Object.defineProperty(document, 'hidden', { configurable: true, value: false })
      document.dispatchEvent(new Event('visibilitychange'))
      await flushPromises(); await vi.advanceTimersByTimeAsync(300)
    }
    expect(callback).not.toHaveBeenCalled()
    expect(fetcher.mock.calls.length).toBe(streams)
  })

  it('rechecks a public timeline after the page was actually hidden', async () => {
    const { callback } = setup(vi.fn().mockResolvedValue(true), 'stream=public')
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    callback.mockClear()
    Object.defineProperty(document, 'hidden', { configurable: true, value: true })
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(1_000)
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(1)
  })

  it('reports streamed status ids on home without an HTTP revalidate', async () => {
    const seen: FeedStreamStatus[] = []
    const { callback } = setup(vi.fn().mockResolvedValue(true), 'stream=user', status => { seen.push(status) })
    await flushPromises(); await vi.advanceTimersByTimeAsync(250); callback.mockClear()
    push.enqueue(encode('event: update\ndata: {"id":"19687","content":"secret"}\n\n'))
    push.enqueue(encode('event: delete\ndata: {"id":"10","content":"gone"}\n\n'))
    push.enqueue(encode('event: status.update\ndata: {"id":"11","text":"edited"}\n\n'))
    push.enqueue(encode('event: update\ndata: {"id":"not-a-status","content":"nope"}\n\n'))
    push.enqueue(hint('home', '99'))
    await flushPromises(); await vi.advanceTimersByTimeAsync(5_000)
    expect(callback).not.toHaveBeenCalled()
    expect(seen).toEqual([
      { event: 'update', id: '19687' },
      { event: 'delete', id: '10' },
      { event: 'status.update', id: '11' },
    ])
  })

  it('still revalidates a notification stream when a later hint arrives', async () => {
    const { callback } = setup(vi.fn().mockResolvedValue(true), 'stream=notification')
    await flushPromises(); await vi.advanceTimersByTimeAsync(250); callback.mockClear()
    push.enqueue(hint('a', '1'))
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(1)
    push.enqueue(hint('a', '2'))
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(2)
  })
})
