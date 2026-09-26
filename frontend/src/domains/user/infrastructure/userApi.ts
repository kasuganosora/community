import client from '@/shared/api/client'
import type { MastodonAccount, MastodonStatus, MastodonRelationship } from '@/shared/types/mastodon'
import type { PaginationParams } from '@/shared/types/pagination'
import { pickAccountByAcct } from '../domain/acct'

export const userApi = {
  async getAccount(id: string): Promise<MastodonAccount> {
    const { data } = await client.get<MastodonAccount>(`/api/v1/accounts/${encodeURIComponent(id)}`)
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
        params: { q: term, limit: 40 },
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

  async getFollowers(id: string, params?: Pick<PaginationParams, 'max_id' | 'limit'>): Promise<MastodonAccount[]> {
    const { data } = await client.get<MastodonAccount[]>(`/api/v1/accounts/${encodeURIComponent(id)}/followers`, { params })
    return data
  },

  async getFollowing(id: string, params?: Pick<PaginationParams, 'max_id' | 'limit'>): Promise<MastodonAccount[]> {
    const { data } = await client.get<MastodonAccount[]>(`/api/v1/accounts/${encodeURIComponent(id)}/following`, { params })
    return data
  },
}
