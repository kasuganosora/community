import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { backoffDelay, feedSocketProtocols, streamTransport, useFeedStream, STREAM_STALL_MS } from '../useFeedStream'

class FakeSocket {
  static all: FakeSocket[] = []
  onmessage: ((e: { data: string }) => void) | null = null
  onerror: (() => void) | null = null
  onclose: (() => void) | null = null
  closed = false
  url: string
  protocols: string[]
  constructor(url: string, protocols: string[]) { this.url = url; this.protocols = protocols; FakeSocket.all.push(this) }
  close() { if (this.closed) return; this.closed = true; this.onclose?.() }
  hint(epoch: string, revision: string) { this.onmessage?.({ data: JSON.stringify({ stream: ['feed'], event: 'feed.changed', payload: JSON.stringify({ epoch, revision }) }) }) }
  drop() { this.closed = true; this.onclose?.() }
}
const mounted: ReturnType<typeof mount>[] = []
const fetcher = vi.fn()
function setup(target = 'stream=notification&unread=1') {
  const callback = vi.fn().mockResolvedValue(true)
  mounted.push(mount(defineComponent({ setup() { useFeedStream(ref(target), callback, { poll: false }); return () => null } })))
  return callback
}
const last = () => FakeSocket.all.at(-1)!

beforeEach(() => {
  vi.useFakeTimers()
  FakeSocket.all = []
  streamTransport.wsFailures = 0; streamTransport.sseUntil = 0
  vi.stubGlobal('WebSocket', FakeSocket)
  vi.stubGlobal('fetch', fetcher)
  fetcher.mockReset().mockRejectedValue(new Error('sse down'))
  vi.spyOn(Math, 'random').mockReturnValue(1)
  Object.defineProperty(document, 'hidden', { configurable: true, value: false })
  localStorage.setItem('auth_token', 'a+b/c=')
})
afterEach(() => { mounted.splice(0).forEach(w => w.unmount()); vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('feed stream reconnect', () => {
  it('backs off 0.5s doubling to 30s with jitter', () => {
    expect([0, 1, 2, 5, 6, 20].map(n => backoffDelay(n, () => 1))).toEqual([500, 1000, 2000, 16000, 30000, 30000])
    expect(backoffDelay(0, () => 0)).toBe(250)
  })

  it('prefers WebSocket with the token in the subprotocol, not the URL', async () => {
    setup(); await flushPromises()
    expect(last().url).toMatch(/^ws:\/\/[^/]+\/api\/v1\/streaming\/feed\?stream=notification&unread=1$/)
    expect(last().url).not.toContain('a+b')
    expect(last().protocols).toEqual(['community.v1', 'community.bearer.YStiL2M9'])
    expect(feedSocketProtocols(null)).toEqual(['community.v1'])
    expect(fetcher).not.toHaveBeenCalled()
  })

  it('resyncs after every confirmed reconnect and reconnects immediately after a drop', async () => {
    const callback = setup(); await flushPromises()
    await vi.advanceTimersByTimeAsync(250); callback.mockClear()
    last().hint('subscribed', 'r1'); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(1) // resync after confirmation
    await vi.advanceTimersByTimeAsync(20_000) // healthy connection
    last().drop()
    await vi.advanceTimersByTimeAsync(499); expect(FakeSocket.all).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1); expect(FakeSocket.all).toHaveLength(2)
    callback.mockClear()
    last().hint('subscribed', 'r2'); await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(1) // missed events refetched over HTTP
  })

  it('grows the delay for connections that drop early and resets once healthy', async () => {
    setup(); await flushPromises()
    const delays: number[] = []
    for (let i = 0; i < 2; i++) {
      const before = FakeSocket.all.length
      last().hint('subscribed', `x${i}`); last().drop()
      let waited = 0
      while (FakeSocket.all.length === before) { await vi.advanceTimersByTimeAsync(100); waited += 100 }
      delays.push(waited)
    }
    expect(delays).toEqual([500, 1000])
  })

  it('falls back to SSE after repeated WebSocket failures', async () => {
    setup(); await flushPromises()
    for (let i = 0; i < 3; i++) { last().drop(); await vi.advanceTimersByTimeAsync(30_000) }
    expect(FakeSocket.all).toHaveLength(3)
    expect(fetcher).toHaveBeenCalled()
    expect(fetcher.mock.calls[0]![0]).toContain('/api/v1/streaming/feed?stream=notification')
    expect(streamTransport.sseUntil).toBeGreaterThan(Date.now())
  })

  it('does not reread a public timeline on every fast reconnect', async () => {
    const callback = setup('stream=public')
    await flushPromises()
    last().hint('subscribed', 'r1')
    await vi.advanceTimersByTimeAsync(250)
    expect(callback).toHaveBeenCalledTimes(1)
    const opened = FakeSocket.all.length
    for (let i = 0; i < 4; i++) {
      const before = FakeSocket.all.length
      last().drop()
      await vi.advanceTimersByTimeAsync(2_000)
      if (FakeSocket.all.length === before) break
      last().hint('subscribed', `again${i}`)
      await vi.advanceTimersByTimeAsync(250)
    }
    expect(callback).toHaveBeenCalledTimes(1)
    expect(FakeSocket.all.length).toBeGreaterThan(opened)
  })

  it('reconnects a stalled socket and reconnects at once when coming back online', async () => {
    setup(); await flushPromises()
    last().hint('subscribed', 's')
    await vi.advanceTimersByTimeAsync(STREAM_STALL_MS)
    expect(FakeSocket.all[0]!.closed).toBe(true)
    await vi.advanceTimersByTimeAsync(1000)
    const count = FakeSocket.all.length
    window.dispatchEvent(new Event('online')); await flushPromises()
    expect(FakeSocket.all.length).toBe(count + 1)
    expect(FakeSocket.all[count - 1]!.closed).toBe(true)
  })
})
