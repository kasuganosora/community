import { onBeforeUnmount, onMounted, watch, type Ref } from 'vue'
import { API_BASE_URL, tokenService } from '@/shared/api/client'
import { sessionEpoch } from '@/shared/api/sessionScope'

/**
 * Feed streams carry invalidation hints only; authorization and content
 * always come from HTTP. WebSocket is preferred (an SSE response through
 * Cloudflare is sometimes reset right after connect); SSE is the fallback
 * after repeated WebSocket failures. A confirmed connect resyncs through
 * `revalidate`, so events missed during a gap still show up. Public timelines
 * are a firehose (one revision per status, including fast reconnects): those
 * HTTP reads share one quiet window and still land on the new-posts pill.
 * Home and notification streams stay immediate. A real tab resume and the 60s
 * poll are not held back by that window.
 */
export interface FeedStreamOptions {
  /** false: no periodic fallback; revalidate only on connect/reconnect, hints, and window focus. */
  poll?: boolean
}

export const STREAM_BASE_DELAY_MS = 500
export const STREAM_MAX_DELAY_MS = 30_000
/** A connection that stays confirmed this long resets the backoff. */
export const STREAM_HEALTHY_MS = 10_000
/** No bytes/messages for this long (server heartbeats every 15s): reconnect. */
export const STREAM_STALL_MS = 45_000
/**
 * Minimum gap between public-timeline HTTP revalidations while hints or fast
 * reconnects keep arriving. The 60s poll still runs, and a real tab resume
 * still reads immediately.
 */
export const PUBLIC_REVALIDATE_MIN_MS = 15_000
/** Visibility blips shorter than this do not drop the socket or refetch. */
export const STREAM_VISIBILITY_GRACE_MS = 1_000
const HINT_DEBOUNCE_MS = 250
const WS_FAILURE_LIMIT = 3
const SSE_FALLBACK_MS = 5 * 60_000

/** Shared by every stream in the tab: one flaky transport decision for all. */
export const streamTransport = { wsFailures: 0, sseUntil: 0 }

/** Exponential backoff with jitter: 0.5s, 1s, 2s ... capped at 30s, each scaled into [50%, 100%]. */
export function backoffDelay(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(STREAM_MAX_DELAY_MS, STREAM_BASE_DELAY_MS * 2 ** Math.max(0, attempt))
  return Math.round(base / 2 + random() * base / 2)
}

export function chooseTransport(now = Date.now()): 'ws' | 'sse' {
  return typeof WebSocket !== 'undefined' && now >= streamTransport.sseUntil ? 'ws' : 'sse'
}

/** Global and local public timelines are the status firehose. */
export function isPublicFeed(query: string): boolean {
  const stream = new URLSearchParams(query).get('stream') ?? ''
  return stream === 'public' || stream.startsWith('public:')
}

function recordWebSocketResult(ok: boolean) {
  if (ok) { streamTransport.wsFailures = 0; return }
  if (++streamTransport.wsFailures >= WS_FAILURE_LIMIT) {
    streamTransport.wsFailures = 0
    streamTransport.sseUntil = Date.now() + SSE_FALLBACK_MS
  }
}

