import client from '@/shared/api/client'
import type { Appeal } from '@/domains/auth/infrastructure/appealApi'

export type { Appeal }
export interface AppealListParams {
  state: string
  limit: number
  offset: number
}

const base = '/api/v1/admin/appeals'

export const appealAdminApi = {
  async list(params: AppealListParams): Promise<Appeal[]> {
    const query: Record<string, string | number> = { limit: params.limit, offset: params.offset }
    if (params.state) query.state = params.state
    return (await client.get<Appeal[]>(base, { params: query })).data
  },
  async resolve(id: string, accept: boolean, note: string): Promise<Appeal> {
    return (await client.post<Appeal>(`${base}/${encodeURIComponent(id)}/resolve`, { accept, note })).data
  },
}
