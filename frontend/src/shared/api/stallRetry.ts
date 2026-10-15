import axios from 'axios'
import type { AxiosAdapter, AxiosResponse, InternalAxiosRequestConfig } from 'axios'
import { ref } from 'vue'

// Idempotent reads that sometimes stall in transit (proxy / mobile network)
// while the server answers in milliseconds. If no response arrives within
// STALL_MS, the first attempt is dropped and one fresh request is sent; only
// if that also fails does the caller see the error.
export const STALL_MS = 8000
const RETRYABLE = [/^\/api\/v2\/search(\?|$)/, /^\/api\/v1\/timelines\//]

/** Number of stalled requests currently being retried (drives the hint). */
export const stalledRequests = ref(0)

export function isStallRetryable(config: Pick<InternalAxiosRequestConfig, 'method' | 'url'>): boolean {
  return (config.method ?? 'get').toLowerCase() === 'get' && RETRYABLE.some(re => re.test(config.url ?? ''))
}

function linkedAbort(signal: InternalAxiosRequestConfig['signal']): AbortController {
  const ctl = new AbortController()
  const outer = signal as AbortSignal | undefined
  if (outer?.aborted) ctl.abort()
  else outer?.addEventListener?.('abort', () => ctl.abort(), { once: true })
  return ctl
}

export function stallRetryAdapter(base: AxiosAdapter, stallMs = STALL_MS): AxiosAdapter {
  return (config) => {
    const first = linkedAbort(config.signal)
    let stalled = false
    return new Promise<AxiosResponse>((resolve, reject) => {
      const timer = setTimeout(() => {
        stalled = true
        first.abort()
        stalledRequests.value++
        const second = linkedAbort(config.signal)
        base({ ...config, signal: second.signal })
          .then(resolve, reject)
          .finally(() => { stalledRequests.value = Math.max(0, stalledRequests.value - 1) })
      }, stallMs)
      base({ ...config, signal: first.signal }).then(
        (res) => { if (!stalled) { clearTimeout(timer); resolve(res) } },
        (err) => { if (!stalled) { clearTimeout(timer); reject(err) } },
      )
    })
  }
}

/** Request interceptor hook: route eligible GETs through the retry adapter. */
export function applyStallRetry(config: InternalAxiosRequestConfig, defaultAdapter: InternalAxiosRequestConfig['adapter']): InternalAxiosRequestConfig {
  if (!isStallRetryable(config) || config.adapter && typeof config.adapter === 'function' && (config.adapter as { stallRetry?: boolean }).stallRetry) return config
  const base = axios.getAdapter(config.adapter ?? defaultAdapter)
  const wrapped = Object.assign(stallRetryAdapter(base), { stallRetry: true })
  config.adapter = wrapped
  return config
}