function base64url(value: string): string {
  return btoa(unescape(encodeURIComponent(value))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export function feedSocketUrl(query: string): string {
  const base = API_BASE_URL || window.location.origin
  return `${base.replace(/^http/, 'ws')}/api/v1/streaming/feed?${query}`
}

/** The token travels in Sec-WebSocket-Protocol, never in the URL. */
export function feedSocketProtocols(token: string | null): string[] {
  return token ? ['community.v1', `community.bearer.${base64url(token)}`] : ['community.v1']
}

export function useFeedStream(target: Ref<string>, revalidate: () => Promise<boolean | void>, options: FeedStreamOptions = {}) {
  const poll = options.poll !== false
  let stop: (() => void) | undefined
  let reconnect: ReturnType<typeof setTimeout> | undefined
  let debounce: ReturnType<typeof setTimeout> | undefined
  let fallback: ReturnType<typeof setInterval> | undefined
  let generation = 0
  let mounted = false
  let running = false
  let dirty = false
  let revision = ''
  let failures = 0
  /** When the last public HTTP revalidation finished; 0 until the first one does. */
  let lastRevalidated = 0
  let hideTimer: ReturnType<typeof setTimeout> | undefined
  let onlineAt = 0

  function revalidateDelay(gap: boolean): number {
    if (gap || !isPublicFeed(target.value) || !lastRevalidated) return HINT_DEBOUNCE_MS
    const remain = PUBLIC_REVALIDATE_MIN_MS - (Date.now() - lastRevalidated)
    return remain > HINT_DEBOUNCE_MS ? remain : HINT_DEBOUNCE_MS
  }
  function schedule(options?: { gap?: boolean, soon?: boolean }) {
    dirty = true
    if (!mounted || document.hidden || running) return
    const gap = options?.gap === true
    const soon = options?.soon === true
    if (debounce && !gap && !soon) return
    if (debounce) { clearTimeout(debounce); debounce = undefined }
    debounce = setTimeout(() => { debounce = undefined; void refresh() }, soon ? HINT_DEBOUNCE_MS : revalidateDelay(gap))
  }
  async function refresh() {
    if (!mounted || document.hidden || running || !dirty) return
    const mine = generation
    running = true
    dirty = false
    let retrySoon = false
    try {
      if (await revalidate() === false && mine === generation) { dirty = true; retrySoon = true }
    } catch {
      // Keep stale data visible on transport failures; retry at the bounded fallback.
    } finally {
      running = false
      // Quiet window starts when the read finishes, so a slow public timeline
      // cannot be followed immediately by another full-window read.
      if (!retrySoon && mine === generation && isPublicFeed(target.value)) lastRevalidated = Date.now()
      if (dirty) schedule(retrySoon ? { soon: true } : undefined)
    }
  }

  /** Handles one feed.changed payload; returns true for the subscribed confirmation. */
  function onHint(raw: string): boolean {
    try {
      const payload = JSON.parse(raw)
      if (typeof payload.epoch !== 'string' || typeof payload.revision !== 'string') return false
      const next = JSON.stringify([payload.epoch, payload.revision])
      if (next !== revision) { revision = next; schedule() }
      return payload.epoch === 'subscribed'
    } catch { return false /* malformed events never become content */ }
  }

  /** Opens one connection; calls `ended(confirmedAt)` exactly once when it is over. */
  function open(mine: number, ended: (confirmedAt: number | null) => void) {
    const kind = chooseTransport()
    let confirmedAt: number | null = null
    let finished = false
    let stall: ReturnType<typeof setTimeout> | undefined
    const alive = () => { clearTimeout(stall); stall = setTimeout(() => close(), STREAM_STALL_MS) }
    const confirm = () => {
      if (confirmedAt !== null) return
      confirmedAt = Date.now()
      if (kind === 'ws') recordWebSocketResult(true)
      schedule() // resync once; a public firehose then waits out the quiet window
    }
    const finish = () => {
      if (finished) return
      finished = true
      clearTimeout(stall)
      if (kind === 'ws' && confirmedAt === null && mine === generation) recordWebSocketResult(false)
      ended(confirmedAt)
    }
    let close: () => void
    alive()
    if (kind === 'ws') {
      let socket: WebSocket
      try {
        socket = new WebSocket(feedSocketUrl(target.value), feedSocketProtocols(tokenService.get()))
      } catch { close = () => {}; stop = close; queueMicrotask(finish); return }
      close = () => { try { socket.close() } catch { /* already closed */ } finish() }
      socket.onmessage = (event) => {
        alive()
        if (mine !== generation || typeof event.data !== 'string' || event.data.length > 65536) return
        try {
          const message = JSON.parse(event.data) as { event?: string; payload?: unknown }
          if (message.event === 'feed.changed' && typeof message.payload === 'string' && onHint(message.payload)) confirm()
        } catch { /* ignore */ }
      }
      socket.onerror = () => close()
      socket.onclose = () => finish()
    } else {
      const controller = new AbortController()
      close = () => { controller.abort(); finish() }
      void (async () => {
        try {
          const headers: Record<string, string> = { Accept: 'text/event-stream' }
          const token = tokenService.get()
          if (token) headers.Authorization = `Bearer ${token}`
          const response = await fetch(`${API_BASE_URL}/api/v1/streaming/feed?${target.value}`, {
            headers, signal: controller.signal, cache: 'no-store', redirect: 'error',
          })
          if (!response.ok || !response.body || !response.headers.get('content-type')?.includes('text/event-stream')) throw new Error('feed stream unavailable')
          const reader = response.body.getReader()
          const decoder = new TextDecoder()
          let buffer = ''
          try {
            while (mine === generation && !controller.signal.aborted) {
              const chunk = await reader.read()
              if (chunk.done || mine !== generation || controller.signal.aborted) break
              alive()
              buffer = (buffer + decoder.decode(chunk.value, { stream: true })).replace(/\r\n/g, '\n')
              if (buffer.length > 65536) throw new Error('oversized stream event')
              let end: number
              while ((end = buffer.indexOf('\n\n')) !== -1) {
                const frame = buffer.slice(0, end); buffer = buffer.slice(end + 2)
                const lines = frame.split('\n')
                if (!lines.some(line => line === 'event: feed.changed' || line === 'event:feed.changed')) continue
                if (onHint(lines.filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n'))) confirm()
              }
            }
          } finally { await reader.cancel().catch(() => {}); reader.releaseLock() }
        } catch { /* reconnect below; polling covers proxies without streaming */ }
        finish()
      })()
    }
    stop = () => close()
  }

  function connect(mine: number) {
    open(mine, (confirmedAt) => {
      if (mine !== generation) return
      stop = undefined
      // A connection that was confirmed and stayed up resets the backoff; one
      // that dropped early (or never confirmed) backs off further.
      failures = confirmedAt !== null && Date.now() - confirmedAt >= STREAM_HEALTHY_MS ? 0 : failures + 1
      if (!mounted || document.hidden) return
      reconnect = setTimeout(() => { reconnect = undefined; if (mine === generation) connect(mine) }, backoffDelay(failures - 1))
    })
  }
  function restart(options?: { gap?: boolean }) {
    ++generation
    const previous = stop
    stop = undefined
    previous?.()
    clearTimeout(reconnect); reconnect = undefined
    clearTimeout(debounce); debounce = undefined
    clearTimeout(hideTimer); hideTimer = undefined
    revision = ''; dirty = false; failures = 0
    if (!mounted || document.hidden) return
    schedule(options?.gap ? { gap: true } : undefined)
    const mine = generation
    void Promise.resolve().then(() => { if (mine === generation && mounted && !document.hidden) connect(mine) })
  }
  function onVisibility() {
    if (document.hidden) {
      clearTimeout(hideTimer)
      hideTimer = setTimeout(() => { hideTimer = undefined; if (mounted && document.hidden) restart() }, STREAM_VISIBILITY_GRACE_MS)
      return
    }
    // A blip (toolbar, DevTools, mobile chrome) never closes the socket.
    if (hideTimer) { clearTimeout(hideTimer); hideTimer = undefined; return }
    restart({ gap: true })
  }
  function onOnline() {
    const now = Date.now()
    if (isPublicFeed(target.value) && onlineAt && now - onlineAt < PUBLIC_REVALIDATE_MIN_MS) return
    onlineAt = now
    restart({ gap: true })
  }
  function onFocus() { schedule({ gap: true }) }
  watch([target, sessionEpoch], () => { lastRevalidated = 0; restart() }, { flush: 'sync' })
  onMounted(() => {
    mounted = true
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('online', onOnline)
    if (poll) fallback = setInterval(() => schedule(), 60_000)
    else window.addEventListener('focus', onFocus)
    restart()
  })
  onBeforeUnmount(() => {
    mounted = false; clearTimeout(hideTimer); restart(); clearInterval(fallback)
    document.removeEventListener('visibilitychange', onVisibility)
    window.removeEventListener('online', onOnline)
    window.removeEventListener('focus', onFocus)
  })
}
