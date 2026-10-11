import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useFeedStream } from '../useFeedStream'
import { invalidateSessionData } from '@/shared/api/sessionScope'

let push: ReadableStreamDefaultController<Uint8Array>
const mounted: ReturnType<typeof mount>[] = []
const fetcher = vi.fn()
const encode = (value: string) => new TextEncoder().encode(value)
function setup(callback = vi.fn().mockResolvedValue(true)) {
  const target = ref('stream=user')
  const wrapper = mount(defineComponent({ setup() { useFeedStream(target, callback); return () => null } }))
  mounted.push(wrapper)
  return { callback, target }
}
beforeEach(() => {
  vi.useFakeTimers(); vi.stubGlobal('fetch', fetcher)
  Object.defineProperty(document, 'hidden', { configurable: true, value: false })
  localStorage.setItem('auth_token', 'test-secret')
  fetcher.mockReset().mockImplementation(async () => ({ ok: true, headers: new Headers({ 'Content-Type': 'text/event-stream' }), body: new ReadableStream<Uint8Array>({ start(controller) { push = controller } }) }))
})
afterEach(() => { mounted.splice(0).forEach(w => w.unmount()); vi.useRealTimers(); vi.unstubAllGlobals() })

describe('feed invalidation stream', () => {
  it('uses header auth and merges bursts without treating heartbeat or duplicate revisions as content', async () => {
    const { callback } = setup()
    await flushPromises(); await vi.advanceTimersByTimeAsync(250)
    expect(fetcher.mock.calls[0]![0]).not.toContain('test-secret')
    expect(fetcher.mock.calls[0]![1].headers.Authorization).toBe('Bearer test-secret')
    callback.mockClear()
    for (let i = 0; i < 10; i++) push.enqueue(encode(`event: feed.changed\ndata: {"epoch":"a","revision":"${i}"}\n\n`))
    await flushPromises(); await vi.advanceTimersByTimeAsync(249)
    expect(callback).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1); expect(callback).toHaveBeenCalledTimes(1)
    push.enqueue(encode(': heartbeat\n\nevent: feed.changed\ndata: {"epoch":"a","revision":"9"}\n\n'))
    await flushPromises(); await vi.advanceTimersByTimeAsync(300)
    expect(callback).toHaveBeenCalledTimes(1)
  })
  it('revalidates after disconnect even if the next revision is unchanged', async () => {
    const { callback } = setup()
    await flushPromises(); await vi.advanceTimersByTimeAsync(250); callback.mockClear()
    push.close(); await flushPromises(); await vi.advanceTimersByTimeAsync(1250)
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(callback).toHaveBeenCalledTimes(1)
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
})
