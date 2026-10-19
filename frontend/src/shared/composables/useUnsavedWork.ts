import { getCurrentInstance, onBeforeUnmount, onMounted } from 'vue'

/**
 * Registry of in-progress user work (composer text, attachments, uploads)
 * so app-level code such as the deploy auto-reload can tell whether a reload
 * would interrupt someone, and persist drafts to localStorage first.
 */
export interface UnsavedWork {
  /** True while there is unsent text/attachments or an upload in flight. */
  busy: () => boolean
  /** Serializable draft to keep across a reload, or null when empty. */
  snapshot?: () => unknown
}

const PREFIX = 'community:draft:'
const registry = new Map<string, UnsavedWork>()

export function registerUnsavedWork(key: string, work: UnsavedWork): () => void {
  registry.set(key, work)
  return () => { if (registry.get(key) === work) registry.delete(key) }
}

function modalOpen(): boolean {
  if (typeof document === 'undefined') return false
  if (document.querySelector('dialog[open]')) return true
  return [...document.querySelectorAll<HTMLElement>('[role="dialog"], [aria-modal="true"], .t-dialog__ctx')]
    .some(el => el.offsetParent !== null || el.getClientRects().length > 0)
}

/** Whether reloading right now would interrupt the user. */
export function hasUnsavedWork(): boolean {
  for (const work of registry.values()) {
    try { if (work.busy()) return true } catch { return true }
  }
  return modalOpen()
}

/** Writes every non-empty draft to localStorage. */
export function persistUnsavedWork(): void {
  for (const [key, work] of registry) {
    try {
      const value = work.snapshot?.()
      if (value == null) localStorage.removeItem(PREFIX + key)
      else localStorage.setItem(PREFIX + key, JSON.stringify({ at: Date.now(), value }))
    } catch { /* storage full or disabled: nothing else to do */ }
  }
}

/** Returns (and forgets) a draft persisted before a reload; drafts older than a day are dropped. */
export function takePersistedDraft<T>(key: string): T | null {
  try {
    const raw = localStorage.getItem(PREFIX + key)
    if (!raw) return null
    localStorage.removeItem(PREFIX + key)
    const parsed = JSON.parse(raw) as { at?: number; value?: T }
    if (!parsed || typeof parsed.at !== 'number' || Date.now() - parsed.at > 86_400_000) return null
    return parsed.value ?? null
  } catch {
    return null
  }
}

/**
 * Component helper: restores a persisted draft on mount and keeps the
 * component registered while mounted. `key` is read at mount time.
 */
export function useUnsavedWork<T>(key: () => string | null, work: { busy: () => boolean; snapshot: () => T | null; restore: (value: T) => void }) {
  if (!getCurrentInstance()) return
  let stop: (() => void) | undefined
  onMounted(() => {
    const k = key()
    if (!k) return
    const saved = takePersistedDraft<T>(k)
    if (saved != null) work.restore(saved)
    stop = registerUnsavedWork(k, work)
  })
  onBeforeUnmount(() => stop?.())
}

export function _resetUnsavedWorkForTests() { registry.clear() }
