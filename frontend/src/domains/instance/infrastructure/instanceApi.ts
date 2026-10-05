import client from '@/shared/api/client'

export interface InstanceRule {
  id: string
  text: string
}

export interface InstanceInfo {
  domain?: string
  uri?: string
  title?: string
  description?: string
  short_description?: string
  stats?: { user_count?: number; status_count?: number; domain_count?: number }
  usage?: { users?: { active_month?: number } }
  registrations?: {
    enabled?: boolean
    approval_required?: boolean
    invite_required?: boolean
    message?: string
  }
  contact?: { email?: string }
}

export const instanceApi = {
  async getInstance(): Promise<InstanceInfo> {
    const { data } = await client.get<InstanceInfo>('/api/v1/instance')
    return data
  },

  async getRules(): Promise<InstanceRule[]> {
    const { data } = await client.get<InstanceRule[]>('/api/v1/instance/rules')
    return data
  },
}

/** Fallback shown when the admin has not configured any rules yet. */
export const DEFAULT_RULES: InstanceRule[] = [
  { id: 'd1', text: '友善、尊重他人' },
  { id: 'd2', text: '禁止发布色情内容，严禁任何涉及未成年人的性内容' },
  { id: 'd3', text: '禁止发布恐怖主义相关内容' },
  { id: 'd4', text: '禁止仇恨言论' },
]
