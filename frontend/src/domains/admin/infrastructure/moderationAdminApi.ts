import client from '@/shared/api/client'
import type { MastodonAccount, MastodonStatus } from '@/shared/types/mastodon'

export interface AdminAccount {
  id: string
  username: string
  domain: string | null
  display_name: string
  suspended: boolean
  disabled?: boolean
  user_status?: number
  role?: string
  locked: boolean
  created_at?: string
  avatar?: string
  avatar_static?: string
  avatar_default?: string
  avatar_decorations?: MastodonAccount['avatar_decorations']
  avatar_decoration_url?: string
  avatar_decoration_id?: string
  account?: MastodonAccount
}
export interface AdminReport {
  id: string
  category: string
  comment: string
  action_taken: boolean
  created_at?: string
  account: MastodonAccount | null
  target_account: MastodonAccount | null
  assigned_account: MastodonAccount | null
  action_taken_by_account?: MastodonAccount | null
  status_ids: string[]
  statuses?: MastodonStatus[]
  rule_ids?: string[]
}
export type AccountAction = 'disable' | 'enable' | 'suspend' | 'unsuspend' | 'approve' | 'reject'
export type ReportAction = 'assign_to_self' | 'unassign' | 'resolve' | 'reopen'
export interface AccountFilters {
  origin: string
  status: string
  username: string
  display_name: string
  offset: number
  limit: number
}
export interface ReportFilters {
  resolved: string
  account_id: string
  target_account_id: string
  max_id?: string
  limit: number
}
const base = '/api/v1/admin'
function nonempty<T extends object>(params: T) {
  return Object.fromEntries(Object.entries(params).filter(([, value]) => value !== '' && value !== undefined))
}
export const moderationAdminApi = {
  async accounts(params: AccountFilters) {
    const response = await client.get<AdminAccount[]>(`${base}/accounts`, { params: nonempty(params) })
    const total = Number(response.headers['x-total-count'])
    return { items: response.data, total: Number.isFinite(total) ? total : null }
  },
  async account(id: string) {
    return (await client.get<AdminAccount>(`${base}/accounts/${encodeURIComponent(id)}`)).data
  },
  async accountTwoFactor(id: string) {
    return (await client.get<{ enabled: boolean; enabled_at: string | null; backup_codes_remaining: number }>(`${base}/accounts/${encodeURIComponent(id)}/two_factor`)).data
  },
  async resetAccountTwoFactor(id: string) {
    await client.post(`${base}/accounts/${encodeURIComponent(id)}/two_factor/reset`)
  },
  async accountAction(id: string, action: AccountAction) {
    const url = `${base}/accounts/${encodeURIComponent(id)}`
    if (action === 'disable' || action === 'suspend') await client.post(`${url}/action`, { type: action })
    else await client.post(`${url}/${action}`)
  },
  async reports(params: ReportFilters) {
    return (await client.get<AdminReport[]>(`${base}/reports/`, { params: nonempty(params) })).data
  },
  async report(id: string) {
    return (await client.get<AdminReport>(`${base}/reports/${encodeURIComponent(id)}`)).data
  },
  async deleteStatus(id: string) {
    await client.delete(`${base}/statuses/${encodeURIComponent(id)}`)
  },
  async reportAction(id: string, action: ReportAction) {
    await client.post(`${base}/reports/${encodeURIComponent(id)}/${action}`)
  },
}
