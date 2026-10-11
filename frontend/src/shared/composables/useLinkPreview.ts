import { onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { sessionEpoch } from '@/shared/api/sessionScope'
import type { MastodonCard, MastodonStatus } from '@/shared/types/mastodon'

// Shared across virtual rows and detail views. Only metadata is retained, never
// status bodies. A failed lookup has a bounded lifetime, not a permanent miss.
const MAX_ENTRIES = 128
const RETRY_AT = [3000, 8000, 15000]
const TTL = 60_000
interface Entry {
  id: string
  epoch: number
  started: number
  expires: number
  attempts: number
  card: MastodonCard | null
  listeners: Set<(card: MastodonCard | null) => void>
  timer?: ReturnType<typeof setTimeout>
  controller?: AbortController
  queued: boolean
  done: boolean
}
const cache = new Map<string, Entry>()
const queue: Entry[] = []
let active = 0
function current(entry: Entry) { return entry.epoch === sessionEpoch.value && cache.get(entry.id) === entry }
function unqueue(entry: Entry) {
  const index = queue.indexOf(entry)
  if (index >= 0) queue.splice(index, 1)
  entry.queued = false
}
function schedule(entry: Entry) {
  if (!current(entry) || !entry.listeners.size || entry.done || entry.timer || entry.queued || entry.controller) return
  const delay = Math.max(0, entry.started + RETRY_AT[entry.attempts]! - Date.now())
  entry.timer = setTimeout(() => {
    entry.timer = undefined
    if (!current(entry) || !entry.listeners.size) return
    entry.queued = true; queue.push(entry); drain()
  }, delay)
}
function drain() {
  while (active < 2 && queue.length) {
    const entry = queue.shift()!
    entry.queued = false
    if (!current(entry) || !entry.listeners.size || entry.done) continue
    active++
    entry.attempts++
    const controller = new AbortController()
    entry.controller = controller
    // The shared HTTP client enforces a 15-second timeout. Once started, a lookup
    // may finish into the session cache, but offscreen listeners receive nothing.
    void postApi.getCard(entry.id, controller.signal).then(card => {
      if (!current(entry)) return
      if (card) {
        entry.card = card; entry.done = true
        for (const listener of entry.listeners) listener(card)
      }
    }).catch((error: unknown) => {
      const code = (error as { response?: { status?: number } })?.response?.status
      if (current(entry) && code && [401, 403, 404].includes(code)) entry.done = true
    }).finally(() => {
      active--; entry.controller = undefined
      if (current(entry)) {
        if (entry.attempts >= RETRY_AT.length) entry.done = true
        if (entry.done) entry.expires = Date.now() + TTL
        else schedule(entry)
      }
      drain()
    })
  }
}
watch(sessionEpoch, () => {
  for (const entry of cache.values()) { clearTimeout(entry.timer); entry.controller?.abort() }
  cache.clear(); queue.length = 0
}, { flush: 'sync' })

function subscribe(id: string, listener: (card: MastodonCard | null) => void) {
  let entry = cache.get(id)
  if (entry?.done && entry.expires <= Date.now() && !entry.listeners.size) { cache.delete(id); entry = undefined }
  if (!entry) {
    if (cache.size >= MAX_ENTRIES) {
      const evict = [...cache.values()].find(value => !value.listeners.size && !value.controller)
      if (!evict) return () => {}
      clearTimeout(evict.timer); unqueue(evict); cache.delete(evict.id)
    }
    entry = { id, epoch: sessionEpoch.value, started: Date.now(), expires: 0, attempts: 0, card: null, listeners: new Set(), queued: false, done: false }
    cache.set(id, entry)
  }
  entry.listeners.add(listener)
  listener(entry.card)
  schedule(entry)
  return () => {
    entry.listeners.delete(listener)
    if (!entry.listeners.size) { clearTimeout(entry.timer); entry.timer = undefined; unqueue(entry) }
  }
}

function hasExternalLink(status: MastodonStatus) {
  if (!status.content?.includes('http')) return false
  const doc = new DOMParser().parseFromString(status.content, 'text/html')
  const candidates = [...doc.querySelectorAll('a[href]:not(.mention):not(.hashtag)')].map(a => a.getAttribute('href') || '')
  candidates.push(...((doc.body.textContent || '').match(/https?:\/\/[^\s<>"']+/g) || []))
  return candidates.some(raw => {
    try { const url = new URL(raw); return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password && url.origin !== window.location.origin }
    catch { return false }
  })
}

/** No observer means no background requests; the link itself remains usable. */
export function useLinkPreview(status: () => MastodonStatus, element: Ref<HTMLElement | undefined>, revealed: () => boolean) {
  const card = ref<MastodonCard | null>(null)
  let observer: IntersectionObserver | undefined
  let visible = false
  let unsubscribe = () => {}
  let generation = 0
  function restart(reset = false) {
    const mine = ++generation
    unsubscribe(); unsubscribe = () => {}
    if (reset) card.value = null
    const value = status()
    if (!visible || document.hidden || !revealed() || value.card || value.quote || value.reblog || value.visibility === 'direct' || value.media_attachments?.length || !hasExternalLink(value)) return
    unsubscribe = subscribe(value.id, result => { if (mine === generation && result) card.value = result })
  }
  watch([() => status().id, () => status().content, () => status().card, revealed, sessionEpoch], () => restart(true), { flush: 'sync' })
  function visibilityChanged() { restart() }
  onMounted(() => {
    if (!element.value || typeof IntersectionObserver === 'undefined') return
    observer = new IntersectionObserver(entries => {
      visible = entries.some(entry => entry.isIntersecting)
      restart()
    }, { rootMargin: '0px' })
    observer.observe(element.value)
    document.addEventListener('visibilitychange', visibilityChanged)
  })
  onBeforeUnmount(() => { generation++; unsubscribe(); observer?.disconnect(); document.removeEventListener('visibilitychange', visibilityChanged) })
  return card
}
