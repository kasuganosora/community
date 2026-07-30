import client from '@/shared/api/client'
import type { MastodonAccount, MastodonStatus, MastodonRelationship } from '@/shared/types/mastodon'

export const userApi = {
  async getAccount(id: string): Promise<MastodonAccount> {
    const { data } = await client.get<MastodonAccount>(`/api/v1/accounts/${id}`)
    return data
  },

  async lookupAccount(acct: string): Promise<MastodonAccount> {
    // Backend uses /api/v1/accounts/search?q=acct rather than Mastodon's /accounts/lookup
    const { data } = await client.get<MastodonAccount[]>('/api/v1/accounts/search', {
      params: { q: acct, limit: 1 },
    })
    if (!data[0]) throw new Error('Account not found')
    return data[0]
  },

  async getStatuses(id: string, params?: {
    exclude_replies?: boolean
    only_media?: boolean
    pinned?: boolean
    max_id?: string
    limit?: number
  }): Promise<MastodonStatus[]> {
    const { data } = await client.get<MastodonStatus[]>(`/api/v1/accounts/${id}/statuses`, { params })
    return data
  },

  async getRelationships(ids: string[]): Promise<MastodonRelationship[]> {
    const { data } = await client.get<MastodonRelationship[]>('/api/v1/accounts/relationships', {
      params: { 'id[]': ids },
    })
    return data
  },

  async follow(id: string): Promise<MastodonRelationship> {
    const { data } = await client.post<MastodonRelationship>(`/api/v1/accounts/${id}/follow`)
    return data
  },

  async unfollow(id: string): Promise<MastodonRelationship> {
    const { data } = await client.delete<MastodonRelationship>(`/api/v1/accounts/${id}/follow`)
    return data
  },

  async getFollowers(id: string, params?: { max_id?: string; limit?: number }): Promise<MastodonAccount[]> {
    const { data } = await client.get<MastodonAccount[]>(`/api/v1/accounts/${id}/followers`, { params })
    return data
  },

  async getFollowing(id: string, params?: { max_id?: string; limit?: number }): Promise<MastodonAccount[]> {
    const { data } = await client.get<MastodonAccount[]>(`/api/v1/accounts/${id}/following`, { params })
    return data
  },
}
