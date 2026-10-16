import client from '@/shared/api/client'

import { t } from '@/shared/i18n'
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
  source_url?: string
}

export const instanceApi = {
  async getInstance(): Promise<InstanceInfo> {
    const { data } = await client.get<InstanceInfo>('/api/v2/instance')
    return data
  },

  async getRules(): Promise<InstanceRule[]> {
    const { data } = await client.get<InstanceRule[]>('/api/v1/instance/rules')
    return data
  },
}

/** Fallback shown when the admin has not configured any rules yet. */
export const DEFAULT_RULES: InstanceRule[] = [
  { id: 'd1', text: t('instance.instanceApi.s1') },
  { id: 'd2', text: t('instance.instanceApi.s2') },
  { id: 'd3', text: t('instance.instanceApi.s3') },
  { id: 'd4', text: t('instance.instanceApi.s4') },
]
