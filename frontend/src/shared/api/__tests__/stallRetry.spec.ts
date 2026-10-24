import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { AxiosAdapter, AxiosProgressEvent, AxiosResponse, InternalAxiosRequestConfig } from 'axios'
import { nextTick, watch } from 'vue'
import { applyStallRetry, isStallRetryable, stallRetryAdapter, stalledRequests } from '../stallRetry'

beforeEach(() => { vi.useFakeTimers(); stalledRequests.value = 0 })
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

const cfg = (extra: Partial<InternalAxiosRequestConfig> = {}) => ({ method: 'get', url: '/api/v2/search', headers: {}, ...extra }) as InternalAxiosRequestConfig
const ok = (data: unknown) => ({ data, status: 200 }) as AxiosResponse

/** Base adapter whose n-th call hangs until aborted, or answers. */
function adapter(plan: Array<'hang' | 'ok' | 'fail'>) {
  const signals: AbortSignal[] = []
  const base: AxiosAdapter = (c) => {
    const step = plan[signals.length]
    const signal = c.signal as AbortSignal
    signals.push(signal)
    if (step === 'ok') return Promise.resolve(ok(signals.length))
    if (step === 'fail') return Promise.reject(new Error('timeout of 15000ms exceeded'))
    return new Promise((_, reject) => signal.addEventListener('abort', () => reject(new Error('canceled'))))
  }
  return { base, signals }
}

it('only applies to idempotent search, timeline, and account profile GETs', () => {
  expect(isStallRetryable(cfg())).toBe(true)
  expect(isStallRetryable(cfg({ url: '/api/v1/timelines/home' }))).toBe(true)
  expect(isStallRetryable(cfg({ url: '/api/v1/accounts/lookup' }))).toBe(true)
  expect(isStallRetryable(cfg({ url: '/api/v1/accounts/1538/statuses' }))).toBe(true)
  expect(isStallRetryable(cfg({ method: 'post' }))).toBe(false)
  expect(isStallRetryable(cfg({ url: '/api/v1/statuses' }))).toBe(false)
  expect(isStallRetryable(cfg({ url: '/api/v1/accounts/search' }))).toBe(false)
  expect(isStallRetryable(cfg({ method: 'post', url: '/api/v1/accounts/1538/follow' }))).toBe(false)
})

it('retries a stalled account lookup once and keeps a fast 410 as a single attempt', async () => {
  const stalled = adapter(['hang', 'ok'])
  const lookup = cfg({ url: '/api/v1/accounts/lookup', timeout: 30000 })
  applyStallRetry(lookup, stalled.base)
  const recovered = (lookup.adapter as AxiosAdapter)(lookup)
  await vi.advanceTimersByTimeAsync(8000)
  await expect(recovered).resolves.toMatchObject({ data: 2 })
  expect(stalled.signals).toHaveLength(2)
  expect(stalled.signals[0].aborted).toBe(true)

  const gone = Object.assign(new Error('gone'), { response: { status: 410 } })
  const base = vi.fn<AxiosAdapter>(() => Promise.reject(gone))
  const missing = cfg({ url: '/api/v1/accounts/lookup', timeout: 30000 })
  applyStallRetry(missing, base)
  await expect((missing.adapter as AxiosAdapter)(missing)).rejects.toBe(gone)
  await vi.advanceTimersByTimeAsync(30000)
  expect(base).toHaveBeenCalledTimes(1)
})

it('answers normally without retrying when the first attempt is quick', async () => {
  const { base, signals } = adapter(['ok'])
  await expect(stallRetryAdapter(base, 8000)(cfg())).resolves.toMatchObject({ data: 1 })
  await vi.advanceTimersByTimeAsync(9000)
  expect(signals).toHaveLength(1)
})

it('aborts a stalled attempt after 8 s and retries once, showing the hint meanwhile', async () => {
  const { base, signals } = adapter(['hang', 'fail'])
  const seen: number[] = []
  const stop = watch(stalledRequests, v => seen.push(v))
  const p = stallRetryAdapter(base, 8000)(cfg())
  const settled = p.catch((e: Error) => e)
  await vi.advanceTimersByTimeAsync(8000)
  expect(signals).toHaveLength(2)
  expect(signals[0].aborted).toBe(true)
  expect((await settled as Error).message).toMatch('timeout')
  await nextTick()
  expect(seen).toContain(1)
  stop()
  expect(stalledRequests.value).toBe(0)
})

