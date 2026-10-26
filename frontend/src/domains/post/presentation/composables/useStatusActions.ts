import { sessionEpoch } from '@/shared/api/sessionScope'
import { reactive, watch, onScopeDispose, getCurrentScope } from 'vue'
import { MessagePlugin, preloadFeedback } from '@/shared/ui/feedback'
import { t } from '@/shared/i18n'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { postApi } from '@/domains/post/infrastructure/postApi'

/**
 * Statuses deleted in this session. Cards hide themselves when their status
 * (or the status they reblog) is in here, so a deleted post disappears from
 * every list (timelines, profile, bookmarks, search) without each page
 * having to handle a delete event.
 */
export const deletedStatusIds = reactive(new Set<string>())

/** Latest version of statuses edited in this session, keyed by id. */
export const editedStatuses = reactive(new Map<string, MastodonStatus>())

watch(sessionEpoch, () => { deletedStatusIds.clear(); editedStatuses.clear() }, { flush: 'sync' })

export function markStatusDeleted(id: string) {
  deletedStatusIds.add(id)
}

export function markStatusEdited(status: MastodonStatus) {
  editedStatuses.set(status.id, status)
}

type StatusUpdater = (id: string, updates: Partial<MastodonStatus>) => void

/**
 * Apply a partial update to a status inside a flat list, handling both
 * top-level statuses and reblog wrappers transparently.
 *
 * exported so pages with local arrays can reuse the same lookup logic.
 */
export function applyStatusUpdate(
  list: MastodonStatus[],
  id: string,
  updates: Partial<MastodonStatus>,
): void {
  for (const item of list) {
    if (item.id === id) {
      Object.assign(item, updates)
    }
    if (item.reblog?.id === id) {
      Object.assign(item.reblog, updates)
    }
  }
}

type ToggleKind = 'fav' | 'reblog' | 'bm'

interface ToggleSpec {
  flag: 'favourited' | 'reblogged' | 'bookmarked'
  count?: 'favourites_count' | 'reblogs_count'
  on: (id: string) => Promise<MastodonStatus>
  off: (id: string) => Promise<MastodonStatus>
  errorKey: string
}

const SPECS: Record<ToggleKind, ToggleSpec> = {
  fav: { flag: 'favourited', count: 'favourites_count', on: id => postApi.favourite(id), off: id => postApi.unfavourite(id), errorKey: 'shared.PostActions.s15' },
  reblog: { flag: 'reblogged', count: 'reblogs_count', on: id => postApi.reblog(id), off: id => postApi.unreblog(id), errorKey: 'shared.PostActions.s16' },
  bm: { flag: 'bookmarked', on: id => postApi.bookmark(id), off: id => postApi.unbookmark(id), errorKey: 'shared.PostActions.s17' },
}

interface ToggleState {
  epoch: number
  /** What the user last asked for (what the UI shows). */
  desired: boolean
  /** Last state the server confirmed (rollback target). */
  confirmed: boolean
  confirmedCount: number | undefined
  inFlight: boolean
}

/** Per status+action toggle state, shared by every page so the same post stays consistent everywhere. */
const toggleStates = new Map<string, ToggleState>()
/** Every mounted state source (timeline store, thread lists, notifications…), so an action on one page updates all copies. */
const updaters = new Set<StatusUpdater>()
/** Keys currently awaiting the server (reactive, for callers that want to show it). */
export const pendingActions = reactive(new Set<string>())

interface StreamedRefresh {
  dirty: boolean
  updaters: Set<StatusUpdater>
  promise: Promise<void>
}
const streamedRefreshes = new Map<string, StreamedRefresh>()

watch(sessionEpoch, () => { toggleStates.clear(); pendingActions.clear(); streamedRefreshes.clear() }, { flush: 'sync' })

/** Fetch an edited visible object, including old posts outside the head page.
 * Share one flight per id across feeds and reconcile another edit arriving
 * during it. Never let an old account's response populate the new session. */
export function refreshStreamedStatus(id: string, update?: StatusUpdater): Promise<void> {
  const current = streamedRefreshes.get(id)
  if (current) {
    current.dirty = true
    if (update) current.updaters.add(update)
    return current.promise
  }
  const epoch = sessionEpoch.value
  const state: StreamedRefresh = { dirty: true, updaters: new Set(update ? [update] : []), promise: Promise.resolve() }
  streamedRefreshes.set(id, state)
  const active = () => sessionEpoch.value === epoch && streamedRefreshes.get(id) === state && !deletedStatusIds.has(id)
  state.promise = (async () => {
    while (state.dirty && active()) {
      state.dirty = false
      try {
        const status = await postApi.getStatus(id)
        if (!active()) return
        if (state.dirty) continue
        // An edit fetch must not undo a like/boost/bookmark still being sent.
        for (const [kind, spec] of Object.entries(SPECS)) {
          const pending = toggleStates.get(`${kind}:${id}`)
          if (!pending?.inFlight || pending.epoch !== epoch) continue
          status[spec.flag] = pending.desired
          if (spec.count && pending.confirmedCount !== undefined) {
            status[spec.count] = Math.max(0, pending.confirmedCount + (pending.desired === pending.confirmed ? 0 : pending.desired ? 1 : -1))
          }
        }
        markStatusEdited(status)
        for (const updater of new Set([...state.updaters, ...updaters])) updater(id, status)
      } catch {
        // Keep the current card on a network failure. A later edit/read retries.
      }
    }
  })().finally(() => { if (streamedRefreshes.get(id) === state) streamedRefreshes.delete(id) })
  return state.promise
}

