import { sessionEpoch } from '@/shared/api/sessionScope'
import { ref, reactive, watch } from 'vue'
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

/**
 * Shared post-action logic: optimistic update + API call + revert on failure.
 *
 * The `updateStatus` callback is responsible for writing changes back to
 * whatever state source the caller uses (Pinia store, local ref, etc.).
 * This eliminates the ~120 lines of duplicated action handlers that were
 * previously copy-pasted across HomePage and ExplorePage.
 */
export function useStatusActions(updateStatus: StatusUpdater) {
  function scopedUpdater() {
    const epoch = sessionEpoch.value
    return (id: string, updates: Partial<MastodonStatus>) => {
      if (sessionEpoch.value !== epoch) return
      updateStatus(id, updates)
      const cached = editedStatuses.get(id)
      if (cached) Object.assign(cached, updates)
    }
  }
  // Track which status actions are currently in-flight to prevent
  // duplicate toggles from rapid clicks causing state desync.
  const pendingActions = ref(new Set<string>())

  async function toggleReblog(status: MastodonStatus) {
    const update = scopedUpdater()
    const actionKey = `reblog:${status.id}`
    if (pendingActions.value.has(actionKey)) return
    pendingActions.value.add(actionKey)

    const wasReblogged = status.reblogged
    // Optimistic update for immediate UI feedback
    update(status.id, { reblogged: !wasReblogged })
    try {
      const updated = wasReblogged
        ? await postApi.unreblog(status.id)
        : await postApi.reblog(status.id)
      update(status.id, { reblogged: updated.reblogged, reblogs_count: updated.reblogs_count })
    } catch (e) {
      // Revert optimistic update on failure
      update(status.id, { reblogged: wasReblogged })
      console.error('Failed to toggle reblog:', e)
    } finally {
      pendingActions.value.delete(actionKey)
    }
  }

  async function toggleFavourite(status: MastodonStatus) {
    const update = scopedUpdater()
    const actionKey = `fav:${status.id}`
    if (pendingActions.value.has(actionKey)) return
    pendingActions.value.add(actionKey)

    const wasFavourited = status.favourited
    // Optimistic update for immediate UI feedback
    update(status.id, { favourited: !wasFavourited })
    try {
      const updated = wasFavourited
        ? await postApi.unfavourite(status.id)
        : await postApi.favourite(status.id)
      update(status.id, { favourited: updated.favourited, favourites_count: updated.favourites_count })
    } catch (e) {
      // Revert optimistic update on failure
      update(status.id, { favourited: wasFavourited })
      console.error('Failed to toggle favourite:', e)
    } finally {
      pendingActions.value.delete(actionKey)
    }
  }

  async function toggleBookmark(status: MastodonStatus) {
    const update = scopedUpdater()
    const actionKey = `bm:${status.id}`
    if (pendingActions.value.has(actionKey)) return
    pendingActions.value.add(actionKey)

    const wasBookmarked = status.bookmarked
    // Optimistic update for immediate UI feedback
    update(status.id, { bookmarked: !wasBookmarked })
    try {
      const updated = wasBookmarked
        ? await postApi.unbookmark(status.id)
        : await postApi.bookmark(status.id)
      update(status.id, { bookmarked: updated.bookmarked })
    } catch (e) {
      // Revert optimistic update on failure
      update(status.id, { bookmarked: wasBookmarked })
      console.error('Failed to toggle bookmark:', e)
    } finally {
      pendingActions.value.delete(actionKey)
    }
  }

  return { pendingActions, toggleReblog, toggleFavourite, toggleBookmark }
}
