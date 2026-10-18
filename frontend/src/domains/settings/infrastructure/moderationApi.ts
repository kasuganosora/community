import client from '@/shared/api/client'
import type { MastodonAccount } from '@/shared/types/mastodon'

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

/** One entry of Mastodon's keywords_attributes[]. */
export interface KeywordAttribute {
  id?: string
  keyword?: string
  whole_word?: boolean
  _destroy?: boolean
}

export interface FilterDraft {
  title: string
  context: string[]
  filter_action: 'warn' | 'hide'
  keywords_attributes: KeywordAttribute[]
}

export type RelationOrder = 'newest' | 'oldest'

export interface RelationPage<T> {
  items: T[]
  /** Absolute or root-relative URL of the next page, from the Link header. */
  next?: string
}

/** Pull rel="next" out of a Link header, keeping the path and query only. */
export function nextLink(link: string | undefined | null): string | undefined {
  for (const part of (link ?? '').split(',')) {
    const match = part.match(/<([^>]+)>;\s*rel="?next"?/)
    if (match?.[1]) {
      const url = new URL(match[1], 'http://link.local')
      return url.pathname + url.search
    }
  }
  return undefined
}

async function page<T>(path: string, order: RelationOrder, next?: string): Promise<RelationPage<T>> {
  const response = next
    ? await client.get<T[]>(next)
    : await client.get<T[]>(path, { params: { limit: 40, ...(order === 'oldest' ? { order: 'asc' } : {}) } })
  const link = (response.headers as Record<string, string | undefined>)?.link
  return { items: response.data, next: nextLink(link) }
}

export type AccountListKind = 'hidden_reblogs' | 'mutes' | 'blocks'

const accountListPath: Record<AccountListKind, string> = {
  hidden_reblogs: '/api/v1/hidden_reblogs',
  mutes: '/api/v1/mutes',
  blocks: '/api/v1/blocks',
}

export const moderationApi = {
  async listFilters(): Promise<MastodonFilter[]> {
    const { data } = await client.get<MastodonFilter[]>('/api/v2/filters')
    return data
  },

  async createFilter(draft: FilterDraft): Promise<MastodonFilter> {
    const { data } = await client.post<MastodonFilter>('/api/v2/filters', draft)
    return data
  },

  async updateFilter(id: string, draft: FilterDraft): Promise<MastodonFilter> {
    const { data } = await client.put<MastodonFilter>(`/api/v2/filters/${encodeURIComponent(id)}`, draft)
    return data
  },

  async deleteFilter(id: string): Promise<void> {
    await client.delete(`/api/v2/filters/${encodeURIComponent(id)}`)
  },

  listDomainBlocks(order: RelationOrder = 'newest', next?: string): Promise<RelationPage<string>> {
    return page<string>('/api/v1/domain_blocks', order, next)
  },

  async blockDomain(domain: string): Promise<void> {
    await client.post('/api/v1/domain_blocks', { domain })
  },

  async unblockDomain(domain: string): Promise<void> {
    await client.delete('/api/v1/domain_blocks', { data: { domain } })
  },

  listAccounts(kind: AccountListKind, order: RelationOrder = 'newest', next?: string): Promise<RelationPage<MastodonAccount>> {
    return page<MastodonAccount>(accountListPath[kind], order, next)
  },

  /** Undo the relation the list is about: show reblogs again, unmute, or unblock. */
  async undo(kind: AccountListKind, id: string): Promise<void> {
    const target = encodeURIComponent(id)
    if (kind === 'hidden_reblogs') await client.post(`/api/v1/accounts/${target}/follow`, { reblogs: true })
    else if (kind === 'mutes') await client.post(`/api/v1/accounts/${target}/unmute`)
    else await client.post(`/api/v1/accounts/${target}/unblock`)
  },
}
