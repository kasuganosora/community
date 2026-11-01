import client from '@/shared/api/client'

export interface Appeal {
  id: string
  account_id: string
  account_acct?: string
  status_id: string
  text: string
  state: string
  created_at: string
  resolved_at?: string
  admin_note?: string
}

export interface AppealContext {
  items: Appeal[]
  suspended: boolean
  suspension: { status_id: string; can_appeal: boolean } | null
}

export const appealApi = {
  async context(): Promise<AppealContext> {
    return (await client.get<AppealContext>('/api/v1/appeals', { params: { include_context: true } })).data
  },
  async list(): Promise<Appeal[]> {
    return (await client.get<Appeal[]>('/api/v1/appeals')).data
  },
  async create(text: string, statusId?: string): Promise<Appeal> {
    const body: { text: string; status_id?: string } = { text }
    if (statusId) body.status_id = statusId
    return (await client.post<Appeal>('/api/v1/appeals', body)).data
  },
}
