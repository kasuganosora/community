import { cursorPage, type CursorPage } from '@/shared/utils/pagination'
import client, { tokenService } from '@/shared/api/client'
import type { MastodonAccount, MastodonStatus, MastodonRelationship } from '@/shared/types/mastodon'
import type { PaginationParams } from '@/shared/types/pagination'
import { pickAccountByAcct } from '../domain/acct'

import { t } from '@/shared/i18n'
export type ProfileBackfillState = 'pending' | 'done' | 'unavailable' | null

export type AccountStatusesParams = {
  exclude_replies?: boolean
  only_media?: boolean
  pinned?: boolean
} & PaginationParams

export function parseBackfillState(value: unknown): ProfileBackfillState {
  return value === 'pending' || value === 'done' || value === 'unavailable' ? value : null
}

function isNotFound(e: unknown): boolean {
  return (e as { response?: { status?: number } } | null)?.response?.status === 404
}

export const userApi = {
  // Hovering must never trigger a federated account resolution/backfill.
  async lookupCachedAccount(acct: string, signal?: AbortSignal): Promise<MastodonAccount> {
    const { data } = await client.get<MastodonAccount>('/api/v1/accounts/lookup', { params: { acct }, signal })
    return data
  },

  async getAccount(id: string): Promise<MastodonAccount> {
    const { data } = await client.get<MastodonAccount>(`/api/v1/accounts/${encodeURIComponent(id)}`)
    return data
  },

  async searchAccounts(q: string, limit = 8): Promise<MastodonAccount[]> {
    const { data } = await client.get<MastodonAccount[]>('/api/v1/accounts/search', {
      params: { q, limit, resolve: false },
    })
    return data
  },

  /**
   * Resolve a profile route (/@user or /@user@domain) to exactly that account.
   *
   * Uses /api/v1/accounts/lookup, an exact username + domain match, so a
   * remote "luna@maid.lat" can never land on the local "luna". When a remote
   * account is not cached yet, a signed-in user asks the server to fetch it
   * (accounts/search with resolve=true, which only resolves for signed-in
   * users) and still only accepts the exact acct.
   */
  async lookupAccount(acct: string): Promise<MastodonAccount> {
    const q = acct.trim().replace(/^@/, '')
    if (!q) throw new Error(t('user.userApi.s1'))
    try {
      const { data } = await client.get<MastodonAccount>('/api/v1/accounts/lookup', { params: { acct: q } })
      return data
    } catch (e) {
      if (!isNotFound(e)) throw e
    }
    if (q.includes('@') && tokenService.get()) {
      const { data } = await client.get<MastodonAccount[]>('/api/v1/accounts/search', {
        params: { q, limit: 5, resolve: true },
      })
      const match = pickAccountByAcct(data, q, window.location.hostname)
      if (match) return match
    }
    throw new Error(t('user.userApi.s1'))
  },

  async getStatuses(id: string, params?: AccountStatusesParams): Promise<MastodonStatus[]> {
    return (await this.getStatusesWithBackfill(id, params)).statuses
  },

  /**
   * Account statuses plus the server's remote profile backfill state
   * (X-Profile-Backfill): "pending" while older posts are still being
   * imported from the remote server, "unavailable" when it hides them.
   */
  async getStatusesWithBackfill(id: string, params?: AccountStatusesParams): Promise<{ statuses: MastodonStatus[]; backfill: ProfileBackfillState }> {
    const { data, headers } = await client.get<MastodonStatus[]>(`/api/v1/accounts/${encodeURIComponent(id)}/statuses`, { params })
    return { statuses: data, backfill: parseBackfillState(headers?.['x-profile-backfill']) }
  },

  async getRelationships(ids: string[], signal?: AbortSignal): Promise<MastodonRelationship[]> {
    const { data } = await client.get<MastodonRelationship[]>('/api/v1/accounts/relationships', {
      params: { 'id[]': ids }, signal,
    })
    return data
  },

  async follow(id: string): Promise<MastodonRelationship> {
    const { data } = await client.post<MastodonRelationship>(`/api/v1/accounts/${encodeURIComponent(id)}/follow`)
    return data
  },

  async unfollow(id: string): Promise<MastodonRelationship> {
    const { data } = await client.delete<MastodonRelationship>(`/api/v1/accounts/${encodeURIComponent(id)}/follow`)
    return data
  },

  async block(id: string): Promise<MastodonRelationship> {
    const { data } = await client.post<MastodonRelationship>(`/api/v1/accounts/${encodeURIComponent(id)}/block`)
    return data
  },

  async unblock(id: string): Promise<MastodonRelationship> {
    const { data } = await client.post<MastodonRelationship>(`/api/v1/accounts/${encodeURIComponent(id)}/unblock`)
    return data
  },

  async mute(id: string): Promise<MastodonRelationship> {
    const { data } = await client.post<MastodonRelationship>(`/api/v1/accounts/${encodeURIComponent(id)}/mute`)
    return data
  },

  async unmute(id: string): Promise<MastodonRelationship> {
    const { data } = await client.post<MastodonRelationship>(`/api/v1/accounts/${encodeURIComponent(id)}/unmute`)
    return data
  },

  async report(accountId: string, comment: string, category: string, statusIds: string[] = []): Promise<void> {
    await client.post('/api/v1/reports', {
      account_id: accountId,
      status_ids: statusIds,
      comment,
      category,
      forward: false,
    })
  },

  async getFollowers(id: string, params?: Pick<PaginationParams, 'max_id' | 'limit'>): Promise<MastodonAccount[]> {
    const { data } = await client.get<MastodonAccount[]>(`/api/v1/accounts/${encodeURIComponent(id)}/followers`, { params })
    return data
  },

  async getFollowing(id: string, params?: Pick<PaginationParams, 'max_id' | 'limit'>): Promise<MastodonAccount[]> {
    const { data } = await client.get<MastodonAccount[]>(`/api/v1/accounts/${encodeURIComponent(id)}/following`, { params })
    return data
  },

  async getFollowPage(id: string, kind: 'followers' | 'following', params?: Pick<PaginationParams, 'max_id' | 'limit'>): Promise<CursorPage<MastodonAccount>> {
    const { data, headers } = await client.get<MastodonAccount[]>(`/api/v1/accounts/${encodeURIComponent(id)}/${kind}`, { params })
    return cursorPage(data, headers.link)
  },

  async getAllFollowing(id: string): Promise<MastodonAccount[]> {
    const accounts: MastodonAccount[] = []
    const seen = new Set<string>()
    let cursor: string | undefined
    do {
      const page = await this.getFollowPage(id, 'following', { limit: 80, max_id: cursor })
      accounts.push(...page.items)
      cursor = page.nextCursor
      if (cursor && seen.has(cursor)) break
      if (cursor) seen.add(cursor)
    } while (cursor)
    return accounts
  },

  /** Statuses the current user has liked (only available for yourself). */
  async getFavourites(params?: Pick<PaginationParams, 'max_id' | 'limit'>): Promise<CursorPage<MastodonStatus>> {
    const { data, headers } = await client.get<MastodonStatus[]>('/api/v1/favourites', { params })
    return cursorPage(data, headers.link)
  },

  /** JSON retains ordered profile-field objects, including an empty array. */
  async updateCredentials(fields: UpdateCredentialsParams): Promise<MastodonAccount> {
    const { data } = await client.patch<MastodonAccount>('/api/v1/accounts/update_credentials', fields)
    return data
  },

  async changePassword(oldPassword: string, newPassword: string, otp = ''): Promise<void> {
    const body = new URLSearchParams({ old_password: oldPassword, new_password: newPassword })
    if (otp) body.set('otp_attempt', otp)
    await client.put('/api/v1/accounts/me/password', body, {
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    })
  },
}

export interface UpdateCredentialsParams {
  display_name?: string
  note?: string
  /** URL of an uploaded media attachment (the backend has no file upload here). */
  avatar?: string
  header?: string
  locked?: boolean
  bot?: boolean
  is_cat?: boolean
  require_signin_to_view?: boolean
  location?: string
  birthday?: string
  language?: string
  followed_message?: string
  reaction_acceptance?: import('@/shared/types/mastodon').ReactionAcceptance
  fields_attributes?: { name: string; value: string }[]
  discoverable?: boolean
  indexable?: boolean
}
