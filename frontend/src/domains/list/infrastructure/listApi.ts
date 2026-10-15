import client from '@/shared/api/client'
import type { MastodonAccount, MastodonList, MastodonStatus, MastodonTag } from '@/shared/types/mastodon'
import type { PaginationParams } from '@/shared/types/pagination'

export type RepliesPolicy = 'followed' | 'list' | 'none'

export const listApi = {
  async getLists(): Promise<MastodonList[]> {
    const { data } = await client.get<MastodonList[]>('/api/v1/lists')
    return data
  },

  async getList(id: string): Promise<MastodonList> {
    const { data } = await client.get<MastodonList>(`/api/v1/lists/${encodeURIComponent(id)}`)
    return data
  },

  async createList(title: string, repliesPolicy: RepliesPolicy = 'list'): Promise<MastodonList> {
    const { data } = await client.post<MastodonList>('/api/v1/lists', { title, replies_policy: repliesPolicy })
    return data
  },

  async updateList(id: string, title: string, repliesPolicy?: RepliesPolicy): Promise<MastodonList> {
    const { data } = await client.put<MastodonList>(`/api/v1/lists/${encodeURIComponent(id)}`, {
      title,
      ...(repliesPolicy ? { replies_policy: repliesPolicy } : {}),
    })
    return data
  },

  async deleteList(id: string): Promise<void> {
    await client.delete(`/api/v1/lists/${encodeURIComponent(id)}`)
  },

  async listsContaining(accountId: string): Promise<MastodonList[]> {
    const { data } = await client.get<MastodonList[]>(`/api/v1/accounts/${encodeURIComponent(accountId)}/lists`)
    return data
  },

  async getAccounts(id: string): Promise<MastodonAccount[]> {
    // This endpoint returns all members when limit is omitted. Its internal
    // relationship cursor is not exposed, so a fixed limit hides members.
    const { data } = await client.get<MastodonAccount[]>(`/api/v1/lists/${encodeURIComponent(id)}/accounts`)
    return data
  },

  async addAccounts(id: string, accountIds: string[]): Promise<void> {
    await client.post(`/api/v1/lists/${encodeURIComponent(id)}/accounts`, { account_ids: accountIds })
  },

  async removeAccounts(id: string, accountIds: string[]): Promise<void> {
    await client.delete(`/api/v1/lists/${encodeURIComponent(id)}/accounts`, {
      params: { 'account_ids[]': accountIds },
    })
  },

  // Extension (not Mastodon): hashtags a list follows besides its members.
  async getTags(id: string): Promise<MastodonTag[]> {
    const { data } = await client.get<MastodonTag[]>(`/api/v1/lists/${encodeURIComponent(id)}/tags`)
    return data
  },

  async addTags(id: string, names: string[]): Promise<MastodonTag[]> {
    const { data } = await client.post<MastodonTag[]>(`/api/v1/lists/${encodeURIComponent(id)}/tags`, { names })
    return data
  },

  async removeTags(id: string, names: string[]): Promise<MastodonTag[]> {
    const { data } = await client.delete<MastodonTag[]>(`/api/v1/lists/${encodeURIComponent(id)}/tags`, { params: { 'names[]': names } })
    return data
  },

  /** Hashtag autocomplete via the regular search API. */
  async searchTags(q: string, signal?: AbortSignal): Promise<MastodonTag[]> {
    const { data } = await client.get<{ hashtags?: MastodonTag[] }>('/api/v2/search', { params: { q, type: 'hashtags', limit: 8, resolve: false }, signal })
    return data.hashtags ?? []
  },

  async getTimeline(id: string, params?: PaginationParams): Promise<MastodonStatus[]> {
    const { data } = await client.get<MastodonStatus[]>(`/api/v1/timelines/list/${encodeURIComponent(id)}`, { params })
    return data
  },
}
