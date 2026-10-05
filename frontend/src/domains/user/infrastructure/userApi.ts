import { cursorPage, type CursorPage } from '@/shared/utils/pagination'
import client, { tokenService } from '@/shared/api/client'
import type { MastodonAccount, MastodonStatus, MastodonRelationship } from '@/shared/types/mastodon'
import type { PaginationParams } from '@/shared/types/pagination'
import { pickAccountByAcct } from '../domain/acct'

export const userApi = {
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

  async lookupAccount(acct: string): Promise<MastodonAccount> {
    // Backend has no /accounts/lookup; /accounts/search is a prefix search,
    // so pick the exact match instead of the first hit. Local accounts
    // currently come back as "user@domain", which the search does not
    // match, so retry with the bare username.
    const q = acct.replace(/^@/, '')
    const search = async (term: string) => {
      const { data } = await client.get<MastodonAccount[]>('/api/v1/accounts/search', {
        params: { q: term, limit: 40, resolve: !!tokenService.get() },
      })
      return data
    }
    let match = pickAccountByAcct(await search(q), q, window.location.hostname)
    if (!match && q.includes('@')) {
      match = pickAccountByAcct(await search(q.split('@')[0] ?? q), q, window.location.hostname)
    }
    if (!match) throw new Error('用户不存在')
    return match
  },

  async getStatuses(id: string, params?: {
    exclude_replies?: boolean
    only_media?: boolean
    pinned?: boolean
  } & PaginationParams): Promise<MastodonStatus[]> {
    const { data } = await client.get<MastodonStatus[]>(`/api/v1/accounts/${encodeURIComponent(id)}/statuses`, { params })
    return data
  },

  async getRelationships(ids: string[]): Promise<MastodonRelationship[]> {
    const { data } = await client.get<MastodonRelationship[]>('/api/v1/accounts/relationships', {
      params: { 'id[]': ids },
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

  /**
   * PATCH /api/v1/accounts/update_credentials. The backend binds form fields
   * (not JSON), so send application/x-www-form-urlencoded like Mastodon clients.
   */
  async updateCredentials(fields: UpdateCredentialsParams): Promise<MastodonAccount> {
    const body = new URLSearchParams()
    for (const [k, v] of Object.entries(fields)) {
      if (v === undefined || v === null) continue
      body.append(k, typeof v === 'boolean' ? String(v) : v)
    }
    const { data } = await client.patch<MastodonAccount>('/api/v1/accounts/update_credentials', body, {
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    })
    return data
  },

  async changePassword(oldPassword: string, newPassword: string): Promise<void> {
    const body = new URLSearchParams({ old_password: oldPassword, new_password: newPassword })
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
  discoverable?: boolean
}
