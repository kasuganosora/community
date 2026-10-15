import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { AxiosAdapter, AxiosResponse, InternalAxiosRequestConfig } from 'axios'
import { nextTick, watch } from 'vue'
import { isStallRetryable, stallRetryAdapter, stalledRequests } from '../stallRetry'

beforeEach(() => { vi.useFakeTimers(); stalledRequests.value = 0 })
afterEach(() => vi.useRealTimers())

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

it('only applies to idempotent search and timeline GETs', () => {
  expect(isStallRetryable(cfg())).toBe(true)
  expect(isStallRetryable(cfg({ url: '/api/v1/timelines/home' }))).toBe(true)
  expect(isStallRetryable(cfg({ method: 'post' }))).toBe(false)
  expect(isStallRetryable(cfg({ url: '/api/v1/statuses' }))).toBe(false)
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
