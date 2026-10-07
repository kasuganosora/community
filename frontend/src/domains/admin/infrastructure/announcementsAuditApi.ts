import client from '@/shared/api/client'
export interface Announcement {
  id: string
  content: string
  published: boolean
  all_day: boolean
  scheduled_at: string | null
  starts_at: string | null
  ends_at: string | null
  updated_at: string
  read?: boolean
  text?: string
}
export interface AnnouncementInput {
  content: string
  all_day: boolean
  scheduled_at: string | null
  starts_at: string | null
  ends_at: string | null
}
export interface AuditLog {
  id: string
  actor_id: string
  account_id: string
  action: string
  method: string
  route: string
  target_id: string
  outcome: 'pending' | 'success' | 'failure'
  status: number
  duration_ms: number
  created_at: string
}
export const announcementsAuditApi = {
  async logs(filters: Record<string, string>) {
    return (await client.get<AuditLog[]>('/api/v1/admin/audit-logs', { params: Object.fromEntries(Object.entries(filters).filter(([, v]) => v !== '')) })).data
  },
  async list(params: { published?: string; max_id?: string } = {}) {
    return (await client.get<Announcement[]>('/api/v1/admin/announcements', { params })).data
  },
  async save(input: AnnouncementInput, id?: string) {
    if (id) return (await client.put<Announcement>(`/api/v1/admin/announcements/${encodeURIComponent(id)}`, input)).data
    return (await client.post<Announcement>('/api/v1/admin/announcements', input)).data
  },
  async publish(id: string, published: boolean) {
    return (await client.post<Announcement>(`/api/v1/admin/announcements/${encodeURIComponent(id)}/${published ? 'publish' : 'unpublish'}`)).data
  },
  async remove(id: string) { await client.delete(`/api/v1/admin/announcements/${encodeURIComponent(id)}`) },
  async visible(unreadOnly = false) {
    return (await client.get<Announcement[]>('/api/v1/announcements', { params: { with_dismissed: !unreadOnly } })).data
  },
  async dismiss(id: string) { await client.post(`/api/v1/announcements/${encodeURIComponent(id)}/dismiss`) },
}
