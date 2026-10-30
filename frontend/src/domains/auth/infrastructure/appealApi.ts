import client from '@/shared/api/client'

export interface Appeal {
  id: string
  account_id: string
  status_id: string
  text: string
  state: string
  created_at: string
  resolved_at?: string
  admin_note?: string
}

export const appealApi = {
  async list(): Promise<Appeal[]> {
    return (await client.get<Appeal[]>('/api/v1/appeals')).data
  },
  async create(text: string, statusId?: string): Promise<Appeal> {
    const body: { text: string; status_id?: string } = { text }
    if (statusId) body.status_id = statusId
    return (await client.post<Appeal>('/api/v1/appeals', body)).data
  },
}
