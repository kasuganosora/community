import client from '@/shared/api/client'
import type { MastodonAccount, MastodonList, MastodonStatus } from '@/shared/types/mastodon'
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

  async getAccounts(id: string): Promise<MastodonAccount[]> {
    const { data } = await client.get<MastodonAccount[]>(`/api/v1/lists/${encodeURIComponent(id)}/accounts`, {
      params: { limit: 80 },
    })
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

  async getTimeline(id: string, params?: PaginationParams): Promise<MastodonStatus[]> {
    const { data } = await client.get<MastodonStatus[]>(`/api/v1/timelines/list/${encodeURIComponent(id)}`, { params })
    return data
  },
}
