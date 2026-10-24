import { onBeforeUnmount, onMounted, watch, type Ref } from 'vue'
import { API_BASE_URL, tokenService } from '@/shared/api/client'
import { sessionEpoch } from '@/shared/api/sessionScope'

/**
 * WebSocket is preferred (an SSE response through Cloudflare is sometimes reset
 * right after connect); SSE is the fallback after repeated WebSocket failures.
 * Timeline sockets (home, public, tag, list) carry status ids. While one of
 * those sockets is healthy, a hint must not refetch the loaded window: new
 * posts stay on the pill via `onStatus`. A head check still runs every 60s,
 * and a full window read still runs after a real gap (the socket was down, or
 * the tab was actually hidden and restored). Browser "online" flaps on a
 * lossy VPN are not that gap: they must not close a live socket or walk the
 * window. A second window is never queued behind one that is already reading.
 * Notification streams stay eager.
 */
export type FeedSyncReason = 'sync' | 'poll' | 'gap'
export type FeedStatusEvent = 'update' | 'delete' | 'status.update'
export interface FeedStreamStatus { event: FeedStatusEvent, id: string }
export interface FeedStreamOptions {
  /** false: no periodic fallback; revalidate only on connect/reconnect, hints, and window focus. */
  poll?: boolean
  /** Status ids from a healthy timeline socket. Must not fetch the timeline. */
  onStatus?: (status: FeedStreamStatus) => void
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
/**
 * A confirmed socket that was down at least this long is a real gap: one
 * window read on the next subscribed frame. Shorter flaps are not.
 */
export const STREAM_GAP_RESYNC_MS = PUBLIC_REVALIDATE_MIN_MS
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

/** Home, public, tag and list. Notification streams are not timelines. */
export function isTimelineFeed(query: string): boolean {
  const stream = new URLSearchParams(query).get('stream') ?? ''
  return stream === 'user' || isPublicFeed(query) || stream === 'list' || stream === 'hashtag' || stream.startsWith('hashtag:')
}

const STATUS_EVENTS = new Set<FeedStatusEvent>(['update', 'delete', 'status.update'])

/** The only field a timeline frame may carry. Anything else is ignored. */
export function feedStatusID(event: string, raw: string): FeedStreamStatus | null {
  if (!STATUS_EVENTS.has(event as FeedStatusEvent)) return null
  try {
    const payload = JSON.parse(raw) as unknown
    const value = typeof payload === 'string' || typeof payload === 'number'
      ? payload
      : payload && typeof payload === 'object' && 'id' in payload
        ? (payload as { id: unknown }).id
        : ''
    const id = typeof value === 'number' || typeof value === 'string' ? String(value) : ''
    return /^\d+$/.test(id) ? { event: event as FeedStatusEvent, id } : null
  } catch { return null }
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

export function useFeedStream(target: Ref<string>, revalidate: (reason?: FeedSyncReason) => Promise<boolean | void>, options: FeedStreamOptions = {}) {
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
  /** When the last confirmed timeline socket closed. 0 until that happens. */
  let downSince = 0
  let pending: FeedSyncReason = 'sync'
  /** Reason of the read currently inside `revalidate`. */
  let inflight: FeedSyncReason = 'sync'

  function rank(reason: FeedSyncReason) { return reason === 'gap' ? 2 : reason === 'poll' ? 1 : 0 }
  function revalidateDelay(immediate: boolean): number {
    if (immediate || !isPublicFeed(target.value) || !lastRevalidated) return HINT_DEBOUNCE_MS
    const remain = PUBLIC_REVALIDATE_MIN_MS - (Date.now() - lastRevalidated)
    return remain > HINT_DEBOUNCE_MS ? remain : HINT_DEBOUNCE_MS
  }
  function schedule(options?: { gap?: boolean, soon?: boolean, poll?: boolean }) {
    const next: FeedSyncReason = options?.poll ? 'poll' : options?.gap ? 'gap' : 'sync'
    if (!dirty || rank(next) >= rank(pending)) pending = next
    dirty = true
    if (!mounted || document.hidden || running) {
      // One timeline read at a time. A window already in flight is the
      // resync, so a later gap must not line up another max_id walk behind
      // it. A head poll can still be followed by that one gap walk.
      if (running && isTimelineFeed(target.value) && !(pending === 'gap' && inflight === 'poll')) dirty = false
      return
    }
    const immediate = options?.gap === true || options?.soon === true || options?.poll === true
    if (debounce && !immediate) return
    if (debounce) { clearTimeout(debounce); debounce = undefined }
    debounce = setTimeout(() => { debounce = undefined; void refresh() }, revalidateDelay(immediate))
  }
  async function refresh() {
    if (!mounted || document.hidden || running || !dirty) return
    const mine = generation
    const why = pending
    inflight = why
    running = true
    dirty = false
    pending = 'sync'
    let retrySoon = false
    try {
      if (await revalidate(why) === false && mine === generation) { dirty = true; retrySoon = true; pending = why }
    } catch {
      // Keep stale data visible on transport failures; retry at the bounded fallback.
    } finally {
      running = false
      // Quiet window starts when the read finishes, so a slow public timeline
      // cannot be followed immediately by another full-window read.
      if (!retrySoon && mine === generation && isPublicFeed(target.value)) lastRevalidated = Date.now()
      // A timeline window never schedules another window. The only follow-up
      // is one gap walk after a head poll, including when a resume bumped the
      // generation while that poll was still reading.
      const followGap = isTimelineFeed(target.value) && pending === 'gap' && why === 'poll'
      if (dirty && mounted && !document.hidden && (mine === generation || followGap)) {
        const follow = pending
        const chainWindow = !(isTimelineFeed(target.value) && why !== 'poll')
        dirty = false
        pending = 'sync'
        if (chainWindow) schedule(follow === 'gap' ? { gap: true } : follow === 'poll' ? { poll: true } : retrySoon ? { soon: true } : undefined)
      }
    }
  }

  function emitStatus(event: string, raw: string) {
    const status = feedStatusID(event, raw)
    if (status) options.onStatus?.(status)
  }

  /** Handles one feed.changed payload; returns true for the subscribed confirmation. */
  function onHint(raw: string): boolean {
    try {
      const payload = JSON.parse(raw)
      if (typeof payload.epoch !== 'string' || typeof payload.revision !== 'string') return false
      const next = JSON.stringify([payload.epoch, payload.revision])
      const changed = next !== revision
      if (changed) revision = next
      const subscribed = payload.epoch === 'subscribed'
      // A healthy timeline learns about posts from status ids, not from hints.
      if (changed && !subscribed && !isTimelineFeed(target.value)) schedule()
      return subscribed
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
      if (!isTimelineFeed(target.value)) { schedule(); return }
      // The mount read already covered the first connect. Resync only after a real outage.
      if (downSince && Date.now() - downSince >= STREAM_GAP_RESYNC_MS) {
        downSince = 0
        schedule({ gap: true })
      }
    }
    const finish = () => {
      if (finished) return
      finished = true
      clearTimeout(stall)
      if (confirmedAt !== null) downSince = Date.now()
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
          if (typeof message.event !== 'string' || typeof message.payload !== 'string') return
          if (message.event === 'feed.changed') { if (onHint(message.payload)) confirm() }
          else emitStatus(message.event, message.payload)
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
                let eventName = ''
                const data: string[] = []
                for (const line of lines) {
                  if (line.startsWith('event:')) eventName = line.slice(6).trim()
                  else if (line.startsWith('data:')) data.push(line.slice(5).trimStart())
                }
                const raw = data.join('\n')
                if (eventName === 'feed.changed') { if (onHint(raw)) confirm() }
                else if (eventName) emitStatus(eventName, raw)
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
    revision = ''; dirty = false; pending = 'sync'; failures = 0
    if (!mounted || document.hidden) return
    if (options?.gap) downSince = 0
    schedule(options?.gap ? { gap: true } : isTimelineFeed(target.value) ? { poll: true } : undefined)
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
    // Notifications still resync on reconnect. A timeline already learned
    // about posts from status ids; tearing the socket down here was a
    // public?limit=20 about every quiet-gap while a VPN flipped "online".
    if (!isTimelineFeed(target.value)) { restart({ gap: true }); return }
    if (stop) return
    const now = Date.now()
    if (onlineAt && now - onlineAt < STREAM_GAP_RESYNC_MS) return
    onlineAt = now
    if (!mounted || document.hidden) return
    clearTimeout(reconnect)
    reconnect = undefined
    connect(generation)
  }
  function onFocus() { schedule({ gap: true }) }
  watch([target, sessionEpoch], () => { lastRevalidated = 0; restart() }, { flush: 'sync' })
  onMounted(() => {
    mounted = true
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('online', onOnline)
    if (poll) fallback = setInterval(() => schedule(isTimelineFeed(target.value) ? { poll: true } : undefined), 60_000)
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
