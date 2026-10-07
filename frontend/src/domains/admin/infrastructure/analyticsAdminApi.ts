import client from '@/shared/api/client'
export interface AnalyticsDay { date: string; local_posts: number; remote_posts: number; posting_accounts: number }
export interface AdminAnalytics {
  generated_at: string; timezone: string; days: number
  totals: Record<string, number>
  daily: AnalyticsDay[]
  cohorts: { date: string; size: number; retained: number }[]
  federation: Record<'outgoing' | 'incoming', { domain: string; count: number }[]>
  online: { available: boolean; reason: string }
}
export const analyticsAdminApi = {
  async get(days: 7 | 30 = 30) {
    return (await client.get<AdminAnalytics>('/api/v1/admin/analytics', { params: { days } })).data
  },
}
