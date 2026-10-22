import { t } from '@/shared/i18n'
import { hasUnsavedWork, persistUnsavedWork } from './useUnsavedWork'

declare const __BUILD_ID__: string

/**
 * Keeps the open client on the server's frontend build (Misskey-style):
 * registers the service worker, compares /version.json with the build this
 * bundle came from on load, periodically and on focus, and after a deploy
 * refreshes the service worker and reloads once the user is idle (a toast
 * offers to reload right away). A reload never interrupts work: while any
 * composer holds unsent text/attachments, an upload runs or a modal is open,
 * drafts are persisted and the reload waits until that clears or the tab is
 * hidden (a hidden tab reloads immediately).
 */
const CHECK_INTERVAL_MS = 10 * 60_000
const IDLE_MS = 10_000
const RELOAD_KEY = 'community:version-reload'

export const currentBuild = typeof __BUILD_ID__ === 'string' ? __BUILD_ID__ : 'dev'

let pendingBuild: string | null = null
let lastInput = Date.now()
let registration: ServiceWorkerRegistration | undefined
let buildRequest: Promise<string | null> | null = null

export function fetchServerBuild(): Promise<string | null> {
  if (!buildRequest) buildRequest = readServerBuild().finally(() => { buildRequest = null })
  return buildRequest
}

