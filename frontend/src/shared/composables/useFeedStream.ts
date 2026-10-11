import { onBeforeUnmount, onMounted, watch, type Ref } from 'vue'
import { API_BASE_URL, tokenService } from '@/shared/api/client'
import { sessionEpoch } from '@/shared/api/sessionScope'

/** SSE transports invalidations only. Authorization and content always come from HTTP. */
export function useFeedStream(target: Ref<string>, revalidate: () => Promise<boolean | void>) {
  let controller: AbortController | undefined
  let reconnect: ReturnType<typeof setTimeout> | undefined
  let debounce: ReturnType<typeof setTimeout> | undefined
  let fallback: ReturnType<typeof setInterval> | undefined
  let generation = 0
  let mounted = false
  let running = false
  let dirty = false
  let revision = ''
  let failures = 0

  function schedule() {
    dirty = true
    if (!mounted || document.hidden || running || debounce) return
    debounce = setTimeout(() => { debounce = undefined; void refresh() }, 250)
  }
  async function refresh() {
    if (!mounted || document.hidden || running || !dirty) return
    const mine = generation
    running = true
    dirty = false
    try {
      if (await revalidate() === false && mine === generation) dirty = true
    } catch {
      // Keep stale data visible on transport failures; retry at the bounded fallback.
    } finally {
      running = false
      if (dirty) schedule()
    }
  }
  async function connect(mine: number) {
    const current = new AbortController()
    controller = current
    try {
      const headers: Record<string, string> = { Accept: 'text/event-stream' }
      const token = tokenService.get()
      if (token) headers.Authorization = `Bearer ${token}`
      const response = await fetch(`${API_BASE_URL}/api/v1/streaming/feed?${target.value}`, {
        headers, signal: current.signal, cache: 'no-store', redirect: 'error',
      })
      if (!response.ok || !response.body || !response.headers.get('content-type')?.includes('text/event-stream')) throw new Error('feed stream unavailable')
      if (mine !== generation) { await response.body.cancel(); return }
      failures = 0
      schedule() // A reconnect can include deletes and late low-ID commits.
      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      try {
        while (mine === generation && !current.signal.aborted) {
          const chunk = await reader.read()
          if (chunk.done || mine !== generation || current.signal.aborted) break
          buffer = (buffer + decoder.decode(chunk.value, { stream: true })).replace(/\r\n/g, '\n')
          if (buffer.length > 65536) throw new Error('oversized stream event')
          let end: number
          while ((end = buffer.indexOf('\n\n')) !== -1) {
            const frame = buffer.slice(0, end); buffer = buffer.slice(end + 2)
            const lines = frame.split('\n')
            if (!lines.some(line => line === 'event: feed.changed' || line === 'event:feed.changed')) continue
            try {
              const payload = JSON.parse(lines.filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n'))
              if (typeof payload.epoch !== 'string' || typeof payload.revision !== 'string') continue
              const next = JSON.stringify([payload.epoch, payload.revision])
              if (next !== revision) { revision = next; schedule() }
            } catch { /* malformed events never become content */ }
          }
        }
      } finally { await reader.cancel().catch(() => {}); reader.releaseLock() }
    } catch { /* polling remains available when proxies do not support streaming */ }
    finally {
      if (mine === generation && mounted && !document.hidden) {
        reconnect = setTimeout(() => { reconnect = undefined; void connect(mine) }, Math.min(30_000, 1000 * 2 ** Math.min(failures++, 5)))
      }
    }
  }
  function restart() {
    ++generation
    controller?.abort()
    clearTimeout(reconnect); reconnect = undefined
    clearTimeout(debounce); debounce = undefined
    revision = ''; dirty = false
    if (!mounted || document.hidden) return
    schedule()
    const mine = generation
    void Promise.resolve().then(() => { if (mine === generation && mounted && !document.hidden) void connect(mine) })
  }
  watch([target, sessionEpoch], restart, { flush: 'sync' })
  onMounted(() => {
    mounted = true
    document.addEventListener('visibilitychange', restart)
    fallback = setInterval(schedule, 60_000)
    restart()
  })
  onBeforeUnmount(() => {
    mounted = false; restart(); clearInterval(fallback)
    document.removeEventListener('visibilitychange', restart)
  })
}
