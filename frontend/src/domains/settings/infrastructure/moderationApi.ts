import client from '@/shared/api/client'

export interface FilterKeyword {
  id?: string
  keyword: string
  whole_word: boolean
}

export interface MastodonFilter {
  id: string
  title: string
  context: string[]
  expires_at: string | null
  filter_action: 'warn' | 'hide' | 'none'
  keywords: FilterKeyword[]
}

export interface FilterDraft {
  title: string
  context: string[]
  filter_action: 'warn' | 'hide'
  keywords: { keyword: string; whole_word: boolean }[]
}

export const moderationApi = {
  async listFilters(): Promise<MastodonFilter[]> {
    const { data } = await client.get<MastodonFilter[]>('/api/v1/filters')
    return data
  },

  async createFilter(draft: FilterDraft): Promise<MastodonFilter> {
    const { data } = await client.post<MastodonFilter>('/api/v1/filters', draft)
    return data
  },

  async deleteFilter(id: string): Promise<void> {
    await client.delete(`/api/v1/filters/${encodeURIComponent(id)}`)
  },

  async listDomainBlocks(): Promise<string[]> {
    const { data } = await client.get<string[]>('/api/v1/domain_blocks')
    return data
  },

  async blockDomain(domain: string): Promise<void> {
    await client.post('/api/v1/domain_blocks', { domain })
  },

  async unblockDomain(domain: string): Promise<void> {
    await client.delete('/api/v1/domain_blocks', { data: { domain } })
  },
}