it('returns the retry result, and a caller abort cancels both attempts', async () => {
  const { base } = adapter(['hang', 'ok'])
  const p = stallRetryAdapter(base, 8000)(cfg())
  await vi.advanceTimersByTimeAsync(8000)
  await expect(p).resolves.toMatchObject({ data: 2 })

  const outer = new AbortController()
  const h = adapter(['hang'])
  const q = stallRetryAdapter(h.base, 8000)(cfg({ signal: outer.signal }))
  outer.abort()
  await expect(q).rejects.toThrow('canceled')
})

it('lets a slow response finish after 8 seconds without discarding it', async () => {
  vi.stubGlobal('navigator', { connection: { effectiveType: '3g' }, onLine: true })
  let respond!: (res: AxiosResponse) => void
  const base = vi.fn<AxiosAdapter>(() => new Promise(resolve => { respond = resolve }))
  const request = stallRetryAdapter(base)(cfg({ timeout: 45000 }))
  await vi.advanceTimersByTimeAsync(20000)
  expect(base).toHaveBeenCalledTimes(1)
  expect(base.mock.calls[0][0].signal?.aborted).toBe(false)
  respond(ok('slow but successful'))
  await expect(request).resolves.toMatchObject({ data: 'slow but successful' })
})

it('never restarts a response that is already downloading and forwards progress', async () => {
  let respond!: (res: AxiosResponse) => void
  const progress = vi.fn()
  const base = vi.fn<AxiosAdapter>(() => new Promise(resolve => { respond = resolve }))
  const request = stallRetryAdapter(base)(cfg({ onDownloadProgress: progress }))
  await vi.advanceTimersByTimeAsync(6000)
  const event = { loaded: 1024 } as AxiosProgressEvent
  base.mock.calls[0][0].onDownloadProgress!(event)
  await vi.advanceTimersByTimeAsync(6000)
  expect(base).toHaveBeenCalledTimes(1)
  expect(progress).toHaveBeenCalledWith(event)
  respond(ok('complete'))
  await request
})

it('keeps the total timeout budget and cancels an in-flight retry without another attempt', async () => {
  const outer = new AbortController()
  const { base, signals } = adapter(['hang', 'hang'])
  const requests: InternalAxiosRequestConfig[] = []
  const tracked: AxiosAdapter = config => { requests.push(config); return base(config) }
  const request = stallRetryAdapter(tracked)(cfg({ timeout: 15000, signal: outer.signal }))
  const result = request.catch(error => error)
  await vi.advanceTimersByTimeAsync(8000)
  expect(requests[1].timeout).toBe(7000)
  expect(stalledRequests.value).toBe(1)
  outer.abort()
  expect(await result).toMatchObject({ code: 'ERR_CANCELED' })
  expect(signals.every(signal => signal.aborted)).toBe(true)
  expect(stalledRequests.value).toBe(0)
  await vi.advanceTimersByTimeAsync(60000)
  expect(signals).toHaveLength(2)
})

it('does not send a request that was already cancelled', async () => {
  const controller = new AbortController()
  controller.abort()
  const base = vi.fn<AxiosAdapter>()
  await expect(stallRetryAdapter(base)(cfg({ signal: controller.signal }))).rejects.toMatchObject({ code: 'ERR_CANCELED' })
  await vi.advanceTimersByTimeAsync(60000)
  expect(base).not.toHaveBeenCalled()
})

it('does not launch a retry while offline', async () => {
  vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false)
  const controller = new AbortController()
  const { base, signals } = adapter(['hang'])
  const request = stallRetryAdapter(base)(cfg({ signal: controller.signal })).catch(error => error)
  await vi.advanceTimersByTimeAsync(8000)
  expect(signals).toHaveLength(1)
  controller.abort()
  await request
  expect(stalledRequests.value).toBe(0)
})
