import { reactive } from 'vue'
import type { MastodonRelationship } from '@/shared/types/mastodon'
import { userApi } from '../../infrastructure/userApi'
import { followStateOf, type FollowState } from '../../domain/relationship'

/**
 * Relationship cache for a page listing accounts. Loads relationships in
 * one batched request and toggles follow with optimistic updates, so follow
 * buttons show the real state instead of always "Follow".
 */
export function useRelationships() {
  const rels = reactive(new Map<string, MastodonRelationship>())
  const pending = reactive(new Set<string>())

  async function load(ids: string[]) {
    const missing = [...new Set(ids)].filter(id => !rels.has(id))
    if (!missing.length) return
    try {
      const data = await userApi.getRelationships(missing)
      for (const r of data) rels.set(r.id, r)
    } catch (e) {
      console.error('Failed to load relationships:', e)
    }
  }

  function state(id: string): FollowState {
    return followStateOf(rels.get(id))
  }

  async function toggleFollow(id: string) {
    if (pending.has(id)) return
    const before = rels.get(id)
    const wasFollowing = !!before && (before.following || before.requested)
    pending.add(id)
    if (before) rels.set(id, { ...before, following: !wasFollowing, requested: false })
    try {
      const rel = wasFollowing ? await userApi.unfollow(id) : await userApi.follow(id)
      rels.set(id, rel)
    } catch (e) {
      if (before) rels.set(id, before)
      throw e
    } finally {
      pending.delete(id)
    }
  }

  return { rels, pending, load, state, toggleFollow }
}
