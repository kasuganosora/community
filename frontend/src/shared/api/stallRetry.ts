import axios from 'axios'
import type { AxiosAdapter, AxiosResponse, InternalAxiosRequestConfig } from 'axios'
import { ref } from 'vue'
import { isSlowConnection } from '@/shared/utils/network'

// Idempotent reads that sometimes stall in transit (proxy / mobile network)
// while the server answers in milliseconds. If no response arrives within
// STALL_MS, the first attempt is dropped and one fresh request is sent. Slow
// connections get more time; a response already downloading is never restarted.
export const STALL_MS = 8000
export const SLOW_STALL_MS = 30000
const RETRYABLE = [/^\/api\/v2\/search(\?|$)/, /^\/api\/v1\/timelines\//]

/** Number of stalled requests currently being retried (drives the hint). */
export const stalledRequests = ref(0)

export function isStallRetryable(config: Pick<InternalAxiosRequestConfig, 'method' | 'url'>): boolean {
  return (config.method ?? 'get').toLowerCase() === 'get' && RETRYABLE.some(re => re.test(config.url ?? ''))
}

export function stallRetryAdapter(base: AxiosAdapter, stallMs?: number): AxiosAdapter {
  return (config) => {
    const outer = config.signal as AbortSignal | undefined
    let current = new AbortController()
    let retried = false
    let settled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const started = Date.now()
    const budget = config.timeout ?? 0
    return new Promise<AxiosResponse>((resolve, reject) => {
      function finish(error?: unknown, response?: AxiosResponse) {
        if (settled) return
        settled = true
        clearTimeout(timer)
        outer?.removeEventListener?.('abort', cancel)
        if (retried) stalledRequests.value = Math.max(0, stalledRequests.value - 1)
        if (error) reject(error)
        else resolve(response!)
      }
      function cancel() {
        current.abort()
        finish(new axios.CanceledError('canceled', config))
      }
      if (outer?.aborted) { cancel(); return }
      outer?.addEventListener?.('abort', cancel, { once: true })

      function attempt(timeout = config.timeout) {
        const controller = current
        const request = { ...config, timeout, signal: controller.signal, onDownloadProgress: (event: Parameters<NonNullable<typeof config.onDownloadProgress>>[0]) => {
          // Restarting a partially received response wastes scarce bandwidth.
          if (event.loaded > 0) clearTimeout(timer)
          config.onDownloadProgress?.(event)
        } }
        Promise.resolve().then(() => {
          if (settled) throw new axios.CanceledError('canceled', config)
          return base(request)
        }).then(
          response => { if (controller === current) finish(undefined, response) },
          error => { if (controller === current) finish(error) },
        )
      }
      timer = setTimeout(() => {
        // Offline: deliberately no stall retry. A fresh request would fail
        // immediately, and the first attempt is still in flight — it either
        // completes once connectivity returns or fails with its own timeout
        // (the original budget), surfacing the normal offline/timeout error
        // and retry UI. We do not re-arm on the `online` event to avoid a
        // second concurrent request racing the still-pending first one.
        if (settled || navigator.onLine === false) return
        const remaining = budget > 0 ? budget - (Date.now() - started) : budget
        if (budget > 0 && remaining <= 0) return
        const first = current
        current = new AbortController()
        first.abort()
        retried = true
        stalledRequests.value++
        // Keep the original request's timeout budget across both attempts.
        attempt(remaining)
      }, stallMs ?? (isSlowConnection() ? SLOW_STALL_MS : STALL_MS))
      attempt()
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
