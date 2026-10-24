/// <reference types="node" />
import { runInNewContext } from 'node:vm'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import template from '../../../../vite-plugins/sw-template.js?raw'

const source = template.replace('__BUILD_ID__', '"test-build"')
type WorkerEvent = {
  request?: { url: string; method: string; mode: string }
  data?: unknown
  ports?: { postMessage: (data: unknown) => void }[]
  waitUntil: (promise: Promise<unknown>) => void
  respondWith?: (promise: Promise<unknown>) => void
}
const response = () => ({ ok: true, status: 200, headers: { get: () => 'text/html' }, clone() { return this } })

function worker() {
  const listeners: Record<string, (event: WorkerEvent) => void> = {}
  const stored = new Map<string, unknown>()
  const put = vi.fn(async (key: string, value: unknown) => { stored.set(key, value) })
  const fetch = vi.fn(async (_url: unknown, _options?: { signal: AbortSignal }): Promise<unknown> => response())
  const skipWaiting = vi.fn()
  runInNewContext(source, {
    self: { addEventListener: (name: string, handler: (event: WorkerEvent) => void) => { listeners[name] = handler }, skipWaiting, location: { origin: 'https://community.test' }, clients: { claim: vi.fn() } },
    caches: { open: async () => ({ put }), match: async (key: string) => stored.get(key), keys: async () => [], delete: vi.fn() },
    fetch, URL, AbortController, setTimeout, clearTimeout,
  })
  function dispatch(name: string, path = '/') {
    const background: Promise<unknown>[] = []
    let answer: Promise<unknown> | undefined
    listeners[name]({ request: { url: `https://community.test${path}`, method: 'GET', mode: 'navigate' }, waitUntil: promise => { background.push(promise) }, respondWith: promise => { answer = promise } })
    return { background, answer }
  }
  function revalidate(revision: string | null) {
    const background: Promise<unknown>[] = []
    const posted: unknown[] = []
    listeners.message({
      data: { type: 'emoji-revalidate', revision },
      ports: [{ postMessage: (data: unknown) => { posted.push(data) } }],
      waitUntil: promise => { background.push(promise) },
    })
    return { background, posted }
  }
  return { stored, put, fetch, dispatch, revalidate, skipWaiting }
}

describe('service worker on slow connections', () => {
  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  it('installs without downloading unused pages or locales', async () => {
    const w = worker()
    await Promise.all(w.dispatch('install').background)
    expect(w.fetch.mock.calls.map(call => call[0])).toEqual(['/'])
    expect(w.skipWaiting).toHaveBeenCalledTimes(1)
    expect(w.stored.has('/__community_shell__')).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('bounds the shell download so installation cannot hang indefinitely', async () => {
    const w = worker()
    w.fetch.mockImplementation((_url, options) => new Promise((_resolve, reject) => {
      options!.signal.addEventListener('abort', () => reject(new Error('timeout')))
    }))
    const install = Promise.all(w.dispatch('install').background)
    await vi.advanceTimersByTimeAsync(5000)
    await install
    expect(w.skipWaiting).toHaveBeenCalledTimes(1)
  })

  it('serves visited chunks from cache and never intercepts API or media requests', async () => {
    const w = worker()
    const cached = response()
    w.stored.set('https://community.test/assets/js/page-hash.js', cached)
    expect(await w.dispatch('fetch', '/assets/js/page-hash.js').answer).toBe(cached)
    for (const path of ['/api/v1/timelines/home', '/api/v1/custom_emojis', '/emoji-assets/wave.png', '/proxy/media/abc', '/_matrix/client/v3/sync', '/oauth/authorize']) {
      expect(w.dispatch('fetch', path).answer).toBeUndefined()
    }
    expect(w.fetch).not.toHaveBeenCalled()
  })

  it('opens the cached shell after three seconds and refreshes it in the background', async () => {
    const w = worker()
    const cached = response(), fresh = response()
    w.stored.set('/__community_shell__', cached)
    let finish!: (value: unknown) => void
    w.fetch.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const event = w.dispatch('fetch', '/home')
    await vi.advanceTimersByTimeAsync(3000)
    expect(await event.answer).toBe(cached)
    finish(fresh)
    await Promise.all(event.background)
    expect(w.stored.get('/__community_shell__')).toBe(fresh)
  })

  it('waits for a slow first visit when there is no cached shell', async () => {
    const w = worker()
    const fresh = response()
    let finish!: (value: unknown) => void
    w.fetch.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const event = w.dispatch('fetch', '/home')
    let answered = false
    void event.answer!.then(() => { answered = true })
    await vi.advanceTimersByTimeAsync(6000)
    expect(answered).toBe(false)
    finish(fresh)
    expect(await event.answer).toBe(fresh)
    await Promise.all(event.background)
  })

  it('uses the cached shell offline without caching private data', async () => {
    const w = worker()
    const cached = response()
    w.stored.set('/__community_shell__', cached)
    w.fetch.mockRejectedValue(new Error('offline'))
    const event = w.dispatch('fetch', '/home')
    expect(await event.answer).toBe(cached)
    await Promise.all(event.background)
    expect(w.put).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('revalidates the custom emoji catalogue in the background and skips the body when the revision matches', async () => {
    const w = worker()
    const etag = 'W/"1.2.3.4"'
    w.fetch.mockImplementation(async (_url: unknown, options?: { signal: AbortSignal }) => {
      const headers = (options as { headers?: Record<string, string> } | undefined)?.headers
      if (headers?.['If-None-Match'] === etag) {
        return { ok: false, status: 304, headers: { get: (name: string) => name === 'ETag' ? etag : null }, json: async () => { throw new Error('304 has no body') } }
      }
      return {
        ok: true,
        status: 200,
        headers: { get: (name: string) => name === 'ETag' ? 'W/"9.9.9.9"' : 'application/json' },
        json: async () => [{ shortcode: 'wave', url: 'https://cdn.test/wave.png', static_url: 'https://cdn.test/wave.png', visible_in_picker: true }],
      }
    })
    const same = w.revalidate(etag)
    await Promise.all(same.background)
    expect(same.posted[0]).toEqual({ accepted: true })
    expect(same.posted[1]).toEqual({ status: 304, etag, body: null })
    expect(w.fetch).toHaveBeenCalledWith('/api/v1/custom_emojis', expect.objectContaining({ cache: 'no-store' }))
    expect(w.put).not.toHaveBeenCalled()
    const changed = w.revalidate(null)
    await Promise.all(changed.background)
    expect(changed.posted[1]).toMatchObject({ status: 200, etag: 'W/"9.9.9.9"' })
    expect(changed.posted[1]).toEqual(expect.objectContaining({ body: [expect.objectContaining({ shortcode: 'wave' })] }))
    expect(vi.getTimerCount()).toBe(0)
  })
})