async function readServerBuild(): Promise<string | null> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 10000)
  try {
    // A unique query on top of no-store: no browser, proxy or CDN copy can
    // answer it, whatever its cache rules.
    const bust = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`
    const res = await fetch(`/version.json?t=${bust}`, { cache: 'no-store', credentials: 'omit', signal: controller.signal })
    if (!res.ok) return null
    if (res.headers?.get?.('X-Community-Service-Worker') === 'off') void disableServiceWorker()
    const body = await res.json() as { build?: unknown }
    return typeof body.build === 'string' && body.build ? body.build : null
  } catch {
    return null
  } finally { clearTimeout(timer) }
}

/**
 * Rollback kill switch (server FRONTEND_SERVICE_WORKER=off): drop every
 * worker and Community cache; the server's /sw.js unregisters itself too.
 */
let swDisabled = false
export async function disableServiceWorker(): Promise<void> {
  if (swDisabled) return
  swDisabled = true
  registration = undefined
  try {
    if ('serviceWorker' in navigator) {
      const regs = await navigator.serviceWorker.getRegistrations()
      await Promise.all(regs.map(r => r.unregister()))
    }
    if (typeof caches !== 'undefined') {
      const keys = await caches.keys()
      await Promise.all(keys.filter(k => k.startsWith('community-')).map(k => caches.delete(k)))
    }
  } catch { /* best effort */ }
}

/**
 * A chunk failure can also be a slow/offline connection. Only reload when
 * the server confirms a different build; otherwise keep the failure/reload
 * action on the affected page. Browsers can cache a failed module import.
 */
export async function handleChunkLoadError(): Promise<void> {
  // Let Vite reject the import so route/dialog error handling can render.
  // Suppressing it would allow modules to render with missing stylesheets.
  if (navigator.onLine === false) return
  const build = await fetchServerBuild()
  if (!build || build === currentBuild) return
  const key = 'community:preload-reload'
  const last = Number(sessionStorage.getItem(key) || 0)
  if (Date.now() - last < 60_000) {
    return
  }
  sessionStorage.setItem(key, String(Date.now()))
  // Preserve attachments and open dialogs in memory until the user is ready.
  if (hasUnsavedWork() || isEditing()) { await onNewBuild(build); return }
  persistUnsavedWork()
  window.location.reload()
}

function isEditing(): boolean {
  const el = document.activeElement as HTMLElement | null
  if (!el) return false
  if (el.isContentEditable) return true
  if (el instanceof HTMLTextAreaElement) return el.value.trim() !== ''
  return el instanceof HTMLInputElement && !['button', 'checkbox', 'radio', 'submit'].includes(el.type) && el.value.trim() !== ''
}

function reload(build: string) {
  persistUnsavedWork()
  sessionStorage.setItem(RELOAD_KEY, build)
  window.location.reload()
}

function showToast(build: string) {
  if (document.getElementById('community-update-toast')) return
  const toast = document.createElement('div')
  toast.id = 'community-update-toast'
  toast.setAttribute('role', 'status')
  toast.style.cssText = 'position:fixed;left:50%;bottom:calc(16px + env(safe-area-inset-bottom));transform:translateX(-50%);z-index:10000;'
    + 'display:flex;gap:12px;align-items:center;box-sizing:border-box;width:min(480px,calc(100vw - 32px));padding:10px 14px;border-radius:12px;'
    + 'background:var(--td-bg-color-container,#fff);color:var(--td-text-color-primary,#000);box-shadow:0 6px 24px rgba(0,0,0,.18);font-size:14px;'
  const text = document.createElement('span')
  text.style.cssText = 'flex:1;min-width:0;line-height:1.4;'
  text.textContent = t('shared.appUpdate.available')
  const button = document.createElement('button')
  button.type = 'button'
  button.textContent = t('shared.appUpdate.reloadNow')
  button.style.cssText = 'flex:none;border:0;border-radius:8px;padding:6px 12px;cursor:pointer;background:var(--td-brand-color,#0052d9);color:#fff;font:inherit;'
  button.addEventListener('click', () => reload(build))
  toast.append(text, button)
  document.body.appendChild(toast)
}

function maybeReload() {
  if (!pendingBuild) return
  if (document.visibilityState === 'hidden') {
    reload(pendingBuild)
    return
  }
  if (hasUnsavedWork() || isEditing()) {
    // Keep drafts safe in case the user reloads or the tab is discarded.
    persistUnsavedWork()
    return
  }
  if (Date.now() - lastInput >= IDLE_MS) reload(pendingBuild)
}

async function onNewBuild(build: string) {
  if (pendingBuild === build) return
  // A stale CDN/edge copy can keep serving the old shell: reload at most once
  // per server build so the tab never loops.
  if (sessionStorage.getItem(RELOAD_KEY) === build) return
  pendingBuild = build
  try {
    await registration?.update()
  } catch { /* offline: the reload revalidates anyway */ }
  showToast(build)
  maybeReload()
}

export async function checkVersion(): Promise<void> {
  const build = await fetchServerBuild()
  if (build && build !== currentBuild) await onNewBuild(build)
}

export function startVersionSync(): void {
  if (import.meta.env.DEV || currentBuild === 'dev') return
  if (sessionStorage.getItem(RELOAD_KEY) === currentBuild) sessionStorage.removeItem(RELOAD_KEY)

  if ('serviceWorker' in navigator && window.isSecureContext) {
    navigator.serviceWorker.addEventListener('message', (e) => {
      if (e.data === 'COMMUNITY_SW_DISABLED') void disableServiceWorker()
    })
    void fetchServerBuild().then(() => {
      if (swDisabled) return
      return navigator.serviceWorker.register(`/sw.js?v=${encodeURIComponent(currentBuild)}`, { scope: '/' })
        .then((reg) => { registration = reg })
    }).catch((err) => console.warn('Service worker registration failed', err))
  }

  for (const type of ['pointerdown', 'keydown', 'wheel', 'touchstart']) {
    window.addEventListener(type, () => { lastInput = Date.now() }, { passive: true, capture: true })
  }
  window.addEventListener('focus', () => { void checkVersion() })
  window.addEventListener('online', () => { void checkVersion() })
  window.addEventListener('pagehide', () => { if (pendingBuild) persistUnsavedWork() })
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') void checkVersion()
    else maybeReload()
  })
  window.setInterval(() => { void checkVersion() }, CHECK_INTERVAL_MS)
  window.setInterval(maybeReload, 5_000)
  void checkVersion()
}