function broadcast(own: StatusUpdater, id: string, updates: Partial<MastodonStatus>) {
  own(id, updates)
  for (const u of updaters) if (u !== own) u(id, updates)
  const cached = editedStatuses.get(id)
  if (cached) Object.assign(cached, updates)
}

function toastError(e: unknown, key: string) {
  console.error('Status action failed:', e)
  try { MessagePlugin.error(t(key)) } catch { /* toast is best effort */ }
}

/**
 * Shared post-action logic with optimistic UI.
 *
 * A click flips the flag and count at once on every copy of the status
 * (including reblog wrappers and other mounted pages). Requests per
 * status+action are serialized: clicks made while a request is in flight only
 * change the desired state, and once the request settles one more request is
 * sent if the server state differs from what the user ended on. On success the
 * server counts are written back only when nothing newer is queued, so there is
 * no flicker; on failure the UI returns to the last server-confirmed state and a
 * localized toast is shown.
 */
export function useStatusActions(updateStatus: StatusUpdater) {
  updaters.add(updateStatus)
  if (getCurrentScope()) onScopeDispose(() => updaters.delete(updateStatus))

  function toggle(kind: ToggleKind, status: MastodonStatus): Promise<void> {
    const spec = SPECS[kind]
    const id = status.id
    const key = `${kind}:${id}`
    const epoch = sessionEpoch.value
    let st = toggleStates.get(key)
    if (!st || st.epoch !== epoch) {
      st = { epoch, desired: !!status[spec.flag], confirmed: !!status[spec.flag], confirmedCount: spec.count ? status[spec.count] : undefined, inFlight: false }
      toggleStates.set(key, st)
    }
    const state = st
    const apply = (updates: Partial<MastodonStatus>) => {
      if (sessionEpoch.value !== epoch) return
      broadcast(updateStatus, id, updates)
    }
    const shownCount = (value: boolean) => spec.count && state.confirmedCount !== undefined
      ? Math.max(0, state.confirmedCount + (value === state.confirmed ? 0 : value ? 1 : -1))
      : undefined
    const optimistic = (value: boolean): Partial<MastodonStatus> => {
      const u: Partial<MastodonStatus> = { [spec.flag]: value }
      const c = shownCount(value)
      if (spec.count && c !== undefined) (u as Record<string, unknown>)[spec.count] = c
      return u
    }

    state.desired = !state.desired
    apply(optimistic(state.desired))
    if (state.inFlight) return Promise.resolve()

    state.inFlight = true
    // Fetch the toast code now so a failure can be reported without another round trip.
    try { preloadFeedback() } catch { /* best effort */ }
    pendingActions.add(key)
    const run = async (): Promise<void> => {
      while (state.desired !== state.confirmed) {
        const target = state.desired
        try {
          const updated = await (target ? spec.on(id) : spec.off(id))
          if (sessionEpoch.value !== epoch) return
          const fallbackCount = shownCount(target)
          state.confirmed = updated?.[spec.flag] ?? target
          if (spec.count && typeof updated?.[spec.count] === 'number') state.confirmedCount = updated[spec.count]
          else state.confirmedCount = fallbackCount ?? state.confirmedCount
          // Server reports a different state than requested: accept it.
          if (state.confirmed !== target && state.desired === target) state.desired = state.confirmed
        } catch (e) {
          if (sessionEpoch.value !== epoch) return
          state.desired = state.confirmed
          apply(optimistic(state.confirmed))
          toastError(e, spec.errorKey)
          return
        }
      }
      // Settled: reconcile with the server (same values as shown unless others also acted).
      apply(optimistic(state.confirmed))
    }
    return run().finally(() => {
      state.inFlight = false
      pendingActions.delete(key)
      if (toggleStates.get(key) === state && !state.inFlight) toggleStates.delete(key)
    })
  }

  return {
    pendingActions,
    toggleReblog: (s: MastodonStatus) => toggle('reblog', s),
    toggleFavourite: (s: MastodonStatus) => toggle('fav', s),
    toggleBookmark: (s: MastodonStatus) => toggle('bm', s),
  }
}
