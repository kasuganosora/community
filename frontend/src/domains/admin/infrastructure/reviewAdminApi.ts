import client from '@/shared/api/client'

export interface ReviewJob {
  id: string
  account_id: string
  status_id: string
  acct: string
  display_name: string
  text: string
  held: boolean
  state: string
  verdict: string
  reason: string
  last_error: string
  attempts: number
  created_at: string
  actionable: boolean
}
export interface ReviewPage {
  items: ReviewJob[]
  has_more: boolean
  enabled: boolean
  configured: boolean
}
export const reviewAdminApi = {
  async list(before?: string): Promise<ReviewPage> {
    return (await client.get<ReviewPage>('/api/v1/admin/new-account-reviews', { params: { before } })).data
  },
  async act(id: string, action: 'approve' | 'retry'): Promise<void> {
    await client.post(`/api/v1/admin/new-account-reviews/${encodeURIComponent(id)}`, { action })
  },
}
