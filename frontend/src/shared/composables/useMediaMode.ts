import { ref, readonly } from 'vue'
import client from '@/shared/api/client'
import type { MastodonAttachment, MastodonStatus } from '@/shared/types/mastodon'

/**
 * How sensitive media is shown, Mastodon's "reading:expand:media":
 * default = follow each post's sensitive flag, show_all = never hide,
 * hide_all = always hide until clicked. Kept in localStorage so guests and
 * first paint work, and mirrored to the account via /api/v1/preferences.
 */
export type MediaMode = 'default' | 'show_all' | 'hide_all'

const KEY = 'media-mode'
const MODES: MediaMode[] = ['default', 'show_all', 'hide_all']

function read(): MediaMode {
  try {
    const v = localStorage.getItem(KEY) as MediaMode | null
    return v && MODES.includes(v) ? v : 'default'
  } catch {
    return 'default'
  }
}

const mode = ref<MediaMode>(read())

export function useMediaMode() {
  function store(next: MediaMode) {
    mode.value = next
    try { localStorage.setItem(KEY, next) } catch { /* private mode */ }
  }

  /** Change locally and on the account. */
  async function setMode(next: MediaMode) {
    store(next)
    try { await client.patch('/api/v1/preferences', { 'reading:expand:media': next }) } catch { /* local choice still applies */ }
  }

  /** Adopt the account's saved choice (call when signed in). */
  async function loadMode() {
    try {
      const { data } = await client.get<Record<string, unknown>>('/api/v1/preferences')
      const v = data?.['reading:expand:media']
      if (typeof v === 'string' && MODES.includes(v as MediaMode)) store(v as MediaMode)
    } catch { /* keep the local value */ }
  }

  return { mode: readonly(mode), setMode, loadMode }
}

/** Whether a status' media must stay hidden until the viewer clicks. */
export function mediaGated(status: Pick<MastodonStatus, 'sensitive'>, mediaMode: MediaMode = mode.value): boolean {
  if (mediaMode === 'show_all') return false
  if (mediaMode === 'hide_all') return true
  return !!status.sensitive
}

/** width / height of an attachment when the server knows it. */
export function attachmentAspect(a: Pick<MastodonAttachment, 'meta'>): number | null {
  const o = a.meta?.original
  const v = o?.aspect ?? (o?.width && o?.height ? o.width / o.height : undefined) ?? a.meta?.small?.aspect
  return v && v > 0 ? v : null
}
