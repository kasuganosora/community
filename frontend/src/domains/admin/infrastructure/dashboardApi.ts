import client from '@/shared/api/client'
export interface AdminMeasure { key: string; total: string; unit: string | null }
export const dashboardApi = {
  async measures() {
    return (await client.get<AdminMeasure[]>('/api/v1/admin/measures', {
      params: { 'keys[]': ['user_count', 'status_count', 'new_users', 'opened_reports', 'resolved_reports'] },
    })).data
  },
}
